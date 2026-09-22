# disbursement-worker — multi-stage build (BUILD-SPEC.md §11).
#
# Go service: distroless final stage, well under the 40MB §11 allows.
#
# Builder is golang:1.25 rather than 1.23 because a transitive dependency
# requires it; §3.3 asks for "Go 1.23+", so this satisfies it.

FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/disbursement-worker .

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder --chown=10001:10001 /out/disbursement-worker /app/disbursement-worker

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

# §7.3 defaults. The five injected variables (NATS_URL, VALKEY_URL, DATABASE_URL,
# PAYMENT_RAIL_URL, PAYMENT_RAIL_API_KEY) are deliberately NOT set here: they are
# required, environment-specific, and one is a secret (§2.7, §3.2).
ENV ADMIN_PORT=9090 \
    IDEMPOTENCY_TTL_HOURS=168 \
    LOG_LEVEL=info

# The admin port only. This component declares NO service endpoint (§7.3) — it is
# a consumer, and 9090 exists so the platform can probe and scrape it.
EXPOSE 9090

# distroless has no shell or wget, so the binary probes itself.
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=5 \
    CMD ["/app/disbursement-worker", "-healthcheck"]

ENTRYPOINT ["/app/disbursement-worker"]
