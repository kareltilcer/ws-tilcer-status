# syntax=docker/dockerfile:1
#
# API-only image for the `status` backend: the Go API, with Litestream streaming
# the SQLite database to Cloudflare R2. The SPA ships as its own Nginx image (see
# frontend/Dockerfile); this backend serves no static assets.
#
# The build context is the REPOSITORY ROOT (not backend/), because the image
# needs backend/, litestream.yml, and docker-entrypoint.sh. In Coolify set:
#   Build Pack          = Dockerfile
#   Base Directory      = /            (context root)
#   Dockerfile Location = /backend/Dockerfile
# The backend listens on :112 (STATUS_ADDR) — point Coolify's port at 112.
# Do NOT enable Strip Prefix: routes are served under /api or they 404.

# ── Stage 1: build the Go binary ───────────────────────────────────────
FROM golang:1.26-alpine AS backend
RUN apk add --no-cache git
WORKDIR /src
ENV CGO_ENABLED=0 GOOS=linux GOFLAGS="-mod=readonly"
COPY backend/go.mod backend/go.sum ./
RUN go mod download
COPY backend/ ./
# Pure-Go SQLite (modernc, CGO off).
RUN go build -trimpath -ldflags="-s -w" -o /out/status ./cmd/status

# ── Stage 2: pull the Litestream binary ────────────────────────────────
FROM litestream/litestream:0.3.13 AS litestream

# ── Stage 3: runtime ───────────────────────────────────────────────────
FROM alpine:3.20
RUN apk add --no-cache ca-certificates && update-ca-certificates
COPY --from=litestream /usr/local/bin/litestream /usr/local/bin/litestream
COPY --from=backend    /out/status               /usr/local/bin/status
COPY litestream.yml       /etc/litestream.yml
COPY docker-entrypoint.sh /usr/local/bin/entrypoint
RUN chmod +x /usr/local/bin/entrypoint

# Defaults; secrets and per-service config come from Coolify env at runtime.
# API-only: STATUS_STATIC_DIR is intentionally unset (the SPA is the frontend image).
ENV STATUS_ADDR=:112 \
    STATUS_DB_PATH=/data/status.db
EXPOSE 112
VOLUME ["/data"]
ENTRYPOINT ["/usr/local/bin/entrypoint"]
