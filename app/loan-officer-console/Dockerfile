# loan-officer-console — three-stage build (BUILD-SPEC.md §11).
#
# 1. node   builds the React bundle
# 2. golang builds the static file server
# 3. distroless ships both, non-root on a read-only root filesystem
#
# §7.5 allows nginx-unprivileged or a small Go static file server. Go, because
# stock nginx wants to write pid files and temp paths, which a read-only
# filesystem forbids — and §7.5 says that rules it out "without work".

FROM node:22-alpine AS web

WORKDIR /src
COPY package.json package-lock.json ./
RUN npm ci

COPY tsconfig*.json vite.config.ts vitest.config.ts index.html ./
COPY src ./src
# The API base URL is deliberately NOT provided here. It is read at runtime from
# /config.js, so this bundle is identical across all three environments (§7.5).
RUN npm run build

# ---------------------------------------------------------------------------
FROM golang:1.23-alpine AS server

WORKDIR /src
COPY go.mod ./
COPY server.go ./
ENV CGO_ENABLED=0
RUN go build -trimpath -ldflags="-s -w" -o /out/console-server .

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot AS runtime

COPY --from=server --chown=10001:10001 /out/console-server /app/console-server
COPY --from=web    --chown=10001:10001 /src/dist /app/dist

# Numeric UID, not a name — Kubernetes runAsNonRoot cannot verify a username (§11).
USER 10001:10001

ENV PORT=8080 \
    STATIC_ROOT=/app/dist \
    LOG_LEVEL=info
# API_BASE_URL intentionally unset: environment-specific, injected at runtime.

EXPOSE 8080

HEALTHCHECK --interval=5s --timeout=3s --start-period=5s --retries=5 \
    CMD ["/app/console-server", "-healthcheck"]

ENTRYPOINT ["/app/console-server"]
