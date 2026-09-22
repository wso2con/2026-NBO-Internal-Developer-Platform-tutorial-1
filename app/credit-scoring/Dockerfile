# credit-scoring — multi-stage build (BUILD-SPEC.md §11).
#
# Stage 1 compiles with the full Ballerina distribution; stage 2 ships only the
# resulting JAR on a slim JRE. The full Ballerina distribution is ~1GB and must not
# reach the runtime image.
#
# The runtime image runs as numeric UID 10001 with a read-only root filesystem and
# all capabilities dropped. Verify with `make verify-hardened`, which is a direct
# rehearsal of the Act 4 live edit.

# ---------------------------------------------------------------------------
# Stage 1 — build
# ---------------------------------------------------------------------------
FROM ballerina/ballerina:2201.13.6 AS builder

# The image runs as ballerina (uid 100, gid 1000 "troupe"). WORKDIR creates a missing
# directory owned by root, and bal cannot then write target/ into it. So create the
# directory as root, hand it to the build user, and let WORKDIR step into a directory
# that already exists with the right ownership. Root is confined to this stage; the
# runtime stage below never uses it.
USER root
RUN mkdir -p /build && chown ballerina:troupe /build
USER ballerina

WORKDIR /build

# LOG_LEVEL is required at startup (§7.2). `bal build` initialises the module, so it
# needs a valid value here even though nothing is served during the build.
ENV LOG_LEVEL=info

COPY --chown=100:1000 Ballerina.toml ./
COPY --chown=100:1000 *.bal ./
COPY --chown=100:1000 tests ./tests

# `bal build` compiles without running tests; those run in `make test` / CI.
RUN bal build

# ---------------------------------------------------------------------------
# Stage 2 — runtime
# ---------------------------------------------------------------------------
# Temurin is multi-arch (§9.1). The -alpine JRE keeps this near the low end of the
# 200-300MB §11 expects for a Ballerina service.
FROM eclipse-temurin:21-jre-alpine AS runtime

# Ownership is set at COPY time. There is deliberately no chown at runtime (§11):
# the filesystem is read-only, so it would fail anyway.
COPY --from=builder --chown=10001:10001 \
     /build/target/bin/credit_scoring.jar /app/credit_scoring.jar

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

# §7.2 service-specific default; §2.4's general 8080 does not apply to this service.
ENV PORT=8081 \
    LOG_LEVEL=info \
    # ballerina/log emits JSON when this is set, which is how §2.5's structured
    # stdout logging is achieved without a Config.toml (§3.2 forbids one).
    BAL_CONFIG_VAR_BALLERINA_LOG_FORMAT=json \
    # Under a read-only root filesystem the JVM still needs a writable temp dir,
    # so point it at the tmpfs mount. UseContainerSupport plus MaxRAMPercentage
    # make the heap size itself to the container limit rather than the node (§11).
    JAVA_TOOL_OPTIONS="-Djava.io.tmpdir=/tmp -XX:+UseContainerSupport -XX:MaxRAMPercentage=75.0"

EXPOSE 8081

# For compose. Kubernetes uses the /healthz and /readyz probes instead (§7.2).
HEALTHCHECK --interval=5s --timeout=3s --start-period=20s --retries=5 \
    CMD wget -q -O- "http://127.0.0.1:${PORT}/healthz" >/dev/null 2>&1 || exit 1

# exec form, so the JVM is PID 1 and receives SIGTERM directly. The listener drains
# for 10 seconds before stopping (§2.8).
ENTRYPOINT ["java", "-jar", "/app/credit_scoring.jar"]
