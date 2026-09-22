# arrears-eod — multi-stage build (BUILD-SPEC.md §11).
#
# A batch job, not a server: it runs to completion and exits. There is therefore
# no HEALTHCHECK and no EXPOSE — a CronJob has nothing to probe.
#
# Builder is golang:1.25 because a transitive dependency requires it; §3.3 asks
# for "Go 1.23+", so this satisfies it.

FROM golang:1.25-alpine AS builder

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY *.go ./

ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/arrears-eod .

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=builder --chown=10001:10001 /out/arrears-eod /app/arrears-eod

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

# DATABASE_URL is required and environment-specific, so it is not set here (§3.2).
# AS_OF_DATE is deliberately unset: it defaults to today, and the smoke test
# relies on that (§10.6).
ENV LOG_LEVEL=info \
    DRY_RUN=false

ENTRYPOINT ["/app/arrears-eod"]
