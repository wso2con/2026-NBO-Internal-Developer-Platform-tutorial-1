// events.go — publishing loan.disbursed and loan.disbursement_failed (§8).
package main

import (
	"context"
	"encoding/json"
	"time"

	"github.com/nats-io/nats.go"
)

type publisher struct {
	nc *nats.Conn
}

func newPublisher(nc *nats.Conn) *publisher {
	return &publisher{nc: nc}
}

// traceparent is carried as a payload field rather than a NATS header, matching
// what loan-api publishes — see the note on loanApproved in worker.go.
func (p *publisher) publish(_ context.Context, subject string, payload map[string]any, traceparent string) error {
	if traceparent != "" {
		payload["traceparent"] = traceparent
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}
	return p.nc.Publish(subject, body)
}

func (p *publisher) publishDisbursed(ctx context.Context, evt loanApproved,
	disbursementID, providerRef string, at time.Time) error {
	return p.publish(ctx, "loan.disbursed", map[string]any{
		"application_id":  evt.ApplicationID,
		"disbursement_id": disbursementID,
		"provider_ref":    providerRef,
		"amount_kes":      evt.AmountKES,
		"disbursed_at":    at.Format(time.RFC3339),
	}, evt.Traceparent)
}

func (p *publisher) publishDisbursementFailed(ctx context.Context, evt loanApproved,
	reason string, attempts int) error {
	return p.publish(ctx, "loan.disbursement_failed", map[string]any{
		"application_id": evt.ApplicationID,
		"reason":         reason,
		"attempts":       attempts,
		"failed_at":      time.Now().UTC().Format(time.RFC3339),
	}, evt.Traceparent)
}
