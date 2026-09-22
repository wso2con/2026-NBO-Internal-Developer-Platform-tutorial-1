# payment-rail-stub — multi-stage build (BUILD-SPEC.md §11).
#
# Go service: distroless final stage, comfortably under the 40MB §11 allows.
# No shell in the runtime image, so the HEALTHCHECK uses the binary's own
# -healthcheck flag rather than wget.

FROM golang:1.23-alpine AS builder

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY *.go ./

# CGO off and a stripped binary, so it runs on distroless static.
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/payment-rail-stub .

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder --chown=10001:10001 /out/payment-rail-stub /app/payment-rail-stub

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

# §2.4 general convention. Compose maps host 8082 to this (§9.2).
ENV PORT=8080 \
    LOG_LEVEL=info \
    FAIL_RATE=0 \
    LATENCY_MS=150

EXPOSE 8080

# distroless has no shell or wget, so the binary probes itself.
HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
    CMD ["/app/payment-rail-stub", "-healthcheck"]

ENTRYPOINT ["/app/payment-rail-stub"]
