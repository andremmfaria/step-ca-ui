# ── Stage 1: Build ───────────────────────────────────────────────────────────
FROM golang:1.26.3-alpine3.23 AS builder

WORKDIR /build

RUN apk add --no-cache git ca-certificates

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-w -s" -o step-ui .

# ── Stage 2: Runtime ──────────────────────────────────────────────────────────
FROM alpine:3.23

WORKDIR /opt/step-ui

RUN apk add --no-cache curl openssl ca-certificates netcat-openbsd tzdata postgresql-client && \
    wget -q "https://github.com/smallstep/cli/releases/download/v0.30.2/step_linux_0.30.2_amd64.tar.gz" \
         -O /tmp/step.tar.gz && \
    tar -xzf /tmp/step.tar.gz -C /tmp && \
    mv /tmp/step_0.30.2/bin/step /usr/local/bin/step && \
    rm -rf /tmp/step* && \
    chmod +x /usr/local/bin/step

COPY --from=builder /build/step-ui .
COPY templates/ templates/
COPY static/ static/

RUN mkdir -p data certs uploads ssl

COPY entrypoint.sh /entrypoint.sh
RUN sed -i 's/\r$//' /entrypoint.sh && chmod +x /entrypoint.sh

EXPOSE 8443

ENTRYPOINT ["/entrypoint.sh"]
