// service.bal — credit-scoring HTTP service (BUILD-SPEC.md §7.2).
//
// Internal-only. This service has no public route in production and never will:
// no auth, no UI, nothing that implies external access. On OpenChoreo the equivalent
// call from outside the cell must be refused -- that is the Act 2 beat at T+0:49.
//
// Stateless: no database, no outbound calls. Every request is answered from the
// pure rule engine in scoring.bal.
//
// Tracing: inbound `traceparent` is extracted by Ballerina's built-in OpenTelemetry
// instrumentation (enabled by observabilityIncluded in Ballerina.toml), so W3C trace
// context is honoured without any library of our own (§2.6, §3.2). This service makes
// no outbound HTTP calls, so there is nothing to forward onward.
//
// Metrics: Prometheus metrics come from the same built-in instrumentation and are
// served by the runtime on its own observability port (9797), not on the service port.

import ballerina/http;
import ballerina/log;

// Listener is constructed at module level but only STARTED after init() returns
// successfully, so invalid configuration fails startup before any port is bound.
// gracefulStopTimeout gives in-flight requests 10 seconds to drain on SIGTERM (§2.8).
listener http:Listener scoringListener = new (portOrDefault(), gracefulStopTimeout = 10);

// Validates configuration once, before the listener starts (§3.2). Returning an error
// here terminates the process with a non-zero exit code and a message naming the
// offending variable. The module init() function cannot be public.
function init() returns error? {
    ServiceConfig cfg = check loadConfig();
    // The resolved config on one line, so the presenter can prove on stage which
    // settings the pod actually came up with. This service holds no secrets, so
    // there is nothing to redact.
    log:printInfo("credit-scoring started",
            port = cfg.port,
            log_level = cfg.logLevel,
            approval_threshold = APPROVAL_THRESHOLD);
}

service / on scoringListener {

    // Score one application. Deterministic -- the same body always scores the same.
    resource function post score(@http:Payload ScoreRequest req)
            returns ScoreOk|http:BadRequest {

        FieldError[] errors = validate(req);
        if errors.length() > 0 {
            log:printWarn("scoring request rejected",
                    application_id = req.application_id,
                    error_count = errors.length());
            ValidationErrors body = {message: "validation failed", errors: errors};
            return <http:BadRequest>{body: body};
        }

        ScoreResponse result = score(req);
        log:printInfo("application scored",
                application_id = result.application_id,
                score = result.score,
                decision = result.decision);
        return <ScoreOk>{body: result};
    }

    // The thresholds the engine is currently using, straight from the same constants
    // it scores with -- so "the rules are inspectable" is literally true (§7.2).
    resource function get rules() returns Rules {
        return currentRules();
    }

    // Liveness: the process is up. No dependency checks, by design (§7.1's rule,
    // applied here too) -- this service has no dependencies to check.
    resource function get healthz() returns http:Ok {
        return {body: {status: "ok"}};
    }

    // Readiness: configuration validated and the engine can serve. Reached only after
    // init() succeeded, so arriving here at all means the service is ready.
    resource function get readyz() returns http:Ok {
        return {body: {status: "ready"}};
    }
}

// Guards against inputs that would make the arithmetic meaningless -- chiefly a zero
// income, which would divide by zero computing DTI. Full field-level validation of a
// loan application is loan-api's job (§7.1); this is only what the engine needs to be
// safe, expressed in the same error shape.
isolated function validate(ScoreRequest req) returns FieldError[] {
    FieldError[] errors = [];
    if req.application_id.trim() == "" {
        errors.push({'field: "application_id", message: "must not be empty"});
    }
    if req.monthly_income <= 0d {
        errors.push({'field: "monthly_income", message: "must be greater than 0"});
    }
    if req.amount_kes <= 0d {
        errors.push({'field: "amount_kes", message: "must be greater than 0"});
    }
    if req.term_months <= 0 {
        errors.push({'field: "term_months", message: "must be greater than 0"});
    }
    if req.employment_months < 0 {
        errors.push({'field: "employment_months", message: "must not be negative"});
    }
    if req.existing_defaults < 0 {
        errors.push({'field: "existing_defaults", message: "must not be negative"});
    }
    return errors;
}
