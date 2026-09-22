// service.bal — loan-api, the front door (BUILD-SPEC.md §7.1).
//
// This is the one component built from source live on stage, and it goes on a
// projector at T+0:35 where the presenter reads what is ABSENT from it: no
// hostnames, no ports, no credentials, no retry framework, no ORM. The address of
// credit-scoring, the database and the broker all arrive as environment variables.
//
// Deliberately four files, not one: this service definition, plus scoring_client,
// db and events. Anything more and it stops fitting on a screen.

import ballerina/http;
import ballerina/log;
import ballerina/random;

// Constructed at module level, started only after init() succeeds, so bad
// configuration fails before a port is bound. 10-second drain on SIGTERM (§2.8).
//
// The port is the CONSTANT DEFAULT_PORT, not $PORT. The Ballerina buildpack
// generates an OpenAPI definition at build time and needs a compile-time port:
// a function call gives
//   ERROR [service.bal] Unsupported expression found for the server port value
//   ERROR Generated OpenAPI definition does not have the server information
// `bal build` locally does not generate that spec, so this only fails in the
// platform build. Deviation from §3.2 (all config from env) agreed 2026-09-17:
// the container port is part of the image contract, not runtime configuration --
// compose maps 8090:8080 and the OpenChoreo Workload declares 8080 either way.
listener http:Listener apiListener = new (DEFAULT_PORT, gracefulStopTimeout = 10);

function init() returns error? {
    ServiceConfig cfg = check loadConfig();

    check initDatabase(cfg.db);
    check initEvents(cfg.natsUrl);
    check initScoringClient(cfg.creditScoringUrl, cfg.scoringTimeout);

    // Resolved config on one line so the presenter can prove which database and
    // which scoring endpoint this pod actually came up against. The password is
    // never logged (§3.2 — secrets redacted).
    log:printInfo("loan-api started",
            port = DEFAULT_PORT,
            database = string `${cfg.db.host}:${cfg.db.port}/${cfg.db.database}`,
            database_user = cfg.db.user,
            nats_url = cfg.natsUrl,
            credit_scoring_url = cfg.creditScoringUrl,
            scoring_timeout_seconds = cfg.scoringTimeout,
            log_level = cfg.logLevel);
}

// CORS is required by §7.5's design, not an addition to it: the console reads the
// API's address at runtime from /config.js and the browser then calls loan-api
// DIRECTLY, which is cross-origin whenever the two are served from different
// hosts or ports — as they are locally (console :3000, api :8080) and will be on
// OpenChoreo, where each component gets its own route.
//
// The origin list is "*" deliberately. This API carries no authentication, no
// cookies and no credentials (§14 puts auth out of scope), so there is no
// session for a hostile origin to ride. If auth is ever added, this must become
// an explicit origin list at the same time — a wildcard plus credentials is the
// combination that actually bites.
@http:ServiceConfig {
    cors: {
        allowOrigins: ["*"],
        allowMethods: ["GET", "POST", "OPTIONS"],
        allowHeaders: ["Content-Type", "Accept", "traceparent"],
        maxAge: 86400
    }
}
service / on apiListener {

    // Submit an application (§7.1).
    //
    // validate -> insert PENDING -> score -> persist -> publish if approved -> 201.
    resource function post applications(@http:Payload ApplicationRequest req,
            @http:Header {name: "traceparent"} string? traceparent)
            returns ApplicationCreated|ApplicationExisting|http:BadRequest
                    |http:ServiceUnavailable|http:InternalServerError {

        FieldError[] errors = validateApplication(req);
        if errors.length() > 0 {
            log:printWarn("application rejected",
                    application_id = req.application_id ?: "(none)",
                    error_count = errors.length());
            return <http:BadRequest>{
                body: <ValidationErrors>{message: "validation failed", errors: errors}
            };
        }

        string applicationId = req.application_id ?: generateApplicationId();

        boolean inserted;
        do {
            inserted = check insertPending(applicationId, req);
        } on fail error e {
            log:printError("insert failed", application_id = applicationId, 'error = e);
            return <http:InternalServerError>{body: {message: "could not persist application"}};
        }

        if !inserted {
            // The id already exists. §7.1: return 200 with the existing record, do
            // not re-score, do not re-publish.
            //
            // That rule applies to a DECIDED record. A PENDING one means a previous
            // attempt got a 503 from scoring and never reached a decision; returning
            // 200 for it forever would strand the loan with no path to APPROVED or
            // DECLINED. So PENDING falls through and is scored on this attempt.
            // Raised with the spec author.
            string?|error existing = currentDecision(applicationId);
            if existing is error {
                log:printError("duplicate lookup failed",
                        application_id = applicationId, 'error = existing);
                return <http:InternalServerError>{body: {message: "could not read application"}};
            }
            if existing is string && existing != "PENDING" {
                log:printInfo("duplicate submission ignored",
                        application_id = applicationId, decision = existing);
                ApplicationRecord|error record_ = loadRecord(applicationId);
                if record_ is error {
                    return <http:InternalServerError>{body: {message: "could not read application"}};
                }
                return <ApplicationExisting>{body: record_};
            }
            log:printInfo("retrying a pending application",
                    application_id = applicationId);
        } else {
            ApplicationRecord|error submitted = loadRecord(applicationId);
            if submitted is ApplicationRecord {
                error? published = publishSubmitted(submitted, traceparent);
                if published is error {
                    // Non-fatal: loan.submitted is informational. loan.approved is
                    // the one the worker consumes, and that failure IS fatal below.
                    log:printWarn("could not publish loan.submitted",
                            application_id = applicationId, 'error = published);
                }
            }
        }

        // Score it. If credit-scoring is unreachable or times out we return 503 and
        // leave the record PENDING, publishing nothing.
        //
        // WE NEVER APPROVE ON A SCORING FAILURE. A banking audience will ask about
        // this, and the answer has to be that an unavailable risk engine produces no
        // decision at all rather than a permissive default.
        ScoreResponse|error scored = requestScore(req, applicationId, traceparent);
        if scored is error {
            log:printError("scoring unavailable, application left PENDING",
                    application_id = applicationId, 'error = scored);
            return <http:ServiceUnavailable>{
                body: {message: "scoring service unavailable, application left pending"}
            };
        }

        error? persisted = persistDecision(applicationId, scored.score, scored.decision,
                scored.reason);
        if persisted is error {
            log:printError("could not persist decision",
                    application_id = applicationId, 'error = persisted);
            return <http:InternalServerError>{body: {message: "could not persist decision"}};
        }

        ApplicationRecord|error decided = loadRecord(applicationId);
        if decided is error {
            return <http:InternalServerError>{body: {message: "could not read application"}};
        }

        log:printInfo("application decided",
                application_id = applicationId,
                score = scored.score,
                decision = scored.decision);

        if scored.decision == "APPROVED" {
            error? published = publishApproved(decided, traceparent);
            if published is error {
                log:printError("could not publish loan.approved",
                        application_id = applicationId, 'error = published);
                return <http:InternalServerError>{
                    body: {message: "decision persisted but could not be published"}
                };
            }
            log:printInfo("loan.approved published", application_id = applicationId);
        }

        return <ApplicationCreated>{body: decided};
    }

    // One application, with score, decision, disbursement and arrears bucket (§7.1).
    resource function get applications/[string id]()
            returns ApplicationRecord|http:NotFound|http:InternalServerError {
        ApplicationRecord?|error found = fetchApplication(id);
        if found is error {
            log:printError("fetch failed", application_id = id, 'error = found);
            return <http:InternalServerError>{body: {message: "could not read application"}};
        }
        if found is () {
            return <http:NotFound>{body: {message: string `no application ${id}`}};
        }

        // Attach the scoring breakdown for the console's detail screen (§7.5).
        // Deliberately best-effort: a scoring outage must not make an already
        // decided application unreadable.
        ApplicationRecord app = found;
        if app.score is int {
            Factor[]|error factors = scoreFactorsFor(app);
            if factors is Factor[] {
                app.score_factors = factors;
            } else {
                log:printWarn("could not recompute score factors",
                        application_id = id, 'error = factors);
            }
        }
        return app;
    }

    // Newest first, optional bucket filter (§7.1).
    resource function get applications(int 'limit = 50, string? bucket = ())
            returns ApplicationSummary[]|http:InternalServerError {
        ApplicationSummary[]|error rows = listApplications('limit, bucket);
        if rows is error {
            log:printError("list failed", 'error = rows);
            return <http:InternalServerError>{body: {message: "could not list applications"}};
        }
        return rows;
    }

    // Liveness: the process is up. No dependency checks, by design (§7.1).
    resource function get healthz() returns http:Ok {
        return {body: {status: "ok"}};
    }

    // Readiness: database and NATS reachable (§7.1).
    resource function get readyz() returns http:Ok|http:ServiceUnavailable {
        error? dbReady = pingDatabase();
        if dbReady is error {
            return <http:ServiceUnavailable>{body: {status: "not ready", dependency: "database"}};
        }
        error? natsReady = pingNats();
        if natsReady is error {
            return <http:ServiceUnavailable>{body: {status: "not ready", dependency: "nats"}};
        }
        return <http:Ok>{body: {status: "ready"}};
    }
}

// Reads back the row we just wrote, so the response is what is actually persisted
// rather than what we believe we persisted.
function loadRecord(string applicationId) returns ApplicationRecord|error {
    ApplicationRecord? found = check fetchApplication(applicationId);
    if found is () {
        return error(string `application ${applicationId} vanished after write`);
    }
    return found;
}

// §7.1: if application_id is omitted, generate APP- plus six digits.
function generateApplicationId() returns string {
    int|random:Error n = random:createIntInRange(100000, 1000000);
    int suffix = n is int ? n : 100000;
    return string `APP-${suffix}`;
}
