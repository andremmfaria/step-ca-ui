#!/usr/bin/env bash
# Fails when a breaking contract change lands without a matching row in
# docs/contract-changes.md.
#
# /api/v1 is permanent by construction, so the remedy for a breaking change is
# a sentence a reviewer reads rather than a version integer no resolver reads.
# See Q5 and 7.2 of plans/frontend-backend-split.md.
set -euo pipefail

BASE_SHA="${1:-}"
SPEC=backend/openapi/openapi.json
LEDGER=docs/contract-changes.md

if [[ -z "$BASE_SHA" || "$BASE_SHA" =~ ^0+$ ]]; then
  echo "no base revision to compare against; skipping the breaking-change gate"
  exit 0
fi

if ! git cat-file -e "$BASE_SHA:$SPEC" 2>/dev/null; then
  echo "base revision has no $SPEC; nothing to compare"
  exit 0
fi

BASE_SPEC=$(mktemp)
trap 'rm -f "$BASE_SPEC"' EXIT
git show "$BASE_SHA:$SPEC" > "$BASE_SPEC"

if ! command -v oasdiff >/dev/null 2>&1; then
  echo "::error::oasdiff is not installed; the breaking-change gate cannot run"
  exit 1
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "::error::jq is not installed; the breaking-change gate cannot run"
  exit 1
fi

# A non-zero exit means oasdiff itself failed. Finding breaking changes is not
# an error here: the gate is about whether they are recorded. Swallowing the
# exit code with `|| true` would make a broken tool look like a clean contract,
# which is the fail-open shape this gate exists to remove.
if ! BREAKING=$(oasdiff breaking "$BASE_SPEC" "$SPEC" -f json); then
  echo "::error::oasdiff failed to compare $BASE_SHA against the working tree"
  exit 1
fi

if [[ -z "$BREAKING" || "$BREAKING" == "null" || "$BREAKING" == "[]" ]]; then
  echo "no breaking changes"
  exit 0
fi

OPERATIONS=$(printf '%s' "$BREAKING" | jq -r '[.[].operationId] | map(select(. != null and . != "")) | unique | .[]')

if [[ -z "$OPERATIONS" ]]; then
  echo "::error::oasdiff reports breaking changes it could not attribute to an operation. Review them by hand:"
  printf '%s\n' "$BREAKING"
  exit 1
fi

MISSING=()
while IFS= read -r op; do
  if ! grep -qE "^\|[[:space:]]*\`?${op}\`?[[:space:]]*\|" "$LEDGER"; then
    MISSING+=("$op")
  fi
done <<< "$OPERATIONS"

if [[ ${#MISSING[@]} -gt 0 ]]; then
  echo "::error::breaking changes with no row in $LEDGER: ${MISSING[*]}"
  echo "Add one row per operation: | operationId | what changed | why |"
  exit 1
fi

echo "every breaking change is recorded in $LEDGER"
