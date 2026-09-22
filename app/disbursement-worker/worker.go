// worker.go — the disbursement flow (BUILD-SPEC.md §7.3).
//
// The order of steps below is NOT negotiable. The Valkey claim comes before the
// payment call, not after, and it is not best-effort: it is the only thing
// standing between a duplicated event and a customer being paid twice.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go/jetstream"
	"github.com/prometheus/client_golang/prometheus"
)

// loanApproved is the event loan-api publishes (§8).
//
// NOTE traceparent: §8 says it travels in the NATS header, but ballerinax/nats
// 3.3.2 has no header API at all, so loan-api carries it as a payload field.
// This struct matches what the publisher actually sends. Flagged with the spec
// author; if the connector gains header support, both sides move together.
type loanApproved struct {
	ApplicationID string  `json:"application_id"`
	ApplicantName string  `json:"applicant_name"`
	AmountKES     float64 `json:"amount_kes"`
	TermMonths    int     `json:"term_months"`
	WalletMSISDN  string  `json:"wallet_msisdn"`
	Score         int     `json:"score"`
	ApprovedAt    string  `json:"approved_at"`
	Traceparent   string  `json:"traceparent,omitempty"`
}

type metrics struct {
	suppressed prometheus.Counter
	disbursed  prometheus.Counter
	failed     prometheus.Counter
	retried    prometheus.Counter
}

func newMetrics(reg prometheus.Registerer) *metrics {
	m := &metrics{
		// §10.4 asserts on this one: preferring a metric over log-scraping,
		// because log-scraping is brittle.
		suppressed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "disbursements_suppressed_total",
			Help: "Disbursements suppressed because the loan was already claimed.",
		}),
		disbursed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "disbursements_total",
			Help: "Disbursements successfully sent to the payment rail.",
		}),
		failed: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "disbursement_failures_total",
			Help: "Disbursements abandoned after exhausting retries.",
		}),
		retried: prometheus.NewCounter(prometheus.CounterOpts{
			Name: "disbursement_retries_total",
			Help: "Payment attempts that failed and were scheduled for retry.",
		}),
	}
	reg.MustRegister(m.suppressed, m.disbursed, m.failed, m.retried)
	return m
}

type worker struct {
	cfg      config
	claims   *claimStore
	payments *paymentClient
	db       *store
	events   *publisher
	metrics  *metrics
	log      *slog.Logger
}

// handle processes one loan.approved message. It returns nothing: every path
// either acks or naks the message itself, because a message that is neither is
// redelivered on the ack-wait timeout and looks like a mystery duplicate.
func (w *worker) handle(ctx context.Context, msg jetstream.Msg) {
	var evt loanApproved
	if err := json.Unmarshal(msg.Data(), &evt); err != nil {
		// Malformed events can never succeed, so retrying is pointless — ack and
		// move on rather than blocking the consumer forever.
		w.log.Error("malformed loan.approved event, discarding", "error", err)
		_ = msg.Ack()
		return
	}

	log := w.log.With("application_id", evt.ApplicationID)

	// --- step 2: claim the loan BEFORE any money moves ----------------------
	acquired, holder, err := w.claims.claim(ctx, evt.ApplicationID, w.cfg.workerInstance)
	if err != nil {
		// Valkey is unreachable. We cannot prove this loan is unclaimed, so we
		// must not pay. Retry later.
		log.Error("could not reach valkey, will retry", "error", err)
		_ = msg.NakWithDelay(nakBackoff)
		return
	}

	if !acquired {
		// THE most important behaviour in the demo (§7.3). Ack and stop: the
		// loan is already being handled, or has been.
		log.Info("duplicate disbursement suppressed", "held_by", holder)
		w.metrics.suppressed.Inc()
		_ = msg.Ack()
		return
	}

	log.Info("disbursement claimed", "held_by", w.cfg.workerInstance)

	// Belt and braces behind the claim: if Valkey lost its data but the database
	// already records a payment, do not pay again.
	if already, err := w.db.alreadyDisbursed(ctx, evt.ApplicationID); err == nil && already {
		log.Warn("duplicate disbursement suppressed", "held_by", "database")
		w.metrics.suppressed.Inc()
		_ = msg.Ack()
		return
	}

	attempt := deliveryAttempt(msg)

	// --- step 3: send the payment -------------------------------------------
	result, err := w.payments.pay(ctx, evt.ApplicationID, evt.WalletMSISDN, evt.AmountKES)
	if err != nil {
		// Release the claim so the retry can acquire it again (§7.3).
		if relErr := w.claims.release(ctx, evt.ApplicationID); relErr != nil {
			log.Error("could not release claim after payment failure", "error", relErr)
		}

		if attempt >= w.cfg.maxPaymentTries {
			log.Error("payment failed, giving up",
				"attempts", attempt, "error", err)
			w.metrics.failed.Inc()
			if dbErr := w.db.recordFailure(ctx, evt.ApplicationID,
				disbursementID(evt.ApplicationID), evt.WalletMSISDN,
				ToCents(evt.AmountKES), time.Now().UTC()); dbErr != nil {
				log.Error("could not record failed disbursement", "error", dbErr)
			}
			if pubErr := w.events.publishDisbursementFailed(ctx, evt, err.Error(), attempt); pubErr != nil {
				log.Error("could not publish loan.disbursement_failed", "error", pubErr)
			}
			_ = msg.Ack() // abandoned deliberately; do not redeliver forever
			return
		}

		log.Warn("payment failed, will retry",
			"attempt", attempt, "max_attempts", w.cfg.maxPaymentTries, "error", err)
		w.metrics.retried.Inc()
		_ = msg.NakWithDelay(nakBackoff)
		return
	}

	log.Info("payment sent",
		"provider_ref", result.ProviderRef,
		"amount_kes", evt.AmountKES)

	// --- step 4: record it and build the repayment schedule ------------------
	disbursedAt := time.Now().UTC()
	schedule, err := BuildSchedule(ToCents(evt.AmountKES), evt.TermMonths, disbursedAt)
	if err != nil {
		// The money is already sent, so this cannot be retried by paying again.
		// The provider's own idempotency would return the same ref, but a bad
		// term is a data problem no retry fixes — surface it loudly.
		log.Error("payment sent but schedule could not be built", "error", err)
		_ = msg.Ack()
		return
	}

	if err := w.db.recordDisbursement(ctx, evt.ApplicationID,
		disbursementID(evt.ApplicationID), evt.WalletMSISDN, result.ProviderRef,
		ToCents(evt.AmountKES), schedule, disbursedAt); err != nil {
		// Payment succeeded but persistence failed. Retrying is safe: the
		// payment rail is idempotent on the same key, so it returns the same
		// provider_ref rather than paying again. Keep the claim.
		log.Error("payment sent but could not be recorded, will retry", "error", err)
		w.metrics.retried.Inc()
		_ = msg.NakWithDelay(nakBackoff)
		return
	}

	// --- step 5: announce it -------------------------------------------------
	if err := w.events.publishDisbursed(ctx, evt, disbursementID(evt.ApplicationID),
		result.ProviderRef, disbursedAt); err != nil {
		log.Error("disbursement recorded but could not publish loan.disbursed", "error", err)
	}

	w.metrics.disbursed.Inc()
	log.Info("disbursement complete",
		"provider_ref", result.ProviderRef,
		"instalments", len(schedule))

	// --- step 6: ack ---------------------------------------------------------
	_ = msg.Ack()
}

// deliveryAttempt reports which attempt this is, 1-based. JetStream tracks it,
// so the count survives a worker restart — which a local counter would not.
func deliveryAttempt(msg jetstream.Msg) int {
	meta, err := msg.Metadata()
	if err != nil {
		return 1
	}
	return int(meta.NumDelivered)
}

// One disbursement per application, so the id is derived rather than random.
// That makes the insert naturally idempotent under ON CONFLICT.
func disbursementID(applicationID string) string {
	return "DSB-" + applicationID
}

func (w *worker) describe() string {
	return fmt.Sprintf("instance=%s ttl=%s max_tries=%d",
		w.cfg.workerInstance, w.cfg.idempotencyTTL, w.cfg.maxPaymentTries)
}
