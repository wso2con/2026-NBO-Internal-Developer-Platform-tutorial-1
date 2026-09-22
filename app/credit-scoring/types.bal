// types.bal — wire contract for credit-scoring (BUILD-SPEC.md §7.2).
//
// Field names are snake_case because they are the JSON contract shared with loan-api
// (Ballerina) and the demo payloads. Do not rename them to suit the language.

import ballerina/http;

// A scoring request — the seven fields §7.2 defines. Deliberately an OPEN record:
// loan-api holds a fuller application (applicant_name, national_id, wallet_msisdn),
// and the demo payload files carry all of it. Ignoring fields we do not score on
// means the same payload can be POSTed to loan-api and to this service, which is
// what the presenter does at T+0:49. Unknown fields are ignored, never scored.
public type ScoreRequest record {
    string application_id;
    decimal amount_kes;
    int term_months;
    decimal monthly_income;
    int employment_months;
    boolean kyc_verified;
    int existing_defaults;
};

// One line of the score breakdown. The presenter uses this to explain a decline
// without opening the code (§7.2).
public type Factor record {|
    string name;
    int points;
|};

public type ScoreResponse record {|
    string application_id;
    int score;
    string decision;
    string reason;
    Factor[] factors;
|};

// A debt-to-income band and the points it awards (§6).
public type DtiBand record {|
    string range;
    int points;
|};

// The live thresholds, served by GET /rules so the rules are inspectable (§7.2).
public type Rules record {|
    int base_points;
    int approval_threshold;
    int score_min;
    int score_max;
    decimal annual_flat_rate;
    int income_points_cap;
    decimal income_points_divisor;
    int employment_points_cap;
    int kyc_verified_points;
    int kyc_unverified_points;
    int default_penalty_per_occurrence;
    DtiBand[] dti_bands;
|};

// §7.2 specifies a 200 for POST /score. Ballerina maps a bare record returned from a
// `post` resource to 201 Created, so the status is pinned explicitly here.
public type ScoreOk record {|
    *http:Ok;
    ScoreResponse body;
|};

// Field-level validation failure, returned as 400 (§7.1's error shape).
public type FieldError record {|
    string 'field;
    string message;
|};

public type ValidationErrors record {|
    string message;
    FieldError[] errors;
|};
