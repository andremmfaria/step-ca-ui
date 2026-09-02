# Contract proof

Phase 2 of `plans/frontend-backend-split.md` asks for a real two-commit proof that the consumption mechanism actually works, not just that its steps exist. This is that proof.

## What is being proven

D8 and 7.6 make one claim: the `frontend` job's provenance assertion is what stops a stale client from going green. Without it, the frontend job could install a client from an npm cache, a previously published version, or a lockfile edit, and pass against a contract the Go code does not implement. The assertion is `node -p "require('@andremmfaria/step-ca-ui-client/package.json').version"`, checked for the commit's short sha.

To prove the assertion is load-bearing rather than decorative, `scripts/client-version.sh` was deliberately regressed to stop appending the sha suffix, on a short-lived branch, `proof/contract-negative`, never merged into main. Both commits were run through `ci.yml` via `workflow_dispatch` against that branch, since `ci.yml` only triggers automatically on `main` and on pull requests, and this repository takes no pull requests.

## Commit 1, red

Commit `2f83d7c6bc0fec0410757b2aa68e59ad85034592`, "proof(red): drop the commit sha from the derived client version", removed the `-sha.<short-sha>` suffix from the derived version.

Run: https://github.com/andremmfaria/step-ca-ui/actions/runs/33632738935

Result: `frontend` and `ci-gate` failed. Every other job, including `client` itself, stayed green, because nothing in the client job's own steps validates the shape of the version it packs. The frontend job's failure line:

```
##[error]installed client version 0.1.206 does not contain commit 2f83d7c
```

This is the exact failure mode D8 warns about, produced on demand rather than by accident, and caught in the one place designed to catch it.

## Commit 2, green

Commit `9c8178ca49233f1508f25efde7b7a6edf9eeb6f2`, "proof(green): restore the commit sha in the derived client version", reverted the regression.

Run: https://github.com/andremmfaria/step-ca-ui/actions/runs/33633062420

Result: every job, including `frontend` and `ci-gate`, passed.

## What this also proves

The same red run is the `ci-gate` failure proof required elsewhere in Phase 2's exit criteria: `ci-gate` needs `[build-test-lint, db-integration, client, frontend]`, and it failed correctly when exactly one of those four, `frontend`, failed, while the other three stayed green.

## The ongoing guarantee

A one-off proof decays the moment nobody remembers it happened. The `contract-negative` job in `ci.yml` re-runs the same class of check on every `workflow_dispatch` and on any push touching `ci.yml`, `clients/ts/**` or `backend/cmd/openapi/**`. It does not need the `proof/contract-negative` branch to exist: it builds a real client from the current commit, stamps a version that deliberately omits the commit's sha, and asserts that installing it and reading its version back does not falsely match. That job is required to stay green going forward, so a future change that quietly breaks the provenance check, not just the version format tested here, still gets caught.

The `proof/contract-negative` branch was deleted after both runs completed. Its two commits are reachable only by sha, which is the point: the proof is this document and the two run URLs above, not a branch someone could accidentally build on.
