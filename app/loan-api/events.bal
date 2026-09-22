// events.bal — publishing to NATS (BUILD-SPEC.md §8).
//
// Subjects are prefixed `loan.`, payloads are JSON. The LOANS JetStream stream
// (loan.>, file storage, 24h retention) is created by the `nats-init` compose
// service; messages published to those subjects are captured by it.
//
// TRACE CONTEXT — deviation from §8, flagged with the spec author.
// §8 says every event carries `traceparent` in the NATS header. ballerinax/nats
// 3.3.2 (the latest release) exposes no header API at all: neither AnydataMessage
// nor JetStreamMessage has a headers field. So traceparent travels as a FIELD IN
// THE JSON PAYLOAD instead. The outcome §2.6 actually asks for is preserved — the
// trace still crosses the broker into the Go worker unbroken — but the consumer
// must read it from the body, not the headers. Phase 4's disbursement-worker has
// to match this.

import ballerinax/nats;

const string SUBJECT_SUBMITTED = "loan.submitted";
const string SUBJECT_APPROVED = "loan.approved";

// Connected lazily, for the same reason as the database (see db.bal): startup
// validates configuration, readiness checks reachability.
string? natsUrl = ();
nats:Client? natsClient = ();

function initEvents(string url) returns error? {
    natsUrl = url;
}

function nats_() returns nats:Client|error {
    nats:Client? existing = natsClient;
    if existing is nats:Client {
        return existing;
    }
    string? url = natsUrl;
    if url is () {
        return error("NATS is not configured");
    }
    nats:Client created = check new (url);
    natsClient = created;
    return created;
}

function publishEvent(string subject, map<json> payload, string? traceparent) returns error? {
    nats:Client c = check nats_();
    map<json> body = payload.clone();
    if traceparent is string {
        body["traceparent"] = traceparent;
    }
    check c->publishMessage({subject: subject, content: body.toJsonString().toBytes()});
}

// Published right after the PENDING insert. §7.1's flow does not mention it, but
// §8 lists loan-api as its publisher, and the submission is the only moment it
// can describe.
function publishSubmitted(ApplicationRecord app, string? traceparent) returns error? {
    return publishEvent(SUBJECT_SUBMITTED, {
        application_id: app.application_id,
        amount_kes: app.amount_kes,
        term_months: app.term_months,
        submitted_at: app.submitted_at
    }, traceparent);
}

// The event the disbursement-worker consumes (§7.3). Published only for APPROVED
// applications, and only once — a duplicate submission must not re-publish (§7.1).
function publishApproved(ApplicationRecord app, string? traceparent) returns error? {
    return publishEvent(SUBJECT_APPROVED, {
        application_id: app.application_id,
        applicant_name: app.applicant_name,
        amount_kes: app.amount_kes,
        term_months: app.term_months,
        wallet_msisdn: app.wallet_msisdn,
        score: app.score,
        approved_at: app.decided_at
    }, traceparent);
}

// Readiness probe: forces the lazy connection, so /readyz genuinely reflects
// whether the broker is reachable rather than whether we once configured it.
function pingNats() returns error? {
    _ = check nats_();
}
