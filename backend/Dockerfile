# ── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26.8-alpine3.23 AS builder

WORKDIR /build

# hadolint ignore=DL3018
RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o step-ui .

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.23

WORKDIR /opt/step-ui

# Create a non-root user for the runtime process.
# uid/gid 10001 is in the safe non-system range per container best-practice.
RUN addgroup -g 10001 -S stepui && \
    adduser -u 10001 -S -G stepui stepui

# openssl stays here solely for admin_console.go's "openssl.version"
# diagnostic allowlist entry — TLS bootstrap no longer needs it (main.go
# does root fetch/leaf issuance/self-signed generation via crypto/x509).
# hadolint ignore=DL3018
RUN apk add --no-cache curl openssl ca-certificates netcat-openbsd tzdata postgresql-client

COPY --from=builder /build/step-ui .
COPY templates/ templates/
COPY static/ static/

RUN mkdir -p data certs uploads ssl && \
    chown -R stepui:stepui /opt/step-ui

COPY entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh && \
    chmod +x /entrypoint.sh && \
    chown stepui:stepui /entrypoint.sh

EXPOSE 8443

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
    CMD curl -fsk https://localhost:8443/health || exit 1

USER stepui

ENTRYPOINT ["/entrypoint.sh"]
