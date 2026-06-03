#!/bin/sh
set -e

echo "======================================="
echo "  Step-CA UI (Go) — starting up"
echo "======================================="

# ─── Secret-file helpers ──────────────────────────────────────────────────
# read_secret_file VAR FILE
# If VAR is already set, use it (backward-compat plain-env path).
# Otherwise read the value from FILE and export VAR.
read_secret_file() {
  _var="$1"
  _file="$2"
  eval "_current=\${${_var}:-}"
  if [ -n "$_current" ]; then
    return 0  # plain env already set — honour it
  fi
  if [ -f "$_file" ]; then
    _val="$(cat "$_file")"
    export "${_var}=${_val}"
  fi
}

# ─── Secrets from files ───────────────────────────────────────────────────
# POSTGRES_PASSWORD: used to construct DATABASE_URL below.
read_secret_file POSTGRES_PASSWORD "${POSTGRES_PASSWORD_FILE:-/run/secrets/postgres_password}"

# SECRET_KEY: session/CSRF signing key read by the Go app.
read_secret_file SECRET_KEY "${SECRET_KEY_FILE:-/run/secrets/secret_key}"

# PROVISIONER_PASSWORD: step-ca provisioner password (already handled below,
# but ensure the plain-env fallback still works).
read_secret_file PROVISIONER_PASSWORD "${PROVISIONER_PASSWORD_FILE:-/run/secrets/ca_password}"

# ─── DATABASE_URL construction ────────────────────────────────────────────
# Construct DATABASE_URL from parts so the password never appears in the
# compose environment block or `docker inspect`.  If DATABASE_URL is already
# set (plain-env backward-compat), use it as-is.
if [ -z "${DATABASE_URL:-}" ]; then
  _pg_host="${POSTGRES_HOST:-postgres}"
  _pg_port="${POSTGRES_PORT:-5432}"
  _pg_user="${POSTGRES_USER:-stepui}"
  _pg_db="${POSTGRES_DB:-stepui}"
  if [ -z "${POSTGRES_PASSWORD:-}" ]; then
    echo "[!] Neither DATABASE_URL nor POSTGRES_PASSWORD/POSTGRES_PASSWORD_FILE is set."
    echo "[!] Cannot construct the database connection string — aborting."
    exit 1
  fi
  export DATABASE_URL="postgres://${_pg_user}:${POSTGRES_PASSWORD}@${_pg_host}:${_pg_port}/${_pg_db}?sslmode=disable"
fi

# ─── Wait for PostgreSQL ──────────────────────────────────────────────────
# Derive host/port from DATABASE_URL so the wait works in any deployment
# (docker-compose "postgres:5432", an RDS endpoint, a managed PG, etc.) instead
# of assuming the compose service name. Bounded and non-fatal: if PG is not yet
# reachable we proceed and let the Go app's own connect/retry handle it.
_rest="${DATABASE_URL#*://}" # strip scheme
_hostport="${_rest##*@}"     # drop userinfo (handles '@' in the password)
_hostport="${_hostport%%/*}" # drop /dbname
_hostport="${_hostport%%\?*}" # drop ?query
_db_host="${_hostport%%:*}"
case "$_hostport" in
*:*) _db_port="${_hostport##*:}" ;;
*) _db_port=5432 ;;
esac
echo "[*] Waiting for PostgreSQL at ${_db_host}:${_db_port}..."
_ok=0
_i=0
while [ "$_i" -lt 60 ]; do
  if nc -z "$_db_host" "$_db_port" 2>/dev/null; then
    _ok=1
    break
  fi
  _i=$((_i + 1))
  sleep 1
done
if [ "$_ok" -eq 1 ]; then
  echo "[*] PostgreSQL is ready!"
else
  echo "[!] PostgreSQL not reachable at ${_db_host}:${_db_port} after ${_i}s — continuing; the app will retry."
fi

echo "[*] CA readiness is now reported via /ready — not blocking startup on Step-CA"

# ─── Provisioner password file ─────────────────────────────────────────────
# Established before the TLS block because UI_TLS_MODE=stepca needs it to
# authenticate `step ca certificate`.
PASSWORD_FILE="${PASSWORD_FILE:-/opt/step-ui/data/provisioner_password}"
if [ -f "$PASSWORD_FILE" ]; then
  echo "[*] Provisioner password file found"
elif [ -n "${PROVISIONER_PASSWORD:-}" ]; then
  echo "[*] Writing provisioner password file"
  mkdir -p "$(dirname "$PASSWORD_FILE")"
  printf "%s" "$PROVISIONER_PASSWORD" > "$PASSWORD_FILE"
  chmod 600 "$PASSWORD_FILE"
else
  echo "[!] Provisioner password file not found: $PASSWORD_FILE"
  echo "[!] Set CA_PASSWORD in .env or create this file with the step-ca provisioner password."
fi
export PASSWORD_FILE

# ─── Root CA certificate ──────────────────────────────────────────────────────
# Established before the TLS block because UI_TLS_MODE=stepca needs $ROOT_CERT
# to verify the CA when requesting the leaf cert.
# Precedence (highest → lowest):
#   1. CA_ROOT_CERT_PEM set  → write PEM inline to $ROOT_CERT (ECS / no-volume path)
#   2. CA_FINGERPRINT set AND $ROOT_CERT is absent/empty → download from step-ca
#      with bounded retry (CA may not be up yet); on failure log a warning and
#      continue — the app boots fine on self-signed and the CA will surface errors
#      through its own health endpoint.
#   3. Otherwise → assume the file is already present (docker-compose volume mount)
#
# CA_FINGERPRINT is a public, non-secret value (SHA-256 of the root cert DER).
ROOT_CERT="${ROOT_CERT:-/home/step/certs/root_ca.crt}"
if [ -n "${CA_ROOT_CERT_PEM:-}" ]; then
  echo "[*] Writing root CA certificate to $ROOT_CERT (from CA_ROOT_CERT_PEM)"
  mkdir -p "$(dirname "$ROOT_CERT")"
  printf "%s" "$CA_ROOT_CERT_PEM" > "$ROOT_CERT"
  chmod 644 "$ROOT_CERT"
elif [ -n "${CA_FINGERPRINT:-}" ]; then
  # Download only if $ROOT_CERT is absent or empty.
  if [ ! -s "${ROOT_CERT}" ]; then
    echo "[*] Downloading root CA certificate via CA_FINGERPRINT..."
    mkdir -p "$(dirname "$ROOT_CERT")"
    _fp_ok=0
    _fi=0
    while [ "$_fi" -lt 30 ]; do
      if step ca root "$ROOT_CERT" \
           --ca-url "$CA_URL" \
           --fingerprint "$CA_FINGERPRINT" \
           --force 2>/dev/null; then
        _fp_ok=1
        break
      fi
      _fi=$((_fi + 1))
      echo "[*] Waiting for step-ca root (attempt ${_fi}/30)..."
      sleep 1
    done
    if [ "$_fp_ok" -eq 1 ]; then
      echo "[*] Root CA certificate downloaded to $ROOT_CERT"
    else
      echo "[!] Could not download root CA after 30 attempts — continuing without it"
    fi
  else
    echo "[*] $ROOT_CERT already present — skipping CA_FINGERPRINT download"
  fi
fi
export ROOT_CERT

# ─── TLS cert for the UI ──────────────────────────────────────────────────────
# UI_TLS_MODE controls how the UI's own TLS certificate is obtained:
#   self-signed  (default) — generate a self-signed cert if absent (original behaviour)
#   provided               — expect a cert already at SSL_CERT/SSL_KEY; generate nothing
#   stepca                 — obtain a leaf cert from the step-ca that this UI manages
#
# Depends on PASSWORD_FILE and ROOT_CERT established above.
# SSL_CERT / SSL_KEY default to the paths the Go app reads.
SSL_CERT="${SSL_CERT:-/opt/step-ui/ssl/server.crt}"
SSL_KEY="${SSL_KEY:-/opt/step-ui/ssl/server.key}"
UI_TLS_MODE="${UI_TLS_MODE:-self-signed}"
UI_HOSTNAME="${UI_HOSTNAME:-}"

# Helper: generate a self-signed cert.  Used both by the self-signed branch and
# as the fallback for the stepca branch when the CA is unreachable.
_gen_self_signed() {
  echo "[*] Generating self-signed SSL certificate..."
  _san="IP:${HOST_IP:-127.0.0.1},DNS:localhost"
  if [ -n "$UI_HOSTNAME" ]; then
    _san="${_san},DNS:${UI_HOSTNAME}"
  fi
  openssl req -x509 -nodes -days 3650 -newkey rsa:2048 \
    -keyout "$SSL_KEY" \
    -out    "$SSL_CERT" \
    -subj "/CN=${UI_HOSTNAME:-${HOST_IP:-localhost}}" \
    -addext "subjectAltName=${_san}" 2>/dev/null
}

case "$UI_TLS_MODE" in
  provided)
    # Operator supplies the cert via a mount or prior init container; do nothing.
    echo "[*] UI_TLS_MODE=provided — using cert at $SSL_CERT"
    ;;

  stepca)
    echo "[*] UI_TLS_MODE=stepca — obtaining leaf cert from step-ca"
    _ui_host="${UI_HOSTNAME:-$(hostname -f 2>/dev/null || hostname)}"
    # The CA may not be up yet at container start.  Retry up to 30 times with
    # 1-second gaps.
    _stepca_ok=0
    _si=0
    while [ "$_si" -lt 30 ]; do
      if step ca certificate "$_ui_host" "$SSL_CERT" "$SSL_KEY" \
           --ca-url "$CA_URL" \
           --root   "$ROOT_CERT" \
           --provisioner "${PROVISIONER:-admin}" \
           --provisioner-password-file "$PASSWORD_FILE" \
           --force 2>/dev/null; then
        _stepca_ok=1
        break
      fi
      _si=$((_si + 1))
      echo "[*] Waiting for step-ca (attempt ${_si}/30)..."
      sleep 1
    done

    if [ "$_stepca_ok" -eq 1 ]; then
      echo "[*] Leaf cert obtained for $_ui_host"
      # Start the renewal daemon in the background so that `step ca renew`
      # rewrites the cert files in place; the Go hot-reloader (tlsreload.go)
      # picks up the new cert on the next TLS handshake without a restart.
      # This background process is intentionally not supervised — it is a
      # single-container convenience.  PID 1 remains the Go app via exec.
      step ca renew --daemon \
        --ca-url "$CA_URL" \
        --root   "$ROOT_CERT" \
        "$SSL_CERT" "$SSL_KEY" &
      echo "[*] step ca renew daemon started (PID $!)"
    else
      echo "[!] step ca certificate failed after 30 attempts — falling back to self-signed"
      _gen_self_signed
    fi
    ;;

  *)
    # self-signed (default): generate only if the cert is absent.
    # This is the original docker-compose/dev behaviour — unchanged when
    # UI_TLS_MODE is unset.
    if [ ! -f "$SSL_CERT" ]; then
      _gen_self_signed
    fi
    ;;
esac

echo "[*] Starting Step-CA UI on port ${PORT:-8443}"
# Seed initial admin password from STEPUI_ADMIN_PASSWORD if provided.
# The Go app reads this on first boot when no admin user exists.
if [ -n "${STEPUI_ADMIN_PASSWORD:-}" ]; then
  echo "[*] STEPUI_ADMIN_PASSWORD detected — will seed first admin with it"
  export STEPUI_ADMIN_PASSWORD
fi


exec /opt/step-ui/step-ui
