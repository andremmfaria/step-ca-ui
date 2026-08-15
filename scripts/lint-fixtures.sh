#!/usr/bin/env bash
# Asserts the depguard rules in backend/.golangci.yml actually deny. Run from
# the repository root or from backend/.
#
# Section 2 requires a committed negative fixture the lint run must reject,
# because a rule nobody tests is a rule that stops working the next time the
# config is refactored and nobody notices.
set -euo pipefail

cd "$(dirname "$0")/../backend"

FIXTURE=testdata/lintfixtures/api_imports_models.go.fixture
TARGET=api/zz_lint_fixture.go
trap 'rm -f "$TARGET"' EXIT

cp "$FIXTURE" "$TARGET"

# The repository config is used as-is: passing --no-config alongside --config
# silently disables the very rules being tested.
#
# The output is captured before grepping rather than piped into it. golangci-lint
# exits non-zero whenever it reports anything, and under `set -o pipefail` that
# makes the whole pipeline non-zero even when grep matches, so a piped form
# reports "not rejected" for a fixture that was in fact rejected.
OUTPUT=$(golangci-lint run ./api/ 2>&1 || true)

if grep -q "(depguard)" <<< "$OUTPUT"; then
  echo "depguard rejected the fixture, as required"
  exit 0
fi

echo "::error::depguard did NOT reject $FIXTURE; the api/ import rules are not working"
exit 1
