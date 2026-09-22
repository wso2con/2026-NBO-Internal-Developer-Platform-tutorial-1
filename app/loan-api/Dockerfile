# loan-api — multi-stage build (BUILD-SPEC.md §11).
#
# This is the image rebuilt from source live on stage, so the build has to be
# reliable and reasonably quick. Stage 1 compiles with the full Ballerina
# distribution; stage 2 ships only the JAR on a slim JRE.

# ---------------------------------------------------------------------------
# Stage 1 — build
# ---------------------------------------------------------------------------
FROM ballerina/ballerina:2201.13.6 AS builder

# The image runs as ballerina (uid 100, gid 1000 "troupe"). WORKDIR creates a
# missing directory owned by root, and bal cannot then write target/ into it — so
# create it as root, hand it over, and let WORKDIR step into an existing directory.
# Root is confined to this stage.
USER root
RUN mkdir -p /build && chown ballerina:troupe /build
USER ballerina

WORKDIR /build

# `bal build` initialises the module, and init() requires these (§7.1). Build-time
# placeholders only — the real values are injected at runtime by compose or the
# platform, and nothing here is baked into the image.
ENV LOG_LEVEL=info \
    DATABASE_URL=postgres://build:build@localhost:5432/build \
    NATS_URL=nats://localhost:4222 \
    CREDIT_SCORING_URL=http://localhost:8081

COPY --chown=100:1000 Ballerina.toml ./
COPY --chown=100:1000 *.bal ./
COPY --chown=100:1000 tests ./tests

RUN bal build

# ---------------------------------------------------------------------------
# Stage 2 — runtime
# ---------------------------------------------------------------------------
FROM eclipse-temurin:21-jre-alpine AS runtime

# Ownership set at COPY time; no chown at runtime (§11) — the filesystem is
# read-only, so it would fail anyway.
COPY --from=builder --chown=10001:10001 /build/target/bin/loan_api.jar /app/loan_api.jar

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

# §2.4's general convention; only credit-scoring overrides it (to 8081).
# DATABASE_URL, NATS_URL and CREDIT_SCORING_URL are deliberately NOT set here:
# they are required, environment-specific, and the service must refuse to start
# without them (§3.2). No secrets in the image (§2.7).
ENV PORT=8080 \
    LOG_LEVEL=info \
    SCORING_TIMEOUT_MS=2000 \
    # ballerina/log emits JSON when this is set, which is how §2.5's structured
    # stdout logging is achieved without a Config.toml (§3.2 forbids one).
    BAL_CONFIG_VAR_BALLERINA_LOG_FORMAT=json \
    # Under a read-only root filesystem the JVM still needs a writable temp dir,
    # so point it at the tmpfs mount. UseContainerSupport plus MaxRAMPercentage
    # size the heap to the container limit rather than the node (§11).
    JAVA_TOOL_OPTIONS="-Djava.io.tmpdir=/tmp -XX:+UseContainerSupport -XX:MaxRAMPercentage=75.0"

EXPOSE 8080

# For compose. Kubernetes uses /healthz and /readyz instead (§7.1). Liveness, not
# readiness — a database blip should not make the container unhealthy.
HEALTHCHECK --interval=5s --timeout=3s --start-period=30s --retries=5 \
    CMD wget -q -O- "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 || exit 1

# exec form, so the JVM is PID 1 and receives SIGTERM directly; the listener then
# drains for 10 seconds before stopping (§2.8).
ENTRYPOINT ["java", "-jar", "/app/loan_api.jar"]
