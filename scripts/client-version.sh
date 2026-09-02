#!/usr/bin/env bash
# Version scheme is D8 of plans/frontend-backend-split.md. PATCH is derived,
# never hand-edited, so two pull requests cannot collide on the same number.
set -euo pipefail

cd "$(dirname "$0")/.."

MINOR=$(tr -d '[:space:]' < backend/openapi/package-version.txt)

if PATCH=$(git rev-list --count origin/main 2>/dev/null) && [[ -n "$PATCH" ]]; then
  SHA=$(git rev-parse --short HEAD)
  echo "0.${MINOR}.${PATCH}-sha.${SHA}"
else
  echo "0.${MINOR}.0-dev"
fi
