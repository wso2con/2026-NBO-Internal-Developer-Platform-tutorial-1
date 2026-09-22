-- 001_init.sql — Kifaru Bank retail lending demo
-- Schema per BUILD-SPEC.md §5. Applied by the `migrate` compose service.
-- Plain SQL, no migration framework (§3.4).

CREATE TABLE loan_applications (
    application_id   TEXT PRIMARY KEY,
    applicant_name   TEXT        NOT NULL,
    national_id      TEXT        NOT NULL,
    wallet_msisdn    TEXT        NOT NULL,
    amount_kes       NUMERIC(12,2) NOT NULL,
    term_months      INT         NOT NULL,
    monthly_income   NUMERIC(12,2) NOT NULL,
    employment_months INT        NOT NULL,
    kyc_verified     BOOLEAN     NOT NULL DEFAULT FALSE,
    existing_defaults INT        NOT NULL DEFAULT 0,
    score            INT,
    decision         TEXT,          -- PENDING | APPROVED | DECLINED
    decision_reason  TEXT,
    submitted_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    decided_at       TIMESTAMPTZ
);

CREATE TABLE disbursements (
    disbursement_id  TEXT PRIMARY KEY,
    application_id   TEXT NOT NULL REFERENCES loan_applications(application_id),
    amount_kes       NUMERIC(12,2) NOT NULL,
    wallet_msisdn    TEXT NOT NULL,
    provider_ref     TEXT,
    status           TEXT NOT NULL,  -- SENT | FAILED
    disbursed_at     TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE TABLE repayments (
    repayment_id     BIGSERIAL PRIMARY KEY,
    application_id   TEXT NOT NULL REFERENCES loan_applications(application_id),
    instalment_no    INT NOT NULL,
    due_date         DATE NOT NULL,
    amount_due_kes   NUMERIC(12,2) NOT NULL,
    paid_date        DATE,
    UNIQUE (application_id, instalment_no)
);

CREATE TABLE arrears_classification (
    application_id   TEXT PRIMARY KEY REFERENCES loan_applications(application_id),
    days_past_due    INT  NOT NULL,
    bucket           TEXT NOT NULL,   -- CURRENT | DPD_1_30 | DPD_31_60 | DPD_61_90 | NPL_90_PLUS
    outstanding_kes  NUMERIC(12,2) NOT NULL,
    classified_at    TIMESTAMPTZ NOT NULL
);

-- Indexes for the access paths this demo actually uses:
--   arrears-eod (§7.4)  scans approved loans, then the oldest unpaid instalment per loan
--   loan-api    (§7.1)  lists applications newest-first and joins disbursements by application
CREATE INDEX idx_loan_applications_decision   ON loan_applications (decision);
CREATE INDEX idx_loan_applications_submitted  ON loan_applications (submitted_at DESC);
CREATE INDEX idx_disbursements_application    ON disbursements (application_id);
CREATE INDEX idx_repayments_unpaid            ON repayments (application_id, due_date)
                                               WHERE paid_date IS NULL;
