// store.go — the two queries this job needs (BUILD-SPEC.md §7.4).
package main

import (
	"context"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

// assess finds every loan that is APPROVED and has at least one disbursement,
// then works out its days past due from the oldest unpaid instalment falling
// before the as-of date (§7.4).
//
// A loan disbursed today has every instalment in the future, so the FILTER
// matches nothing, oldest_unpaid_due comes back NULL and days past due is 0 —
// which is why APP-100244 is CURRENT rather than overdue.
func assess(ctx context.Context, pool *pgxpool.Pool, asOf time.Time) ([]Classification, error) {
	rows, err := pool.Query(ctx, `
		SELECT l.application_id,
		       l.applicant_name,
		       MIN(r.due_date) FILTER (
		           WHERE r.paid_date IS NULL AND r.due_date < $1
		       ) AS oldest_unpaid_due,
		       COALESCE(SUM(r.amount_due_kes) FILTER (WHERE r.paid_date IS NULL), 0) AS outstanding
		  FROM loan_applications l
		  JOIN repayments r ON r.application_id = l.application_id
		 WHERE l.decision = 'APPROVED'
		   AND EXISTS (
		       SELECT 1 FROM disbursements d
		        WHERE d.application_id = l.application_id
		   )
		 GROUP BY l.application_id, l.applicant_name
	`, asOf)
	if err != nil {
		return nil, fmt.Errorf("assess query failed: %w", err)
	}
	defer rows.Close()

	var out []Classification
	for rows.Next() {
		var (
			id          string
			name        string
			oldestDue   *time.Time
			outstanding float64
		)
		if err := rows.Scan(&id, &name, &oldestDue, &outstanding); err != nil {
			return nil, fmt.Errorf("scanning assessment row: %w", err)
		}
		dpd := DaysPastDue(oldestDue, asOf)
		out = append(out, Classification{
			ApplicationID:  id,
			ApplicantName:  name,
			OutstandingKES: outstanding,
			DaysPastDue:    dpd,
			Bucket:         Classify(dpd),
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("reading assessment rows: %w", err)
	}
	return out, nil
}

// upsert writes the classifications (§7.4). One transaction: a half-written
// arrears table is worse than an unchanged one, because the collections queue
// would silently be missing loans.
func upsert(ctx context.Context, pool *pgxpool.Pool, rows []Classification, asOf time.Time) error {
	tx, err := pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	batch := &pgx.Batch{}
	classifiedAt := time.Now().UTC()
	for _, r := range rows {
		batch.Queue(`
			INSERT INTO arrears_classification
			       (application_id, days_past_due, bucket, outstanding_kes, classified_at)
			VALUES ($1, $2, $3, $4, $5)
			ON CONFLICT (application_id) DO UPDATE
			   SET days_past_due   = EXCLUDED.days_past_due,
			       bucket          = EXCLUDED.bucket,
			       outstanding_kes = EXCLUDED.outstanding_kes,
			       classified_at   = EXCLUDED.classified_at
		`, r.ApplicationID, r.DaysPastDue, r.Bucket, r.OutstandingKES, classifiedAt)
	}

	results := tx.SendBatch(ctx, batch)
	for range rows {
		if _, err := results.Exec(); err != nil {
			_ = results.Close()
			return fmt.Errorf("upserting classification: %w", err)
		}
	}
	if err := results.Close(); err != nil {
		return fmt.Errorf("closing classification batch: %w", err)
	}

	return tx.Commit(ctx)
}
