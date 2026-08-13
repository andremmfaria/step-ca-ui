#!/usr/bin/env bash
# One entry point per bootstrap scenario (Section 2.7.2). Each names its compose
# overrides and its grep filter, then hands off to the infra project, which owns
# the compose lifecycle for its own disposable stack.
#
# The scenario runs against a copy of .env, never the checkout's own, so a case
# that flips UI_TLS_MODE or plants a wrong CA_FINGERPRINT leaves nothing behind.
set -euo pipefail

scenario="${1:-}"
repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
e2e_dir="$repo_root/test/e2e"

usage() {
  echo "usage: $0 <selfsigned|provided|ca-down|fingerprint|fatals>" >&2
  exit 2
}
[[ -n "$scenario" ]] || usage

# In CI the image job already built step-ca-ui:e2e, so the driver composes the
# image override in and the specs never pass --build.
image_override=""
if [[ "${E2E_USE_PREBUILT_IMAGE:-}" == "1" ]]; then
  image_override="compose.e2e-image.yml"
fi

case "$scenario" in
  selfsigned)  overrides="";                                            filter='E2E-BOOT-04' ;;
  provided)    overrides="";                                            filter='E2E-BOOT-03' ;;
  ca-down)     overrides="compose.e2e-nodeps.yml";                      filter='E2E-BOOT-02|E2E-BOOT-09' ;;
  fingerprint) overrides="compose.e2e-fingerprint.yml";                 filter='E2E-BOOT-01|E2E-BOOT-05|E2E-BOOT-06' ;;
  fatals)      overrides="compose.e2e-fatals.yml:compose.e2e-nodeps.yml"; filter='E2E-BOOT-07' ;;
  *)           usage ;;
esac

# E2E-BOOT-09 needs the fingerprint override's writable, empty ROOT_CERT path;
# E2E-BOOT-02 needs the stock read-only mount so a root cert exists. They share a
# scenario, so the spec composes the extra file itself per case.
if [[ "$scenario" == "ca-down" ]]; then
  export E2E_CA_DOWN_FINGERPRINT_OVERRIDE="compose.e2e-fingerprint.yml"
fi

if [[ -n "$image_override" ]]; then
  overrides="${overrides:+$overrides:}$image_override"
fi

env_file="$repo_root/.env.e2e-$scenario"
cp "$repo_root/.env" "$env_file"
trap 'rm -f "$env_file"' EXIT

# Case (b) of the fatals scenario needs the admin password absent, so that
# scenario is the one place the driver does not set it.
if [[ "$scenario" != "fatals" ]]; then
  if ! grep -qE '^STEPUI_ADMIN_PASSWORD=.+' "$env_file"; then
    echo "FATAL: STEPUI_ADMIN_PASSWORD is unset in .env; every scenario except 'fatals' needs it" >&2
    exit 1
  fi
fi

export E2E_COMPOSE_OVERRIDES="$overrides"
export E2E_ENV_FILE="$env_file"
export E2E_SCENARIO="$scenario"

echo "scenario=$scenario overrides=[${overrides:-none}] filter=$filter env_file=$env_file"

cd "$e2e_dir"
exec npx playwright test --project=infra -g "$filter"
