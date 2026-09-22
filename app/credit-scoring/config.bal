// config.bal — environment configuration (BUILD-SPEC.md §3.2).
//
// Env vars only. There is deliberately no Config.toml in this package and no
// `configurable` variable anywhere: one immutable image is promoted through three
// environments, so configuration cannot live inside the image.
//
// Everything is read, parsed and range-checked ONCE at startup, in init(), which
// runs before the listener is started. A required variable that is missing or empty
// logs one line naming that variable and exits non-zero -- loudly at startup, never
// silently at the first request.

import ballerina/os;

// Resolved configuration, after validation.
public type ServiceConfig record {|
    int port;
    string logLevel;
|};

// §7.2 gives credit-scoring a service-specific default of 8081, which overrides the
// general 8080 convention in §2.4. It is also the host port reserved in §9.2.
const int DEFAULT_PORT = 8081;

const int MIN_PORT = 1;
const int MAX_PORT = 65535;

final readonly & string[] VALID_LOG_LEVELS = ["debug", "info", "warn", "error"];

// Lenient read used only to construct the listener object at module level. The
// authoritative check is loadConfig(), called from init() before the listener starts,
// so a bad PORT still fails startup -- it just fails with a clear message instead of
// a constructor error.
isolated function portOrDefault() returns int {
    int|error parsed = int:fromString(os:getEnv("PORT").trim());
    if parsed is int && parsed >= MIN_PORT && parsed <= MAX_PORT {
        return parsed;
    }
    return DEFAULT_PORT;
}

// Read and validate every setting. Returns an error naming the offending variable.
public isolated function loadConfig() returns ServiceConfig|error {

    // PORT -- optional, defaults to 8081 (§7.2). Parsed and range-checked here,
    // not at the point of use (§3.2).
    int port = DEFAULT_PORT;
    string rawPort = os:getEnv("PORT").trim();
    if rawPort != "" {
        int|error parsed = int:fromString(rawPort);
        if parsed is error {
            return error(string `PORT must be an integer, got '${rawPort}'`);
        }
        if parsed < MIN_PORT || parsed > MAX_PORT {
            return error(string `PORT must be between ${MIN_PORT} and ${MAX_PORT}, got ${parsed}`);
        }
        port = parsed;
    }

    // LOG_LEVEL -- required. §7.2 lists it with no default, unlike PORT, so an
    // unset LOG_LEVEL is a startup failure rather than a silent fallback.
    string logLevel = os:getEnv("LOG_LEVEL").trim().toLowerAscii();
    if logLevel == "" {
        return error("LOG_LEVEL is required but was not set");
    }
    if VALID_LOG_LEVELS.indexOf(logLevel) is () {
        return error(string `LOG_LEVEL must be one of ${string:'join("|", ...VALID_LOG_LEVELS)}`
            + string `, got '${logLevel}'`);
    }

    return {port, logLevel};
}
