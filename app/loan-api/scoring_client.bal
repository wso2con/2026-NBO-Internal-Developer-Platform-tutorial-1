// scoring_client.bal — the outbound call to credit-scoring (BUILD-SPEC.md §7.1).
//
// The address arrives in CREDIT_SCORING_URL. There is deliberately no hostname or
// port written down here (§2.2) — on OpenChoreo this is injected from the endpoint
// dependency, and the same image runs in three environments.

import ballerina/http;

http:Client? scoringClient = ();

function initScoringClient(string url, decimal timeoutSeconds) returns error? {
    scoringClient = check new (url, timeout = timeoutSeconds);
}

// Re-scores a stored application purely to recover its factor breakdown for the
// console (§7.5). Safe because the engine is deterministic (§6) — this returns
// exactly the factors that produced the score already on the record. Nothing is
// persisted and no event is published.
function scoreFactorsFor(ApplicationRecord app) returns Factor[]|error {
    ApplicationRequest req = {
        applicant_name: app.applicant_name,
        national_id: app.national_id,
        wallet_msisdn: app.wallet_msisdn,
        amount_kes: app.amount_kes,
        term_months: app.term_months,
        monthly_income: app.monthly_income,
        employment_months: app.employment_months,
        kyc_verified: app.kyc_verified,
        existing_defaults: app.existing_defaults
    };
    ScoreResponse scored = check requestScore(req, app.application_id, ());
    return scored.factors;
}

// Calls POST /score. The traceparent from the inbound request is forwarded so the
// trace spans loan-api -> credit-scoring (§2.6).
function requestScore(ApplicationRequest req, string applicationId, string? traceparent)
        returns ScoreResponse|error {
    http:Client? c = scoringClient;
    if c is () {
        return error("credit-scoring client is not initialised");
    }

    json payload = {
        application_id: applicationId,
        amount_kes: req.amount_kes,
        term_months: req.term_months,
        monthly_income: req.monthly_income,
        employment_months: req.employment_months,
        kyc_verified: req.kyc_verified,
        existing_defaults: req.existing_defaults ?: 0
    };

    map<string|string[]> headers = {};
    if traceparent is string {
        headers["traceparent"] = traceparent;
    }

    return c->post("/score", payload, headers);
}
