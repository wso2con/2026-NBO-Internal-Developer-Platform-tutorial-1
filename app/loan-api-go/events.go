// events.go — publishing to NATS (BUILD-SPEC.md §8).
//
// Subjects are prefixed `loan.`, payloads are JSON.
//
// TRACE CONTEXT. §8 says every event carries `traceparent` in the NATS header.
// The Ballerina implementation could not do that — ballerinax/nats exposes no
// header API — so traceparent travels as a FIELD IN THE JSON PAYLOAD, and the Go
// disbursement-worker reads it from the body. This service keeps the payload
// field so the two APIs are interchangeable in front of the same worker, and
// ALSO sets the real NATS header, which nats.go supports: a consumer may use
// either. Removing the payload field would break the worker.
package main

import (
	"encoding/json"
	"fmt"
	"sync"

	"github.com/nats-io/nats.go"
)

const (
	subjectSubmitted = "loan.submitted"
	subjectApproved  = "loan.approved"
)

type publisher struct {
	url  string
	mu   sync.Mutex
	conn *nats.Conn
}

func newPublisher(url string) *publisher {
	return &publisher{url: url}
}

// Lazy, for the same reason as the database: startup validates configuration,
// /readyz checks reachability.
func (p *publisher) client() (*nats.Conn, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil && p.conn.IsConnected() {
		return p.conn, nil
	}
	conn, err := nats.Connect(p.url,
		nats.MaxReconnects(-1),
		nats.Name("loan-api-go"))
	if err != nil {
		return nil, fmt.Errorf("could not connect to NATS: %w", err)
	}
	p.conn = conn
	return conn, nil
}

func (p *publisher) ping() error {
	_, err := p.client()
	return err
}

func (p *publisher) close() {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.conn != nil {
		p.conn.Drain() //nolint:errcheck // best effort on shutdown
	}
}

func (p *publisher) publish(subject string, payload map[string]any, traceparent string) error {
	conn, err := p.client()
	if err != nil {
		return err
	}
	body := make(map[string]any, len(payload)+1)
	for k, v := range payload {
		body[k] = v
	}
	if traceparent != "" {
		body["traceparent"] = traceparent
	}
	encoded, err := json.Marshal(body)
	if err != nil {
		return err
	}

	msg := nats.NewMsg(subject)
	msg.Data = encoded
	if traceparent != "" {
		msg.Header.Set("traceparent", traceparent)
	}
	return conn.PublishMsg(msg)
}

// publishSubmitted goes out right after the PENDING insert. Informational: a
// failure here is logged and swallowed.
func (p *publisher) publishSubmitted(app *ApplicationRecord, traceparent string) error {
	return p.publish(subjectSubmitted, map[string]any{
		"application_id": app.ApplicationID,
		"amount_kes":     app.AmountKES,
		"term_months":    app.TermMonths,
		"submitted_at":   app.SubmittedAt,
	}, traceparent)
}

// publishApproved is the event the disbursement-worker consumes (§7.3).
// Published only for APPROVED applications, and only once — a duplicate
// submission must not re-publish (§7.1). A failure here IS fatal to the request.
func (p *publisher) publishApproved(app *ApplicationRecord, traceparent string) error {
	return p.publish(subjectApproved, map[string]any{
		"application_id": app.ApplicationID,
		"applicant_name": app.ApplicantName,
		"amount_kes":     app.AmountKES,
		"term_months":    app.TermMonths,
		"wallet_msisdn":  app.WalletMSISDN,
		"score":          app.Score,
		"approved_at":    app.DecidedAt,
	}, traceparent)
}
