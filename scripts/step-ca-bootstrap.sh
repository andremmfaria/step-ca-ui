#!/bin/sh
set -eu

CA_CONFIG="${STEPPATH:-/home/step}/config/ca.json"
PROVISIONER_NAME="${DOCKER_STEPCA_INIT_PROVISIONER_NAME:-${PROVISIONER:-admin}}"
DEFAULT_TLS_DURATION="${STEPCA_DEFAULT_TLS_CERT_DURATION:-8760h}"
MAX_TLS_DURATION="${STEPCA_MAX_TLS_CERT_DURATION:-87600h}"
CERTS_DIR="${STEPPATH:-/home/step}/certs"

# The base image leaves $CERTS_DIR at 0750 (drwx--S---), owned by the "step"
# user, with the group bit missing +x — so no other UID can even traverse
# into it, let alone read root_ca.crt/intermediate_ca.crt. step-ui (and any
# other container sharing the step-ca-data volume) reads those files as its
# own non-root UID, which is a different UID than step-ca's — the standard
# docker-compose deployment shape this project documents ("root cert is
# assumed already present via volume mount"). root_ca.crt/intermediate_ca.crt
# are public certificates, not secrets (the private keys live in
# $STEPPATH/secrets, untouched here), so relaxing read/traverse access is
# safe and is exactly what makes that documented flow actually work.
if [ -d "$CERTS_DIR" ]; then
  chmod 755 "$CERTS_DIR"
  chmod 644 "$CERTS_DIR"/*.crt 2>/dev/null || true
  echo "[step-ca-ui] relaxed permissions on $CERTS_DIR so containers sharing this volume can read the public root/intermediate certs"
fi

if [ -f "$CA_CONFIG" ]; then
  tmp="$(mktemp)"
  if jq \
    --arg name "$PROVISIONER_NAME" \
    --arg default_duration "$DEFAULT_TLS_DURATION" \
    --arg max_duration "$MAX_TLS_DURATION" \
    '
      def apply_claims:
        map(
          if .name == $name then
            .claims = (.claims // {}) |
            .claims.defaultTLSCertDuration = $default_duration |
            .claims.maxTLSCertDuration = $max_duration
          else
            .
          end
        );

      if .authority.provisioners? then
        .authority.provisioners |= apply_claims
      elif .provisioners? then
        .provisioners |= apply_claims
      else
        .
      end
    ' "$CA_CONFIG" > "$tmp"; then
    cat "$tmp" > "$CA_CONFIG"
    echo "[step-ca-ui] ensured TLS duration claims for provisioner '$PROVISIONER_NAME': default=$DEFAULT_TLS_DURATION max=$MAX_TLS_DURATION"
  else
    echo "[step-ca-ui] failed to update TLS duration claims in $CA_CONFIG" >&2
    rm -f "$tmp"
    exit 1
  fi
  rm -f "$tmp"
fi

exec "$@"
