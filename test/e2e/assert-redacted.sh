#!/usr/bin/env bash
# E2E-SEC-04's canary sweep over a collected artifact directory, run between
# collection and upload. A hit fails the job: ::add-mask:: covers the live log
# stream only and does nothing for an uploaded file.
set -uo pipefail

dir="${1:-}"
[[ -n "$dir" ]] || { echo "usage: $0 <dir>" >&2; exit 2; }
[[ -d "$dir" ]] || { echo "FAIL: $dir does not exist" >&2; exit 1; }

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

canaries=()
names=()
for f in ca_password secret_key postgres_password; do
  if [[ -s "secrets/$f" ]]; then
    canaries+=("$(tr -d '\r\n' < "secrets/$f")")
    names+=("secrets/$f")
  fi
done
admin_pw="$(sed -n 's/^STEPUI_ADMIN_PASSWORD=//p' "${E2E_ENV_FILE:-.env}" 2>/dev/null | head -1 | tr -d '"'"'"'\r')"
if [[ -n "$admin_pw" ]]; then
  canaries+=("$admin_pw")
  names+=("STEPUI_ADMIN_PASSWORD")
fi

# An empty canary list would make this sweep vacuous, and a vacuous pass here is
# worse than no check at all.
if [[ ${#canaries[@]} -eq 0 ]]; then
  echo "FAIL: no canary values found; the sweep would pass vacuously" >&2
  exit 1
fi

# A private key in an artifact is a finding regardless of which key it is.
patterns=("BEGIN RSA PRIVATE KEY" "BEGIN EC PRIVATE KEY" "BEGIN PRIVATE KEY")

status=0
for i in "${!canaries[@]}"; do
  value="${canaries[$i]}"
  [[ ${#value} -ge 6 ]] || continue
  if hits="$(grep -rlF -- "$value" "$dir" 2>/dev/null)"; then
    echo "FAIL: ${names[$i]} appears verbatim in the collected artifact:" >&2
    printf '  %s\n' $hits >&2
    status=1
  fi
done

for pattern in "${patterns[@]}"; do
  if hits="$(grep -rlF -- "$pattern" "$dir" 2>/dev/null)"; then
    echo "FAIL: '$pattern' appears in the collected artifact:" >&2
    printf '  %s\n' $hits >&2
    status=1
  fi
done

if [[ $status -eq 0 ]]; then
  echo "redaction sweep clean: ${#canaries[@]} canaries, ${#patterns[@]} key patterns, $(find "$dir" -type f | wc -l) file(s)"
fi
exit $status
