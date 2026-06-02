# ── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26.3-alpine3.23 AS builder

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

# hadolint ignore=DL3018,DL4006
RUN apk add --no-cache curl openssl ca-certificates netcat-openbsd tzdata postgresql-client && \
    wget -q "https://github.com/smallstep/cli/releases/download/v0.30.2/step_linux_0.30.2_amd64.tar.gz" \
         -O /tmp/step.tar.gz && \
    echo "abf5ffe9a39c01c7b4e777d50a96f30f33f5bd8eeafa2dfeef22b8a4abeee14a  /tmp/step.tar.gz" | sha256sum -c - && \
    tar -xzf /tmp/step.tar.gz -C /tmp && \
    mv /tmp/step_0.30.2/bin/step /usr/local/bin/step && \
    rm -rf /tmp/step* && \
    chmod +x /usr/local/bin/step

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
