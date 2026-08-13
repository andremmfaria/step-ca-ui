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
#   - None. The root package (step-ui) is NOT excluded and DOES contribute
#     real statements to the profile — tlsreload.go's certReloader and
#     tlsbootstrap.go's TLS-bootstrap helpers (writeInlineRootCert,
#     ensureRootCert, generateSelfSignedCert, ensureUICert, renewUICertOnce)
#     are all covered by root-package _test.go files. A prior version of this
#     comment claimed the root package contributed 0 statements and was
#     therefore invisible to the gate; that was already stale before this
#     fix (tlsreload_test.go predates it) and is corrected here.
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
