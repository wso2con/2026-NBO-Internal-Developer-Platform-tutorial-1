#!/usr/bin/env bash
# apply.sh — applies migrations then seed data. Run by the `migrate` compose service.
#
# Plain psql over plain .sql files; no migration framework (BUILD-SPEC.md §3.4, §14).
# Re-runnable: migrations are skipped once the schema exists, and seed.sql deletes its
# own rows before inserting, so `make seed` is safe to run repeatedly.

set -euo pipefail

schema_exists() {
    local found
    found=$(psql -tAc \
        "SELECT 1 FROM information_schema.tables
          WHERE table_schema = 'public' AND table_name = 'loan_applications'")
    [[ "$found" == "1" ]]
}

if schema_exists; then
    echo "migrate: schema already present, skipping migrations"
else
    for f in /db/migrations/*.sql; do
        echo "migrate: applying $(basename "$f")"
        psql -v ON_ERROR_STOP=1 -q -f "$f"
    done
fi

echo "migrate: seeding"
psql -v ON_ERROR_STOP=1 -q -f /db/seed/seed.sql

# Report what landed, so `make seed` is self-verifying.
psql -P pager=off -c "
SELECT l.application_id,
       l.applicant_name,
       l.amount_kes,
       l.term_months,
       l.score,
       l.decision,
       (CURRENT_DATE - MIN(r.due_date) FILTER (WHERE r.paid_date IS NULL)) AS dpd_today,
       COUNT(r.*)                                        AS instalments,
       COUNT(r.*) FILTER (WHERE r.paid_date IS NULL)     AS unpaid,
       SUM(r.amount_due_kes) FILTER (WHERE r.paid_date IS NULL) AS outstanding_kes
  FROM loan_applications l
  JOIN repayments r ON r.application_id = l.application_id
 GROUP BY l.application_id, l.applicant_name, l.amount_kes,
          l.term_months, l.score, l.decision
 ORDER BY dpd_today DESC;
"

echo "migrate: done"
