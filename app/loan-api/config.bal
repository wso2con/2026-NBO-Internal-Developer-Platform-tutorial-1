// config.bal — environment configuration (BUILD-SPEC.md §3.2).
//
// Env vars only. No Config.toml, no `configurable` variables: one immutable image
// is promoted through three environments, so configuration cannot live in the image.
//
// Everything is read, parsed and range-checked ONCE in init(), before the listener
// starts. A required variable that is missing or empty logs a line naming it and
// exits non-zero.

import ballerina/os;

public type DbConfig record {|
    string host;
    int port;
    string user;
    string password;
    string database;
|};

public type ServiceConfig record {|
    int port;
    DbConfig db;
    string natsUrl;
    string creditScoringUrl;
    decimal scoringTimeout;
    string logLevel;
|};

// §2.4's general convention. credit-scoring overrides to 8081; loan-api does not.
const int DEFAULT_PORT = 8080;
const int MIN_PORT = 1;
const int MAX_PORT = 65535;

// §3.2 names this default explicitly.
const int DEFAULT_SCORING_TIMEOUT_MS = 2000;

final readonly & string[] VALID_LOG_LEVELS = ["debug", "info", "warn", "error"];

isolated function requireEnv(string name) returns string|error {
    string value = os:getEnv(name).trim();
    if value == "" {
        return error(string `${name} is required but was not set`);
    }
    return value;
}

// Parses postgres://user:password@host:port/database?params into the parts the
// ballerinax/postgresql client needs. The platform injects DATABASE_URL as a single
// string (§7.1), so the service has to take it apart rather than ask for five vars.
public isolated function parseDatabaseUrl(string url) returns DbConfig|error {
    string rest;
    if url.startsWith("postgres://") {
        rest = url.substring(11);
    } else if url.startsWith("postgresql://") {
        rest = url.substring(13);
    } else {
        return error("DATABASE_URL must start with postgres:// or postgresql://");
    }

    int? atIndex = rest.lastIndexOf("@");
    if atIndex is () {
        return error("DATABASE_URL must contain credentials, as user:password@host");
    }
    string credentials = rest.substring(0, atIndex);
    string hostAndPath = rest.substring(atIndex + 1);

    int? colonIndex = credentials.indexOf(":");
    if colonIndex is () {
        return error("DATABASE_URL credentials must be user:password");
    }
    string user = credentials.substring(0, colonIndex);
    string password = credentials.substring(colonIndex + 1);

    // strip any ?sslmode=... query string
    int? queryIndex = hostAndPath.indexOf("?");
    string hostPortDb = queryIndex is int ? hostAndPath.substring(0, queryIndex) : hostAndPath;

    int? slashIndex = hostPortDb.indexOf("/");
    if slashIndex is () {
        return error("DATABASE_URL must include a database name");
    }
    string hostPort = hostPortDb.substring(0, slashIndex);
    string database = hostPortDb.substring(slashIndex + 1);
    if database == "" {
        return error("DATABASE_URL must include a database name");
    }

    string host = hostPort;
    int dbPort = 5432;
    int? portColon = hostPort.lastIndexOf(":");
    if portColon is int {
        host = hostPort.substring(0, portColon);
        int|error parsed = int:fromString(hostPort.substring(portColon + 1));
        if parsed is error {
            return error("DATABASE_URL port must be an integer");
        }
        dbPort = parsed;
    }
    if host == "" {
        return error("DATABASE_URL must include a host");
    }

    return {host, port: dbPort, user, password, database};
}

public isolated function loadConfig() returns ServiceConfig|error {

    // PORT — optional, defaults to 8080 (§2.4)
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

    // Injected by the platform in Phase 2 of the project; by compose locally (§7.1).
    string databaseUrl = check requireEnv("DATABASE_URL");
    DbConfig|error db = parseDatabaseUrl(databaseUrl);
    if db is error {
        return error(string `DATABASE_URL is invalid: ${db.message()}`);
    }

    string natsUrl = check requireEnv("NATS_URL");
    string creditScoringUrl = check requireEnv("CREDIT_SCORING_URL");

    // SCORING_TIMEOUT_MS — optional, 2000 by default (§3.2)
    int timeoutMs = DEFAULT_SCORING_TIMEOUT_MS;
    string rawTimeout = os:getEnv("SCORING_TIMEOUT_MS").trim();
    if rawTimeout != "" {
        int|error parsed = int:fromString(rawTimeout);
        if parsed is error {
            return error(string `SCORING_TIMEOUT_MS must be an integer, got '${rawTimeout}'`);
        }
        if parsed < 1 {
            return error(string `SCORING_TIMEOUT_MS must be greater than 0, got ${parsed}`);
        }
        timeoutMs = parsed;
    }

    string logLevel = (check requireEnv("LOG_LEVEL")).toLowerAscii();
    if VALID_LOG_LEVELS.indexOf(logLevel) is () {
        return error(string `LOG_LEVEL must be one of ${string:'join("|", ...VALID_LOG_LEVELS)}`
            + string `, got '${logLevel}'`);
    }

    return {
        port,
        db,
        natsUrl,
        creditScoringUrl,
        // ballerina/http expresses timeouts in seconds
        scoringTimeout: <decimal>timeoutMs / 1000d,
        logLevel
    };
}
