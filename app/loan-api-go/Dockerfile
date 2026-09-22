# loan-api-go — multi-stage build (BUILD-SPEC.md §11).
#
# The reason this component exists: a Dockerfile build finishes in well under a
# minute, where the Ballerina buildpack takes ~10 on a cold cache. Same API, same
# database, same events.

FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/loan-api-go .

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder --chown=10001:10001 /out/loan-api-go /app/loan-api-go

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

# The three injected variables (DATABASE_URL, NATS_URL, CREDIT_SCORING_URL) are
# deliberately NOT set here: they are required and environment-specific (§2.7, §3.2).
ENV PORT=8080 \
    SCORING_TIMEOUT_MS=2000 \
    LOG_LEVEL=info

EXPOSE 8080

# distroless has no shell or wget, so the binary probes itself.
HEALTHCHECK --interval=5s --timeout=3s --start-period=10s --retries=5 \
    CMD ["/app/loan-api-go", "-healthcheck"]

ENTRYPOINT ["/app/loan-api-go"]
