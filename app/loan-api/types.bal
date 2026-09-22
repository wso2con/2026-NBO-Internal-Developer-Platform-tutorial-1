// types.bal — wire contract for loan-api (BUILD-SPEC.md §7.1).
//
// Field names are snake_case: they are the JSON contract shared with the demo
// payloads, credit-scoring and the console. Not renamed to suit the language.

import ballerina/http;

// Incoming POST /applications body. application_id is caller-supplied so the demo
// is repeatable and the idempotency story hangs together; if omitted we generate
// APP- plus six digits. Open record: extra fields are ignored, never persisted.
public type ApplicationRequest record {
    string application_id?;
    string applicant_name;
    string national_id;
    string wallet_msisdn;
    decimal amount_kes;
    int term_months;
    decimal monthly_income;
    int employment_months;
    boolean kyc_verified;
    int existing_defaults?;
};

public type Disbursement record {|
    string disbursement_id;
    decimal amount_kes;
    string wallet_msisdn;
    string? provider_ref;
    string status;
    string disbursed_at;
|};

public type Instalment record {|
    int instalment_no;
    string due_date;
    decimal amount_due_kes;
    string? paid_date;
|};

// The full record returned by POST /applications and GET /applications/{id}.
// §7.1 wants score, decision, disbursement and arrears bucket on the single fetch.
public type ApplicationRecord record {|
    string application_id;
    string applicant_name;
    string national_id;
    string wallet_msisdn;
    decimal amount_kes;
    int term_months;
    decimal monthly_income;
    int employment_months;
    boolean kyc_verified;
    int existing_defaults;
    int? score;
    string? decision;
    string? decision_reason;
    string submitted_at;
    string? decided_at;
    Disbursement? disbursement;
    string? arrears_bucket;
    int? days_past_due;
    Instalment[] repayments?;
    // The scoring breakdown the console shows on the detail screen (§7.5).
    //
    // §5's schema has no column for it, and the console cannot ask credit-scoring
    // itself — that service has no public route and never will (§7.2). So loan-api
    // recomputes it on read, which is sound because the engine is deterministic:
    // same inputs, same factors, always (§6).
    //
    // Best-effort and therefore optional: if credit-scoring is unavailable the
    // record is still returned without it, rather than failing a read.
    Factor[] score_factors?;
|};

// Compact row for the list endpoint (§7.1: list, newest first).
public type ApplicationSummary record {|
    string application_id;
    string applicant_name;
    decimal amount_kes;
    int term_months;
    int? score;
    string? decision;
    string submitted_at;
    string? arrears_bucket;
|};

// --- credit-scoring contract (§7.2) -----------------------------------------

public type Factor record {|
    string name;
    int points;
|};

public type ScoreResponse record {
    string application_id;
    int score;
    string decision;
    string reason;
    Factor[] factors;
};

// --- errors -----------------------------------------------------------------

public type FieldError record {|
    string 'field;
    string message;
|};

public type ValidationErrors record {|
    string message;
    FieldError[] errors;
|};

// --- explicit response statuses ---------------------------------------------
// Ballerina maps a bare record returned from a `post` resource to 201 Created, and
// from a `get` to 200. §7.1 needs 201 for a new application and 200 for a duplicate,
// so both are pinned rather than left to inference.

public type ApplicationCreated record {|
    *http:Created;
    ApplicationRecord body;
|};

public type ApplicationExisting record {|
    *http:Ok;
    ApplicationRecord body;
|};
