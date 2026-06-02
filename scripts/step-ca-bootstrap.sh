#!/bin/sh
set -eu

CA_CONFIG="${STEPPATH:-/home/step}/config/ca.json"
PROVISIONER_NAME="${DOCKER_STEPCA_INIT_PROVISIONER_NAME:-${PROVISIONER:-admin}}"
DEFAULT_TLS_DURATION="${STEPCA_DEFAULT_TLS_CERT_DURATION:-8760h}"
MAX_TLS_DURATION="${STEPCA_MAX_TLS_CERT_DURATION:-87600h}"

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
