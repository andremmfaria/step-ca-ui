#!/bin/sh
set -e

echo "======================================="
echo "  Step-CA UI (Go) — starting up"
echo "======================================="

# Ждём PostgreSQL
echo "[*] Waiting for PostgreSQL..."
until nc -z postgres 5432 2>/dev/null; do
  sleep 1
done
echo "[*] PostgreSQL is ready!"

echo "[*] CA readiness is now reported via /ready — not blocking startup on Step-CA"

# SSL сертификат для UI
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

echo "[*] Starting Step-CA UI on port ${PORT:-8443}"
# Seed initial admin password from STEPUI_ADMIN_PASSWORD if provided.
# The Go app reads this on first boot when no admin user exists.
if [ -n "${STEPUI_ADMIN_PASSWORD:-}" ]; then
  echo "[*] STEPUI_ADMIN_PASSWORD detected — will seed first admin with it"
  export STEPUI_ADMIN_PASSWORD
fi


exec /opt/step-ui/step-ui
