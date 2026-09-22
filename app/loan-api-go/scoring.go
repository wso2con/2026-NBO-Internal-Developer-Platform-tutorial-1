// scoring.go — the outbound call to credit-scoring (BUILD-SPEC.md §7.1).
//
// The address arrives in CREDIT_SCORING_URL. No hostname or port is written down
// here (§2.2): on OpenChoreo it is injected from the endpoint dependency, and the
// same image runs in three environments.
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type scorer struct {
	baseURL string
	client  *http.Client
}

func newScorer(baseURL string, timeout time.Duration) *scorer {
	return &scorer{
		baseURL: strings.TrimRight(baseURL, "/"),
		client:  &http.Client{Timeout: timeout},
	}
}

// requestScore calls POST /score. The inbound traceparent is forwarded so the
// trace spans loan-api-go -> credit-scoring (§2.6).
func (s *scorer) requestScore(ctx context.Context, req ApplicationRequest, id, traceparent string) (ScoreResponse, error) {
	var out ScoreResponse

	payload, err := json.Marshal(map[string]any{
		"application_id":    id,
		"amount_kes":        req.AmountKES,
		"term_months":       req.TermMonths,
		"monthly_income":    req.MonthlyIncome,
		"employment_months": req.EmploymentMonths,
		"kyc_verified":      req.KYCVerified,
		"existing_defaults": req.ExistingDefaults,
	})
	if err != nil {
		return out, err
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost,
		s.baseURL+"/score", bytes.NewReader(payload))
	if err != nil {
		return out, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	if traceparent != "" {
		httpReq.Header.Set("traceparent", traceparent)
	}

	resp, err := s.client.Do(httpReq)
	if err != nil {
		return out, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return out, fmt.Errorf("credit-scoring returned %d", resp.StatusCode)
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return out, fmt.Errorf("could not decode the scoring response: %w", err)
	}
	return out, nil
}

// scoreFactorsFor re-scores a stored application purely to recover its factor
// breakdown for the console (§7.5). Safe because the engine is deterministic
// (§6): these are the factors that produced the score already on the record.
// Nothing is persisted and no event is published.
func (s *scorer) scoreFactorsFor(ctx context.Context, app *ApplicationRecord) ([]Factor, error) {
	scored, err := s.requestScore(ctx, ApplicationRequest{
		ApplicantName:    app.ApplicantName,
		NationalID:       app.NationalID,
		WalletMSISDN:     app.WalletMSISDN,
		AmountKES:        app.AmountKES,
		TermMonths:       app.TermMonths,
		MonthlyIncome:    app.MonthlyIncome,
		EmploymentMonths: app.EmploymentMonths,
		KYCVerified:      app.KYCVerified,
		ExistingDefaults: app.ExistingDefaults,
	}, app.ApplicationID, "")
	if err != nil {
		return nil, err
	}
	return scored.Factors, nil
}
