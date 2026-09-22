// infra.go — the four things the worker talks to: Valkey, the payment rail,
// Postgres and NATS (BUILD-SPEC.md §7.3).
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
)

// --- Valkey claim -----------------------------------------------------------

// claimStore implements step 2 of §7.3's flow — the single most important
// behaviour in the demo.
type claimStore struct {
	rdb *redis.Client
	ttl time.Duration
}

func newClaimStore(valkeyURL string, ttl time.Duration) (*claimStore, error) {
	opts, err := redis.ParseURL(valkeyURL)
	if err != nil {
		return nil, fmt.Errorf("VALKEY_URL is invalid: %w", err)
	}
	return &claimStore{rdb: redis.NewClient(opts), ttl: ttl}, nil
}

func claimKey(applicationID string) string {
	return "disb:" + applicationID
}

// claim attempts SET disb:{id} {instance} NX EX={ttl}.
//
// Returns acquired=false plus the instance already holding it when the key
// exists. NX is what makes this atomic: two workers racing the same message
// cannot both succeed, which is the whole guarantee.
func (c *claimStore) claim(ctx context.Context, applicationID, instance string) (bool, string, error) {
	key := claimKey(applicationID)
	ok, err := c.rdb.SetNX(ctx, key, instance, c.ttl).Result()
	if err != nil {
		return false, "", fmt.Errorf("valkey claim failed: %w", err)
	}
	if ok {
		return true, instance, nil
	}
	// Losing the race is the normal path for a duplicate; report who holds it.
	holder, err := c.rdb.Get(ctx, key).Result()
	if err != nil {
		// The key expired between SETNX and GET. Unknown holder, still not ours.
		return false, "unknown", nil
	}
	return false, holder, nil
}

// release removes the claim so the message can be retried after a payment
// failure (§7.3). Without this a transient provider error would permanently
// block that loan.
func (c *claimStore) release(ctx context.Context, applicationID string) error {
	return c.rdb.Del(ctx, claimKey(applicationID)).Err()
}

func (c *claimStore) ping(ctx context.Context) error {
	return c.rdb.Ping(ctx).Err()
}

func (c *claimStore) close() error { return c.rdb.Close() }

// --- payment rail -----------------------------------------------------------

type paymentClient struct {
	baseURL string
	apiKey  string
	http    *http.Client
}

type paymentResult struct {
	ProviderRef string `json:"provider_ref"`
	Status      string `json:"status"`
	Duplicate   bool   `json:"duplicate"`
}

func newPaymentClient(baseURL, apiKey string) *paymentClient {
	return &paymentClient{
		baseURL: baseURL,
		apiKey:  apiKey,
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

// pay POSTs to the provider with Idempotency-Key set to the application id
// (§7.3 step 3), so a retry after a network failure cannot pay twice.
func (p *paymentClient) pay(ctx context.Context, applicationID, msisdn string, amountKES float64) (paymentResult, error) {
	var out paymentResult

	body, err := json.Marshal(map[string]any{
		"application_id": applicationID,
		"amount_kes":     amountKES,
		"wallet_msisdn":  msisdn,
	})
	if err != nil {
		return out, err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.baseURL+"/v1/payments", bytes.NewReader(body))
	if err != nil {
		return out, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Idempotency-Key", applicationID)
	req.Header.Set("Authorization", "Bearer "+p.apiKey)

	resp, err := p.http.Do(req)
	if err != nil {
		return out, fmt.Errorf("payment rail unreachable: %w", err)
	}
	defer resp.Body.Close()

	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("payment rail returned %d: %s", resp.StatusCode, string(raw))
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return out, fmt.Errorf("payment rail returned malformed JSON: %w", err)
	}
	if out.ProviderRef == "" {
		return out, fmt.Errorf("payment rail returned no provider_ref")
	}
	return out, nil
}

// --- postgres ---------------------------------------------------------------

type store struct {
	pool *pgxpool.Pool
}

func newStore(ctx context.Context, databaseURL string) (*store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("DATABASE_URL is invalid: %w", err)
	}
	return &store{pool: pool}, nil
}

func (s *store) ping(ctx context.Context) error {
	return s.pool.Ping(ctx)
}

func (s *store) close() { s.pool.Close() }

// recordDisbursement writes the disbursement row and the whole repayment
// schedule in ONE transaction (§7.3 step 4). A loan with money sent but no
// schedule, or a schedule with no disbursement, would both be worse than a
// clean failure and a retry.
func (s *store) recordDisbursement(
	ctx context.Context,
	applicationID, disbursementID, msisdn, providerRef string,
	amountCents int64,
	schedule []Instalment,
	disbursedAt time.Time,
) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	_, err = tx.Exec(ctx, `
		INSERT INTO disbursements (
			disbursement_id, application_id, amount_kes, wallet_msisdn,
			provider_ref, status, disbursed_at
		) VALUES ($1, $2, $3, $4, $5, 'SENT', $6)
		ON CONFLICT (disbursement_id) DO NOTHING
	`, disbursementID, applicationID, FromCents(amountCents), msisdn, providerRef, disbursedAt)
	if err != nil {
		return fmt.Errorf("insert disbursement: %w", err)
	}

	for _, inst := range schedule {
		_, err = tx.Exec(ctx, `
			INSERT INTO repayments (application_id, instalment_no, due_date, amount_due_kes)
			VALUES ($1, $2, $3, $4)
			ON CONFLICT (application_id, instalment_no) DO NOTHING
		`, applicationID, inst.Number, inst.DueDate, FromCents(inst.AmountKES))
		if err != nil {
			return fmt.Errorf("insert instalment %d: %w", inst.Number, err)
		}
	}

	return tx.Commit(ctx)
}

// recordFailure marks a disbursement FAILED after the retries are exhausted
// (§7.3). No repayment schedule is generated — nothing was paid.
func (s *store) recordFailure(ctx context.Context, applicationID, disbursementID, msisdn string,
	amountCents int64, disbursedAt time.Time) error {
	_, err := s.pool.Exec(ctx, `
		INSERT INTO disbursements (
			disbursement_id, application_id, amount_kes, wallet_msisdn,
			provider_ref, status, disbursed_at
		) VALUES ($1, $2, $3, $4, NULL, 'FAILED', $5)
		ON CONFLICT (disbursement_id) DO NOTHING
	`, disbursementID, applicationID, FromCents(amountCents), msisdn, disbursedAt)
	return err
}

// alreadyDisbursed is a belt-and-braces check behind the Valkey claim. The claim
// is the real guard; this catches the case where Valkey lost its data (a restart
// with no persistence) while the database still holds the truth.
func (s *store) alreadyDisbursed(ctx context.Context, applicationID string) (bool, error) {
	var count int
	err := s.pool.QueryRow(ctx,
		`SELECT count(*) FROM disbursements WHERE application_id = $1 AND status = 'SENT'`,
		applicationID).Scan(&count)
	return count > 0, err
}
