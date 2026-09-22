// db.go — persistence (BUILD-SPEC.md §5, §7.1).
//
// Plain SQL through pgx, no ORM: §7.1's flow is four statements and an ORM would
// hide the ON CONFLICT that makes duplicate submission safe.
//
// Connected LAZILY, like the Ballerina service: startup validates configuration,
// /readyz checks reachability. That keeps init() independent of whether Postgres
// happens to be up, which matters when the platform starts everything at once.
package main

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type store struct {
	url  string
	mu   sync.Mutex
	pool *pgxpool.Pool
}

func newStore(databaseURL string) *store {
	return &store{url: databaseURL}
}

func (s *store) conn(ctx context.Context) (*pgxpool.Pool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil {
		return s.pool, nil
	}
	pool, err := pgxpool.New(ctx, s.url)
	if err != nil {
		return nil, fmt.Errorf("could not connect to the database: %w", err)
	}
	s.pool = pool
	return pool, nil
}

func (s *store) ping(ctx context.Context) error {
	pool, err := s.conn(ctx)
	if err != nil {
		return err
	}
	return pool.Ping(ctx)
}

func (s *store) close() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.pool != nil {
		s.pool.Close()
	}
}

// insertPending writes the PENDING row. ON CONFLICT DO NOTHING means a duplicate
// is detected by the write itself rather than by a separate SELECT, so two
// concurrent submissions of the same id cannot both proceed.
func (s *store) insertPending(ctx context.Context, id string, req ApplicationRequest) (bool, error) {
	pool, err := s.conn(ctx)
	if err != nil {
		return false, err
	}
	tag, err := pool.Exec(ctx, `
		INSERT INTO loan_applications (
			application_id, applicant_name, national_id, wallet_msisdn,
			amount_kes, term_months, monthly_income, employment_months,
			kyc_verified, existing_defaults, decision
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,'PENDING')
		ON CONFLICT (application_id) DO NOTHING`,
		id, req.ApplicantName, req.NationalID, req.WalletMSISDN,
		req.AmountKES, req.TermMonths, req.MonthlyIncome, req.EmploymentMonths,
		req.KYCVerified, req.ExistingDefaults)
	if err != nil {
		return false, err
	}
	return tag.RowsAffected() == 1, nil
}

func (s *store) persistDecision(ctx context.Context, id string, score int, decision, reason string) error {
	pool, err := s.conn(ctx)
	if err != nil {
		return err
	}
	_, err = pool.Exec(ctx, `
		UPDATE loan_applications
		   SET score = $2, decision = $3, decision_reason = $4, decided_at = now()
		 WHERE application_id = $1`,
		id, score, decision, reason)
	return err
}

// currentDecision distinguishes a settled duplicate from a PENDING one that still
// needs scoring. A missing row returns ("", nil) rather than an error.
func (s *store) currentDecision(ctx context.Context, id string) (string, error) {
	pool, err := s.conn(ctx)
	if err != nil {
		return "", err
	}
	var decision *string
	err = pool.QueryRow(ctx,
		`SELECT decision FROM loan_applications WHERE application_id = $1`, id).Scan(&decision)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if decision == nil {
		return "", nil
	}
	return *decision, nil
}

// fetchApplication returns everything §7.1 asks for on the single fetch. The
// disbursement and arrears rows are written by other components, so both stay
// null until those have run — hence the LEFT JOINs.
func (s *store) fetchApplication(ctx context.Context, id string) (*ApplicationRecord, error) {
	pool, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}

	var (
		rec                ApplicationRecord
		disbursementID     *string
		disbursedAmount    *float64
		disbursedMSISDN    *string
		providerRef        *string
		disbursementStatus *string
		disbursedAt        *string
	)

	err = pool.QueryRow(ctx, `
		SELECT l.application_id, l.applicant_name, l.national_id, l.wallet_msisdn,
		       l.amount_kes, l.term_months, l.monthly_income, l.employment_months,
		       l.kyc_verified, l.existing_defaults, l.score, l.decision, l.decision_reason,
		       l.submitted_at::text, l.decided_at::text,
		       d.disbursement_id, d.amount_kes, d.wallet_msisdn, d.provider_ref,
		       d.status, d.disbursed_at::text,
		       a.bucket, a.days_past_due
		  FROM loan_applications l
		  LEFT JOIN disbursements d ON d.application_id = l.application_id
		  LEFT JOIN arrears_classification a ON a.application_id = l.application_id
		 WHERE l.application_id = $1`, id).Scan(
		&rec.ApplicationID, &rec.ApplicantName, &rec.NationalID, &rec.WalletMSISDN,
		&rec.AmountKES, &rec.TermMonths, &rec.MonthlyIncome, &rec.EmploymentMonths,
		&rec.KYCVerified, &rec.ExistingDefaults, &rec.Score, &rec.Decision, &rec.DecisionReason,
		&rec.SubmittedAt, &rec.DecidedAt,
		&disbursementID, &disbursedAmount, &disbursedMSISDN, &providerRef,
		&disbursementStatus, &disbursedAt,
		&rec.ArrearsBucket, &rec.DaysPastDue)

	// No such application is a 404, not a 500 — pgx.ErrNoRows has to be told apart
	// from a genuine database fault.
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}

	if disbursementID != nil {
		d := Disbursement{DisbursementID: *disbursementID, ProviderRef: providerRef}
		if disbursedAmount != nil {
			d.AmountKES = *disbursedAmount
		}
		if disbursedMSISDN != nil {
			d.WalletMSISDN = *disbursedMSISDN
		}
		if disbursementStatus != nil {
			d.Status = *disbursementStatus
		}
		if disbursedAt != nil {
			d.DisbursedAt = *disbursedAt
		}
		rec.Disbursement = &d
	}

	repayments, err := s.fetchRepayments(ctx, id)
	if err != nil {
		return nil, err
	}
	rec.Repayments = repayments

	return &rec, nil
}

func (s *store) fetchRepayments(ctx context.Context, id string) ([]Instalment, error) {
	pool, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}
	rows, err := pool.Query(ctx, `
		SELECT instalment_no, due_date::text, amount_due_kes, paid_date::text
		  FROM repayments
		 WHERE application_id = $1
		 ORDER BY instalment_no`, id)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []Instalment{}
	for rows.Next() {
		var i Instalment
		if err := rows.Scan(&i.InstalmentNo, &i.DueDate, &i.AmountDueKES, &i.PaidDate); err != nil {
			return nil, err
		}
		out = append(out, i)
	}
	return out, rows.Err()
}

// listApplications returns newest first, optionally filtered by arrears bucket.
func (s *store) listApplications(ctx context.Context, limit int, bucket string) ([]ApplicationSummary, error) {
	pool, err := s.conn(ctx)
	if err != nil {
		return nil, err
	}

	var rows pgx.Rows
	if bucket != "" {
		rows, err = pool.Query(ctx, `
			SELECT l.application_id, l.applicant_name, l.amount_kes, l.term_months,
			       l.score, l.decision, l.submitted_at::text, a.bucket
			  FROM loan_applications l
			  JOIN arrears_classification a ON a.application_id = l.application_id
			 WHERE a.bucket = $1
			 ORDER BY l.submitted_at DESC
			 LIMIT $2`, bucket, limit)
	} else {
		rows, err = pool.Query(ctx, `
			SELECT l.application_id, l.applicant_name, l.amount_kes, l.term_months,
			       l.score, l.decision, l.submitted_at::text, a.bucket
			  FROM loan_applications l
			  LEFT JOIN arrears_classification a ON a.application_id = l.application_id
			 ORDER BY l.submitted_at DESC
			 LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := []ApplicationSummary{}
	for rows.Next() {
		var s ApplicationSummary
		if err := rows.Scan(&s.ApplicationID, &s.ApplicantName, &s.AmountKES, &s.TermMonths,
			&s.Score, &s.Decision, &s.SubmittedAt, &s.ArrearsBucket); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}
