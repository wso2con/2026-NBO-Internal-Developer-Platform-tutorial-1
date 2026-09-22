-- seed.sql — Kifaru Bank retail lending demo
-- Four historical loans, already approved and disbursed, with partial repayment history.
-- These are the collections queue (BUILD-SPEC.md §5 "Seed data").
--
-- APP-100244 and APP-100245 are deliberately NOT seeded — scenarios 1 and 3 create them
-- at runtime (§10.3, §10.5). APP-100244 supplies the CURRENT bucket (§7.4).
--
-- ALL DUE DATES ARE RELATIVE TO CURRENT_DATE. Never write absolute date literals here:
-- a hardcoded date is correct today and silently wrong at the conference (§5).
--
-- How the target DPD is hit exactly: for each loan we pick which instalment is the first
-- unpaid one (`first_unpaid`), then anchor that instalment's due date at
-- CURRENT_DATE - target_dpd. Earlier instalments are marked paid, later ones left unpaid,
-- so the oldest unpaid instalment is past due by exactly target_dpd days -- which is what
-- arrears-eod measures (§7.4).

BEGIN;

-- Idempotent: `make seed` can be run repeatedly without duplicating rows.
-- Children first — all three tables reference loan_applications.
DELETE FROM arrears_classification WHERE application_id LIKE 'APP-1003%';
DELETE FROM repayments            WHERE application_id LIKE 'APP-1003%';
DELETE FROM disbursements         WHERE application_id LIKE 'APP-1003%';
DELETE FROM loan_applications     WHERE application_id LIKE 'APP-1003%';

CREATE TEMP TABLE seed_calc ON COMMIT DROP AS
WITH seed (
    application_id, applicant_name, national_id, wallet_msisdn,
    amount_kes, term_months, monthly_income, employment_months,
    score, decision_reason, target_dpd, first_unpaid
) AS (
    VALUES
      -- score/decision_reason below are consistent with the §6 rule engine for the
      -- income/employment/KYC values given, so the seeded rows would re-score identically.
      ('APP-100301', 'Joseph M.', '22345671', '254712345601',
       150000.00::numeric, 24,  70000.00::numeric, 36,
       666, 'Affordability strong (DTI 0.12); employment history 36 months; KYC verified',
       12,  9),

      ('APP-100302', 'Grace N.',  '22345672', '254712345602',
       250000.00::numeric, 24,  85000.00::numeric, 48,
       708, 'Affordability strong (DTI 0.17); employment history 48 months; KYC verified',
       47,  6),

      ('APP-100303', 'Peter O.',  '22345673', '254712345603',
       120000.00::numeric, 12,  80000.00::numeric, 30,
       680, 'Affordability strong (DTI 0.15); employment history 30 months; KYC verified',
       78,  4),

      ('APP-100304', 'Halima S.', '22345674', '254712345604',
       480000.00::numeric, 36, 120000.00::numeric, 60,
       750, 'Affordability strong (DTI 0.17); employment history 60 months; KYC verified',
       124, 7)
)
SELECT
    s.*,
    -- §5: total repayable = amount x (1 + 0.18 x term/12), flat interest at 18% p.a.
    ROUND(s.amount_kes * (1 + 0.18 * s.term_months / 12.0), 2)                AS total_repayable,
    -- evenly divided, 2dp; the remainder lands on the final instalment (below)
    ROUND(ROUND(s.amount_kes * (1 + 0.18 * s.term_months / 12.0), 2)
          / s.term_months, 2)                                                 AS base_instalment,
    -- the anchor: first unpaid instalment is exactly target_dpd days overdue
    (CURRENT_DATE - s.target_dpd)                                             AS oldest_unpaid_due,
    -- first instalment falls one month after disbursement, so disbursement is
    -- first_unpaid months before the anchor
    ((CURRENT_DATE - s.target_dpd) - (s.first_unpaid || ' months')::interval)::date
                                                                              AS disbursed_on
FROM seed s;

INSERT INTO loan_applications (
    application_id, applicant_name, national_id, wallet_msisdn,
    amount_kes, term_months, monthly_income, employment_months,
    kyc_verified, existing_defaults, score, decision, decision_reason,
    submitted_at, decided_at
)
SELECT
    application_id, applicant_name, national_id, wallet_msisdn,
    amount_kes, term_months, monthly_income, employment_months,
    TRUE, 0, score, 'APPROVED', decision_reason,
    (disbursed_on - INTERVAL '2 days')::timestamptz,
    (disbursed_on - INTERVAL '1 day')::timestamptz
FROM seed_calc;

INSERT INTO disbursements (
    disbursement_id, application_id, amount_kes, wallet_msisdn,
    provider_ref, status, disbursed_at
)
SELECT
    'DSB-' || substring(application_id FROM 5),
    application_id,
    amount_kes,
    wallet_msisdn,
    'PR-SEED-' || substring(application_id FROM 5),
    'SENT',
    disbursed_on::timestamptz + TIME '09:15'
FROM seed_calc;

-- Full schedule per loan. due_date walks monthly either side of the anchor, so
-- instalment `first_unpaid` sits at CURRENT_DATE - target_dpd exactly.
INSERT INTO repayments (application_id, instalment_no, due_date, amount_due_kes, paid_date)
SELECT
    t.application_id,
    n,
    (t.oldest_unpaid_due + ((n - t.first_unpaid) || ' months')::interval)::date,
    CASE WHEN n = t.term_months
         -- rounding remainder goes on the final instalment (§5)
         THEN t.total_repayable - t.base_instalment * (t.term_months - 1)
         ELSE t.base_instalment
    END,
    CASE WHEN n < t.first_unpaid
         -- paid on the day it fell due; everything from first_unpaid on is outstanding
         THEN (t.oldest_unpaid_due + ((n - t.first_unpaid) || ' months')::interval)::date
         ELSE NULL
    END
FROM seed_calc t, generate_series(1, t.term_months) AS n;

COMMIT;
