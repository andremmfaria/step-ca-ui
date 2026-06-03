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

# SSL certificate for the UI
if [ ! -f /opt/step-ui/ssl/server.crt ]; then
  echo "[*] Generating self-signed SSL certificate..."
  openssl req -x509 -nodes -days 3650 -newkey rsa:2048     -keyout /opt/step-ui/ssl/server.key     -out /opt/step-ui/ssl/server.crt     -subj "/CN=${HOST_IP:-localhost}"     -addext "subjectAltName=IP:${HOST_IP:-127.0.0.1},DNS:localhost" 2>/dev/null
fi

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

# Root CA certificate. When CA_ROOT_CERT_PEM is provided (e.g. injected from a
# secret in ECS, where no /home/step volume is mounted), write it to the path the
# Go app reads as ROOT_CERT so CA verification and the diagnostics console work.
# In docker-compose CA_ROOT_CERT_PEM is unset and the cert is mounted instead, so
# this block is skipped.
ROOT_CERT="${ROOT_CERT:-/home/step/certs/root_ca.crt}"
if [ -n "${CA_ROOT_CERT_PEM:-}" ]; then
  echo "[*] Writing root CA certificate to $ROOT_CERT"
  mkdir -p "$(dirname "$ROOT_CERT")"
  printf "%s" "$CA_ROOT_CERT_PEM" > "$ROOT_CERT"
  chmod 644 "$ROOT_CERT"
fi
export ROOT_CERT

echo "[*] Starting Step-CA UI on port ${PORT:-8443}"
# Seed initial admin password from STEPUI_ADMIN_PASSWORD if provided.
# The Go app reads this on first boot when no admin user exists.
if [ -n "${STEPUI_ADMIN_PASSWORD:-}" ]; then
  echo "[*] STEPUI_ADMIN_PASSWORD detected — will seed first admin with it"
  export STEPUI_ADMIN_PASSWORD
fi


exec /opt/step-ui/step-ui
