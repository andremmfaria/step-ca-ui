#!/usr/bin/env bash
# Artifact collector (Section 4.6). Redacts before it writes, tolerates a dead or
# absent stack, and always emits at least a marker file so that
# `if-no-files-found: error` catches the collector failing outright rather than
# masking the real failure behind a missing artifact.
#
# ::add-mask:: only masks the live log stream, never an uploaded artifact, which
# is why assert-redacted.sh runs between this and the upload.
set -uo pipefail

dest="${1:-}"
[[ -n "$dest" ]] || { echo "usage: $0 <dir>" >&2; exit 2; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

mkdir -p "$dest"
{
  echo "collected_at=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "scenario=${E2E_SCENARIO:-none}"
  echo "overrides=${E2E_COMPOSE_OVERRIDES:-none}"
  echo "env_file=${E2E_ENV_FILE:-.env}"
} > "$dest/collection-marker.txt"

compose_args=(-f docker-compose.yml)
IFS=':' read -r -a extra <<< "${E2E_COMPOSE_OVERRIDES:-}"
for f in "${extra[@]}"; do [[ -n "$f" ]] && compose_args+=(-f "$f"); done
[[ -n "${E2E_ENV_FILE:-}" ]] && compose_args+=(--env-file "${E2E_ENV_FILE}")

dc() { docker compose "${compose_args[@]}" "$@"; }

# Values that must never reach an uploaded artifact. Read off the mounted files
# rather than passed in, which is one fewer thing to keep in sync.
secrets=()
for f in secrets/ca_password secrets/secret_key secrets/postgres_password; do
  [[ -s "$f" ]] && secrets+=("$(tr -d '\r\n' < "$f")")
done
if admin_pw="$(sed -n 's/^STEPUI_ADMIN_PASSWORD=//p' "${E2E_ENV_FILE:-.env}" 2>/dev/null | head -1 | tr -d '"'"'"'\r')"; then
  [[ -n "$admin_pw" ]] && secrets+=("$admin_pw")
fi

redact() {
  local sed_args=()
  for s in "${secrets[@]}"; do
    [[ ${#s} -ge 6 ]] || continue
    sed_args+=(-e "s|$(printf '%s' "$s" | sed 's/[][\\.*^$/&]/\\&/g')|***REDACTED***|g")
  done
  if [[ ${#sed_args[@]} -eq 0 ]]; then cat; else sed "${sed_args[@]}"; fi
}

echo "collecting into $dest"

# step-ui's log comes from the cumulative capture: a mid-run recreate truncates
# the live view to the current container alone (Section 2.6).
cumulative="${E2E_CUMULATIVE_LOG:-test/e2e/artifacts/step-ui-cumulative.log}"
{
  [[ -s "$cumulative" ]] && cat "$cumulative"
  dc logs --no-color --timestamps step-ui 2>&1
} | redact > "$dest/logs-step-ui.log"

for svc in postgres step-ca mock-oidc mailpit pebble; do
  out="$(dc logs --no-color --timestamps "$svc" 2>&1)"
  [[ -n "$out" ]] && printf '%s\n' "$out" | redact > "$dest/logs-$svc.log"
done

# State.Health.Log is the only place a failing probe's own output exists, and
# `docker compose down` destroys it.
for cid in $(dc ps -aq 2>/dev/null); do
  name="$(docker inspect --format '{{.Name}}' "$cid" 2>/dev/null | tr -d '/')"
  docker inspect "$cid" 2>/dev/null | redact > "$dest/inspect-${name:-$cid}.json"
done

dc config 2>&1 | redact > "$dest/compose-config.yml"

for table in certificates cert_history auth_log users; do
  dc exec -T postgres psql -U stepui -d stepui -c "\\copy (SELECT * FROM $table) TO STDOUT WITH CSV HEADER" \
    2>/dev/null | redact > "$dest/db-$table.csv"
done

# scripts/step-ca-bootstrap.sh rewrites the claims on every start, so the file on
# the volume is not the file in the repository.
dc exec -T step-ca cat /home/step/config/ca.json 2>/dev/null | redact > "$dest/ca.json"

port="$(sed -n 's/^UI_HTTPS_PORT=//p' "${E2E_ENV_FILE:-.env}" 2>/dev/null | head -1)"
port="${port:-443}"
openssl s_client -connect "localhost:${port}" -showcerts </dev/null 2>/dev/null > "$dest/s_client.txt" || true
openssl s_client -connect "localhost:${port}" </dev/null 2>/dev/null \
  | openssl x509 -outform pem > "$dest/served-leaf.pem" 2>/dev/null || true

# secrets/ and any backup bundle are never collected.
if [[ -f "${E2E_ENV_FILE:-.env}" ]]; then
  redact < "${E2E_ENV_FILE:-.env}" > "$dest/env.redacted"
fi

find "$dest" -type f -empty -delete
echo "collected $(find "$dest" -type f | wc -l) file(s)"
exit 0
