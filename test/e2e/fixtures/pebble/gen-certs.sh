#!/usr/bin/env bash
# Generates the ACME server's own TLS material for the Let's Encrypt leg:
# a root (minica.pem) that step-ui trusts through LEGO_CA_CERTIFICATES, and a
# leaf whose SAN is the compose service name step-ui dials. Pebble's shipped
# certificate carries only localhost/127.0.0.1, so it cannot be reused.
#
# Idempotent: regenerates only when the leaf is missing or FORCE=1.
set -euo pipefail

cert_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/certs"
mkdir -p "$cert_dir"

if [[ -s "$cert_dir/pebble.crt" && "${FORCE:-}" != "1" ]]; then
  echo "pebble certs already present: $cert_dir (FORCE=1 to regenerate)"
  exit 0
fi

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 365 \
  -subj "/CN=step-ca-ui e2e pebble root" \
  -addext "basicConstraints=critical,CA:TRUE" \
  -addext "keyUsage=critical,keyCertSign,cRLSign" \
  -keyout "$cert_dir/minica.key" -out "$cert_dir/minica.pem" 2>/dev/null

openssl req -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes \
  -subj "/CN=pebble" \
  -keyout "$cert_dir/pebble.key" -out "$cert_dir/pebble.csr" 2>/dev/null

openssl x509 -req -in "$cert_dir/pebble.csr" -days 365 \
  -CA "$cert_dir/minica.pem" -CAkey "$cert_dir/minica.key" -CAcreateserial \
  -extfile <(printf 'subjectAltName=DNS:pebble,DNS:localhost,IP:127.0.0.1\nextendedKeyUsage=serverAuth\n') \
  -out "$cert_dir/pebble.crt" 2>/dev/null

rm -f "$cert_dir/pebble.csr" "$cert_dir/minica.srl"
# pebble runs as a non-root uid in its own image and reads both files.
chmod 644 "$cert_dir/pebble.key" "$cert_dir/minica.pem" "$cert_dir/pebble.crt"

echo "generated pebble certs in $cert_dir"
