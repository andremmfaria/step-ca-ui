# Development

Local dev loop, tests, lint gates, regenerating the API contract, and the project layout.

## Local dev loop

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui/backend
go mod download
go run .  # requires a running postgres + step-ca, see docker-compose.yml
```

When submitting changes:

- Run `make fmt` and `make lint` before pushing.
- Update relevant tests.
- Keep commits focused and descriptive, following this repository's actual convention: `area: lowercase summary` (`docs(plans):`, `ci:`, `e2e:`, and so on, one area per commit). There is no `CONTRIBUTING` file, the convention is inferred from `git log`.

## Tests

### Unit tests

```bash
make test   # go test -race ./... in backend/
make cover  # go test -coverprofile=coverage.out ./..., then
            # COVERPROFILE=coverage.out THRESHOLD=15 backend/scripts/coverage-gate.sh
```

`ci.yml`'s `build-test-lint` job runs `gofmt -l`, `go vet`, `go build`, `go test -race -coverprofile=coverage.out`, the coverage gate, and `golangci-lint` v2.12.2 via `golangci-lint-action@v9.2.1`, then asserts the depguard fixture in `scripts/lint-fixtures.sh` actually gets rejected. Lint runs against the whole tree, there is no `new-from-rev` or `only-new-issues` baseline that would let a pre-existing finding through. The ruleset itself lives in `backend/.golangci.yml`: `errcheck`, `govet`, `staticcheck`, `ineffassign`, `unused`, `misspell`, `revive`, `gocritic`, `errorlint`, `bodyclose`, `noctx`, `gosec`, `unconvert`, `unparam`, `sloglint`, `depguard` and `gomodguard_v2`.

The coverage gate itself is a floor, not a ratchet against the previous run's number: `THRESHOLD=15` is a fixed value in CI today (`backend/scripts/coverage-gate.sh`'s own comment calls it "the honest measured baseline set in PR-22"). Package coverage from a `go test -coverprofile` run (`backend/coverage.out`) is uneven: `security` 97.0%, `middleware` 90.5%, `config` 85.7% are well covered by unit tests. `le` sits at 45.8% and `handlers` at 17.0%, both leaning on integration and e2e tests rather than unit mocks for a lot of their behaviour. `db` is at 0.8% under a plain unit run because its real coverage lives behind the `integration` build tag and only shows up under `go test -tags=integration`.

### DB integration tests

`ci.yml`'s `db-integration` job runs against a real `postgres:16-alpine` service container:

```bash
go test -tags=integration -race -count=1 ./db/...
```

### End-to-end tests (Playwright)

`test/e2e/` has 24 spec files under `specs/`, organized by domain (`auth/`, `admin/`, `certs/`, `csrf/`, `health/`, `rbac/`, and so on). `playwright.config.ts` defines three projects:

| Project | Matches | Notes |
|---|---|---|
| `api` | `*.api.spec.ts` | Runs as a container on the compose network, dials the `step-ui` service name directly |
| `ui` | `*.ui.spec.ts` | Same network, drives a real Chromium via Playwright |
| `infra` | `*.infra.spec.ts` | Runs on the host, dials the published port. Five spec files under `specs/boot/` now cover E2E-BOOT-01 to -07 and -09 (see below); `make e2e-bootstrap` (below) runs `--project=infra` filtered to whichever IDs the chosen `SCENARIO` names |

`.github/workflows/e2e.yml` builds the application image and a Playwright-plus-docker-CLI harness image, brings up `docker-compose.yml` plus `compose.e2e-image.yml`, and runs **only the `api` project** (`npx playwright test --project=api`). The `ui` project is exercised locally via `make e2e-main` (which runs `api` then `ui`), not in CI today.

Several compose overlays exist purely to reach test scenarios the stock stack can't. `test/e2e/scenario.sh` (invoked by `make e2e-bootstrap SCENARIO=...`, `Makefile:206-210`) is the driver behind the bootstrap scenarios, and only ever applies four of them, chosen per scenario (`scenario.sh:24-32`): `compose.e2e-image.yml`, `compose.e2e-nodeps.yml`, `compose.e2e-fingerprint.yml`, and `compose.e2e-fatals.yml` (the last two together for the `fatals` case). `scripts/step-ca-bootstrap.sh` is unrelated to any of this, it is the CA init script `docker-compose.yml` runs inside the `step-ca` container itself.

| Overlay | Purpose | Applied by |
|---|---|---|
| `compose.e2e-fatals.yml` | Turns off `restart: unless-stopped` so an intended crash stays exited and its exit code can be read | `scenario.sh` (`fatals`) |
| `compose.e2e-fingerprint.yml` | Drops the read-only `step-ca-data` mount so `CA_FINGERPRINT`'s fetch path is actually exercised | `scenario.sh` (`fingerprint`) |
| `compose.e2e-image.yml` | Runs `step-ui` from the image the CI `image` job already built, instead of `up -d --build` | `scenario.sh`, when `E2E_USE_PREBUILT_IMAGE=1` |
| `compose.e2e-nodeps.yml` | Lets `step-ui` start against a stopped step-ca or postgres | `scenario.sh` (`ca-down`, `fatals`) |
| `compose.e2e-config.yml` | Passes `USE_HTTPS`, `ALLOWED_DOMAIN_SUFFIXES`, `UI_CERT_DURATION` through, none of which the stock compose file wires up | the nightly `oidc-mail` leg ([plans/e2e-tests.md](../plans/e2e-tests.md), section 2.7.1), not `scenario.sh` |
| `compose.e2e-le.yml` | A local ACME server (pebble) for the Let's Encrypt leg | the nightly `oidc-mail` leg |
| `compose.e2e-mail.yml` | A mail catcher for reset-link and notification tests | the nightly `oidc-mail` leg |
| `compose.e2e-oidc.yml` | A mock OIDC IdP plus the `OIDC_*` keys the stock compose file omits | the nightly `oidc-mail` leg |

`compose.phase0-spike.yml` is a separate, non-e2e overlay for the Phase 0 SPA/nginx spike, kept out of `docker-compose.yml` so the base stack stays three services until the real split lands, see [docs/architecture.md](architecture.md#migration-state).

The migration plan ([plans/frontend-backend-split.md](../plans/frontend-backend-split.md), section 10) describes the e2e suite as **active**, not paused: 24 `api`/`ui` spec files currently cover 40 of 78 indexed test IDs. Four `ui` spec files were written against the server-rendered markup the migration eventually deletes and will need rewriting or retiring once that markup changes.

`specs/boot/` adds the `infra` project's coverage of the bootstrap matrix (Section 3.1 of [plans/e2e-tests.md](../plans/e2e-tests.md)): `boot-fingerprint.infra.spec.ts` (E2E-BOOT-01, -05, -06, one file since the three share a CA identity across the job), `boot-ca-down.infra.spec.ts` (E2E-BOOT-02, -09), `boot-provided.infra.spec.ts` (E2E-BOOT-03), `boot-selfsigned.infra.spec.ts` (E2E-BOOT-04), and `boot-fatals.infra.spec.ts` (E2E-BOOT-07's five cases plus its positive control). The first four files were verified with real `make e2e-bootstrap SCENARIO=<fingerprint|ca-down|provided|selfsigned>` runs on 2026-09-02; `boot-fatals.infra.spec.ts` is marked `test.fixme` (see the file's own comment) because case (c)'s timing against a stopped-not-absent postgres was not re-confirmed after a same-day fix to how the other cases reach postgres. No CI workflow invokes the `infra` project yet, only `lint-meta.yml`'s harness `tsc`/`eslint` steps cover these files today; wiring `e2e.yml` to actually run `make e2e-bootstrap` per scenario remains open, see `plans/e2e-implementation-status.md`.

## Regenerating the API contract

```bash
cd backend
go run ./cmd/openapi -out openapi/openapi.json   # or: make openapi
```

`ci.yml`'s `client` job fails if this drifts from the committed `openapi.json`. `make hooks` registers a git merge driver that reruns this automatically on a local merge or rebase (note: GitHub's own merge button, squash-merge, and merge queues never invoke local git config, so this only helps a local `git merge`/`git rebase`).

To regenerate the TypeScript client package (`clients/ts`):

```bash
cd clients/ts
npm ci
npm run generate   # @hey-api/openapi-ts against backend/openapi/openapi.json
npm run build
```

The published version string is computed by `scripts/client-version.sh`, never hand-edited, see [docs/architecture.md](architecture.md#the-generated-typescript-client). `ci.yml`'s `client` job does more than build it: it stamps the computed version onto the generated package with `npm version --no-git-tag-version`, runs `publint` and `attw --pack . --profile esm-only --ignore-rules internal-resolution-error` against it, then `npm pack`s the result for the `frontend` job to install (`ci.yml:174-194`).

## Lint gates

| Workflow | Job(s) |
|---|---|
| `ci.yml` | `build-test-lint`, `db-integration`, `client`, `frontend`, `contract-negative`, `ci-gate` (fails unless the first four all succeeded) |
| `lint-meta.yml` | `hadolint`, `actionlint`, `yamllint`, `style` (stylelint/eslint/djlint plus the e2e harness's own typecheck/eslint) |
| `security.yml` | `gosec`, `govulncheck`, `gitleaks`, `trivy-fs`, `trivy-image`, see [docs/security.md](security.md#supply-chain-security) |
| `codeql.yml` | `analyze` (Go) |
| `e2e.yml` | `image`, `e2e-main` (the `api` project only), `e2e-gate` |
| `docker-build.yml` | Builds and, on `main` or a `v*` tag, publishes the image |

There is no markdown linter wired into CI (no markdownlint config or job exists in this repository), and `.pre-commit-config.yaml` covers whitespace/EOL hygiene, gofumpt, golangci-lint, stylelint, eslint and djlint, run locally via `pre-commit`, not in CI.

## Project structure

```
.
├── docker-compose.yml         # 3 services: postgres, step-ca, step-ui
├── compose.e2e-*.yml          # e2e-only overlays, see above
├── compose.phase0-spike.yml   # Phase 0 SPA/nginx spike overlay
├── .env.example               # configuration template
├── Makefile                   # setup, up, down, backup, test, lint, e2e-*, ...
├── secrets/                   # generated by `make setup`, not committed
├── assets/                    # logo.svg, used by README.md
├── scripts/
│   ├── client-version.sh      # derives the TS client's published version
│   ├── contract-gate.sh       # fails a breaking change with no contract-changes.md row
│   ├── lint-fixtures.sh       # asserts the depguard negative fixture is rejected
│   ├── step-ca-bootstrap.sh   # step-ca init script
│   └── test_deploy.sh         # simulates a fresh install end to end
├── docs/                      # this documentation set
├── plans/                     # frontend-backend-split.md and its companions
├── clients/ts/                # generated TypeScript client package
├── frontend/                  # Phase 0 React SPA spike, not deployed today
├── test/e2e/                  # Playwright suite
├── LICENSE                    # GPL-3.0
├── README.md                  # short index
└── backend/
    ├── main.go                # entry point, router setup
    ├── api/                   # huma-registered /api/v1 operations
    ├── apitypes/              # shared API types, incl. BasePath
    ├── cmd/openapi/           # OpenAPI spec generator
    ├── config/                # env-based config loader
    ├── db/                    # SQL queries and schema migrations
    ├── handlers/              # HTTP handlers (one file per area)
    ├── middleware/            # auth, security headers, CSRF, real-IP
    ├── models/                # data structs
    ├── security/              # password hashing, rate limiting, CSRF tokens
    ├── stepca/                # wraps github.com/smallstep/certificates
    ├── le/                    # Let's Encrypt / ACME
    ├── openapi/               # committed openapi.json plus embed glue
    ├── templates/             # HTML templates (Go html/template)
    ├── static/                # CSS, JS, and a favicon (no other images)
    ├── scripts/               # coverage-gate.sh
    ├── testdata/              # golden files for the api/handlers test suites
    ├── Dockerfile              # multi-stage Alpine build
    ├── entrypoint.sh           # startup sequence: secrets → DB wait → provisioner → exec app
    ├── tlsbootstrap.go         # in-process root CA trust and UI cert issuance
    └── tlsreload.go            # hot-reloading TLS cert reloader (mtime-based, zero-restart)
```

See also: [docs/architecture.md](architecture.md), [docs/security.md](security.md), [docs/deployment.md](deployment.md).
