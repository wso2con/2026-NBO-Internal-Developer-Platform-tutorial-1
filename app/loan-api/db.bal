// db.bal — persistence (BUILD-SPEC.md §7.1).
//
// Plain parameterised SQL against the §5 schema. No ORM (§14). Timestamps and dates
// are selected as ::text so they cross the wire as ISO strings without civil-time
// conversion — the console and the smoke script both want strings.

import ballerina/sql;
import ballerinax/postgresql;
// Pulls in the Postgres JDBC driver. Imported for its side effect only — without
// it the client fails at runtime with "Error while loading database driver".
import ballerinax/postgresql.driver as _;

// Connection settings are captured at startup; the pool itself is opened on first
// use. init() therefore validates configuration without requiring the database to
// be reachable, which keeps startup independent of dependency availability and lets
// the pure unit tests run with no database at all. Whether the database is actually
// reachable is a READINESS question, and /readyz is where §7.1 puts it.
DbConfig? dbConfig = ();
postgresql:Client? dbClient = ();

function db() returns postgresql:Client|error {
    postgresql:Client? existing = dbClient;
    if existing is postgresql:Client {
        return existing;
    }
    DbConfig? cfg = dbConfig;
    if cfg is () {
        return error("database is not configured");
    }
    postgresql:Client created = check new (
        host = cfg.host,
        username = cfg.user,
        password = cfg.password,
        database = cfg.database,
        port = cfg.port
    );
    dbClient = created;
    return created;
}

function initDatabase(DbConfig cfg) returns error? {
    dbConfig = cfg;
}

// Cheap round trip for /readyz (§7.1).
function pingDatabase() returns error? {
    postgresql:Client c = check db();
    int _ = check c->queryRow(`SELECT 1`);
}

// Inserts the application as PENDING, before scoring is attempted (§7.1's flow).
// Returns false if the id already exists, which is how a duplicate submission is
// detected without a separate SELECT.
function insertPending(string applicationId, ApplicationRequest req) returns boolean|error {
    postgresql:Client c = check db();
    sql:ExecutionResult result = check c->execute(`
        INSERT INTO loan_applications (
            application_id, applicant_name, national_id, wallet_msisdn,
            amount_kes, term_months, monthly_income, employment_months,
            kyc_verified, existing_defaults, decision
        ) VALUES (
            ${applicationId}, ${req.applicant_name}, ${req.national_id}, ${req.wallet_msisdn},
            ${req.amount_kes}, ${req.term_months}, ${req.monthly_income}, ${req.employment_months},
            ${req.kyc_verified}, ${req.existing_defaults ?: 0}, 'PENDING'
        )
        ON CONFLICT (application_id) DO NOTHING
    `);
    return result.affectedRowCount == 1;
}

function persistDecision(string applicationId, int score, string decision, string reason)
        returns error? {
    postgresql:Client c = check db();
    _ = check c->execute(`
        UPDATE loan_applications
           SET score = ${score},
               decision = ${decision},
               decision_reason = ${reason},
               decided_at = now()
         WHERE application_id = ${applicationId}
    `);
}

// The decision state of an existing row, used to tell a settled duplicate from a
// PENDING one that still needs scoring.
function currentDecision(string applicationId) returns string?|error {
    postgresql:Client c = check db();
    // queryRow raises sql:NoRowsError rather than returning nil when nothing
    // matches, so "no such application" has to be distinguished from a real fault.
    record {|string? decision;|}|sql:Error row = c->queryRow(`
        SELECT decision FROM loan_applications WHERE application_id = ${applicationId}
    `);
    if row is sql:NoRowsError {
        return ();
    }
    if row is sql:Error {
        return row;
    }
    return row.decision;
}

// One application with everything §7.1 asks for: score, decision, disbursement and
// arrears bucket. The disbursement and arrears rows are written by other components
// (phases 4 and 5), so both are absent until those exist.
function fetchApplication(string applicationId) returns ApplicationRecord?|error {
    postgresql:Client c = check db();

    record {|
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
        string? disbursement_id;
        decimal? disbursed_amount;
        string? disbursed_msisdn;
        string? provider_ref;
        string? disbursement_status;
        string? disbursed_at;
        string? bucket;
        int? days_past_due;
    |}|sql:Error row = c->queryRow(`
        SELECT l.application_id, l.applicant_name, l.national_id, l.wallet_msisdn,
               l.amount_kes, l.term_months, l.monthly_income, l.employment_months,
               l.kyc_verified, l.existing_defaults, l.score, l.decision, l.decision_reason,
               l.submitted_at::text AS submitted_at,
               l.decided_at::text   AS decided_at,
               d.disbursement_id,
               d.amount_kes         AS disbursed_amount,
               d.wallet_msisdn      AS disbursed_msisdn,
               d.provider_ref,
               d.status             AS disbursement_status,
               d.disbursed_at::text AS disbursed_at,
               a.bucket,
               a.days_past_due
          FROM loan_applications l
          LEFT JOIN disbursements d ON d.application_id = l.application_id
          LEFT JOIN arrears_classification a ON a.application_id = l.application_id
         WHERE l.application_id = ${applicationId}
    `);

    // No such application is a 404, not a 500 — sql:NoRowsError has to be told
    // apart from a genuine database fault.
    if row is sql:NoRowsError {
        return ();
    }
    if row is sql:Error {
        return row;
    }

    Disbursement? disbursement = ();
    string? disbursementId = row.disbursement_id;
    if disbursementId is string {
        disbursement = {
            disbursement_id: disbursementId,
            amount_kes: row.disbursed_amount ?: 0d,
            wallet_msisdn: row.disbursed_msisdn ?: "",
            provider_ref: row.provider_ref,
            status: row.disbursement_status ?: "",
            disbursed_at: row.disbursed_at ?: ""
        };
    }

    return {
        application_id: row.application_id,
        applicant_name: row.applicant_name,
        national_id: row.national_id,
        wallet_msisdn: row.wallet_msisdn,
        amount_kes: row.amount_kes,
        term_months: row.term_months,
        monthly_income: row.monthly_income,
        employment_months: row.employment_months,
        kyc_verified: row.kyc_verified,
        existing_defaults: row.existing_defaults,
        score: row.score,
        decision: row.decision,
        decision_reason: row.decision_reason,
        submitted_at: row.submitted_at,
        decided_at: row.decided_at,
        disbursement: disbursement,
        arrears_bucket: row.bucket,
        days_past_due: row.days_past_due,
        repayments: check fetchRepayments(applicationId)
    };
}

function fetchRepayments(string applicationId) returns Instalment[]|error {
    postgresql:Client c = check db();
    stream<Instalment, sql:Error?> rs = c->query(`
        SELECT instalment_no,
               due_date::text  AS due_date,
               amount_due_kes,
               paid_date::text AS paid_date
          FROM repayments
         WHERE application_id = ${applicationId}
         ORDER BY instalment_no
    `);
    return from Instalment i in rs
        select i;
}

// Newest first, optionally filtered by arrears bucket (§7.1).
function listApplications(int 'limit, string? bucket) returns ApplicationSummary[]|error {
    postgresql:Client c = check db();
    sql:ParameterizedQuery query = bucket is string
        ? `SELECT l.application_id, l.applicant_name, l.amount_kes, l.term_months,
                  l.score, l.decision, l.submitted_at::text AS submitted_at, a.bucket AS arrears_bucket
             FROM loan_applications l
             JOIN arrears_classification a ON a.application_id = l.application_id
            WHERE a.bucket = ${bucket}
            ORDER BY l.submitted_at DESC
            LIMIT ${'limit}`
        : `SELECT l.application_id, l.applicant_name, l.amount_kes, l.term_months,
                  l.score, l.decision, l.submitted_at::text AS submitted_at, a.bucket AS arrears_bucket
             FROM loan_applications l
             LEFT JOIN arrears_classification a ON a.application_id = l.application_id
            ORDER BY l.submitted_at DESC
            LIMIT ${'limit}`;

    stream<ApplicationSummary, sql:Error?> rs = c->query(query);
    return from ApplicationSummary s in rs
        select s;
}
