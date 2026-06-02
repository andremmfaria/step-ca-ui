#!/usr/bin/env bash
# coverage-gate.sh — ratcheted coverage gate for step-ca-ui.
#
# Usage:
#   ./scripts/coverage-gate.sh [THRESHOLD]
#
# THRESHOLD defaults to the THRESHOLD environment variable, then to 15 (the
# honest measured baseline set in PR-22: 15.4% total, floored to 15).
# Reaching 80% requires DI seams — a DB interface for handlers/ and an issuer
# interface for le/ — tracked as a follow-up refactor wave.
#
# Exemptions (kept thin so the number reflects logic, not bootstrap):
#   - main() wiring in the root package (step-ui) is excluded because it is
#     a one-liner entrypoint that calls handlers.New + chi setup; testing it
#     requires a live DB and port.  The root package contributes 0 statements
#     to the profile and is therefore invisible to the gate.
#   - Generated / templated glue: none currently exists; if added, exclude via
#     //go:build ignore or a separate module so it never enters coverage.out.
#
# The gate compares the *total* line from `go tool cover -func` which sums all
# non-excluded packages.

set -euo pipefail

THRESHOLD="${1:-${THRESHOLD:-15}}"

PROFILE="${COVERPROFILE:-coverage.out}"

if [ ! -f "$PROFILE" ]; then
    echo "[coverage-gate] running go test to generate $PROFILE …"
    go test ./... -race -covermode=atomic -coverprofile="$PROFILE"
fi

total=$(go tool cover -func="$PROFILE" | awk '/^total:/ { gsub(/%/, "", $3); print $3 }')

if [ -z "$total" ]; then
    echo "[coverage-gate] ERROR: could not parse total coverage from $PROFILE" >&2
    exit 1
fi

echo "[coverage-gate] total coverage: ${total}%  (threshold: ${THRESHOLD}%)"

# awk arithmetic comparison avoids bash floating-point limitations.
if awk -v t="$total" -v min="$THRESHOLD" 'BEGIN { exit !(t+0 >= min+0) }'; then
    echo "[coverage-gate] PASS"
else
    echo "[coverage-gate] FAIL — coverage ${total}% is below threshold ${THRESHOLD}%" >&2
    exit 1
fi
