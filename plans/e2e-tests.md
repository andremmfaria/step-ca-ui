# End-to-End Test Specification: step-ca-ui

Status: 2026-08-10. Verified against `step-ui-go/` at `e53236f`.

## 0. Using this document

### 0.1 Contents

| Section | |
|---|---|
| [1. Purpose and scope](#1-purpose-and-scope) | 1.1 Tiers · 1.2 Tests that cannot be green today · 1.3 Tier rosters |
| [2. Test environment](#2-test-environment) | 2.1 Compose stack · 2.2 Fresh vs reused volumes · 2.3 Seed credentials · 2.4 Root provisioning and `UI_TLS_MODE` · 2.5 Which `.env` keys reach the container · 2.6 Log-assertion mechanics · 2.7 Required test infrastructure · 2.8 Running a subset locally |
| [3. Test suites](#3-test-suites) | 3.0 Conventions and execution order · 3.1 Bootstrap · 3.2 Auth · 3.3 RBAC · 3.4 Certificates · 3.5 Provisioners · 3.6 History and security log · 3.7 Admin · 3.8 Backup · 3.9 Health · 3.10 UI-cert renewal · 3.11 CSRF · 3.12 Config and static · 3.13 Temporary users · 3.14 Let's Encrypt · 3.15 Notifications |
| [4. Automation and CI](#4-automation-and-ci) | 4.1 The harness, its three projects and two execution contexts · 4.2 Topology and cost · 4.3 Jobs · 4.4 Existing workflows · 4.5 Secrets · 4.6 Artifacts · 4.7 Flake policy |
| [5. Traceability](#5-traceability) | 5.1 Acceptance criteria · 5.2 Risk register · 5.3 Coverage by area · 5.4 Source file to test |
| [6. Application findings](#6-application-findings) | V1 to V12, all closed on 2026-08-10, and what the suite asserts about each |
| [Appendix A](#appendix-a-test-index) | Test index, all 78, sorted by ID |
| [Appendix B](#appendix-b-workflow-file) | The workflow file |

### 0.2 Where to start

| If you are | Read first | Then |
|---|---|---|
| building the harness | 2.7 Required test infrastructure | 3.0 Conventions and execution order |
| triaging a red check | 1.3 Tier rosters, to find which job ran the test | 4.6 Artifacts, for what the run collected |
| reviewing coverage | 5.3 Coverage by area | 5.2 Risk register, for the partial discharges |
| about to push | 2.8 Running a subset locally | 1.2 Tests that cannot be green today |

### 0.3 Commands

| Command | Purpose |
|---|---|
| `make setup` | generate `secrets/postgres_password`, `secrets/secret_key`, `secrets/ca_password`. `FORCE=1` regenerates |
| `docker compose up -d --build` | bring up the stock stack |
| `make e2e-fresh` | `down -v` then `up -d --wait` |
| `make e2e-install` | `npm ci` in `test/e2e`, plus `npx playwright install chromium` for a local run |
| `make e2e-quick` | the pre-push subset, Section 2.8 |
| `make e2e-main` | the `api` then the `ui` project against the long-lived stack, in the Section 3.0.3 order |
| `npx playwright test --project=api` | one project on its own, from inside `test/e2e` |
| `npx playwright test -g 'E2E-CERT-05'` | one test by ID, since every title begins with its ID |
| `npx playwright show-report` | open the HTML report from the last local run |
| `npx playwright show-trace <trace.zip>` | inspect a failure's trace, including request and response bodies |
| `make e2e-restart-ui` | restart `step-ui`, which clears both process-local rate limiters |
| `make e2e-reset-ssl` | remove the `step-ui-ssl` volume only |
| `make e2e-seed-history N` | insert exactly N synthetic `cert_history` rows |
| `./test/e2e/scenario.sh <scenario>` | run one bootstrap scenario, driving the `infra` project against its own disposable stack |
| `./test/e2e/collect.sh <dir>` | collect the artifact set in Section 4.6, redacting as it writes |
| `./test/e2e/assert-redacted.sh <dir>` | E2E-SEC-04's canary sweep over a collected artifact |

## 1. Purpose and scope

This is the project's end-to-end suite. It validates the deployed `step-ca-ui` stack (postgres + step-ca + step-ui, `docker-compose.yml`) as a black box: real HTTP requests against a running container set, real TLS handshakes, real files on the compose volumes. A test belongs here when its property is only observable against a running stack.

The suite was seeded from the step CLI to `ca` library swap plan, removed on 2026-09-02 once the swap had fully landed in `backend/stepca/` (see `plans/frontend-backend-split.md`, Section 12). Section 5 traces which tests discharge which of that plan's criteria and risks.

**What e2e covers that unit tests do not:**
- The `UI_TLS_MODE` bootstrap switch (`self-signed` / `provided` / `stepca`) crossed with all four root-provisioning modes, running against a real `step-ca` container, including retry-then-fallback timing (`caBootstrapRetries=30`, `caBootstrapInterval=1s`, `stepca/bootstrap.go`, `tlsbootstrap.go`).
- The deliberate startup-fatal paths. Process-exit behaviour is black-box by definition and is structurally untestable anywhere else. "Never fatal on CA failure" only means something if the intended fatals are pinned as contrast.
- The background UI-cert renewal goroutine (`startUICertRenewer`) picking up a renewed cert with zero downtime via `certReloader` (`tlsreload.go`).
- Full request/response round-trips through chi middleware, meaning CSRF, session cookies and RBAC as an integrated stack rather than handler by handler.
- Certificate issuance producing material a real `openssl` and a real TLS stack can parse, with SANs, duration and key type matching what was requested. This is Risk R4's "what would have to be true for this plan to fail silently": the locally-built CSR's shape must match `step ca certificate`'s, not merely "issuance succeeded".
- Revocation actually rejected CA-side on reuse (Risk R7). `Revoke()` returning `nil` is not proof.
- Fresh-volume `docker compose up` behaviour, which unit tests structurally cannot exercise.
- The full configuration-switch and response-header matrix. Eleven environment keys change runtime behaviour, and a response header is only observable on the wire. Section 3.12 enumerates all eleven and says which test covers each.
- Let's Encrypt (`le/`, `handlers/le_renewer.go`). Eleven routes behind a manager gate, a second certificate-issuance backend with its own downloads and its own renewal goroutine. It is in scope, behind an env flag with skip-with-reason, on the same discipline this suite applies to OIDC.

**What e2e does NOT cover** (already gated elsewhere, do not duplicate):
- `go build`/`go vet`/`golangci-lint`/`go test ./...`/`coverage-gate.sh`, plus `gosec`, `govulncheck`, `trivy` and `gitleaks`. These are CI, not this suite. The e2e jobs never compile the app.
- Unit-level CA-client behaviour: timeout wrapping, error-string parity, `FakeCA`-based handler tests (`stepca/*_test.go`, `handlers/*_test.go`).
- The `grep -rn 'exec.Command'` / `grep -rn '"step"'` code-hygiene sweeps. Static checks, not runtime behaviour.

**Delegated elsewhere.** Some of these are owed: no such unit test exists yet, and the row says so rather than implying one is already in place.

| Property | Where it is asserted |
|---|---|
| The notification worker's 24h ticker | owed: a unit test with an injected clock. No test file exercises `StartNotificationWorker`'s ticker today |
| `RetryAfter` and `LockoutMessage` against `LimitWindow` | `security/security_test.go`: `TestRateLimiterRetryAfter`, `TestLockoutMessageNamesLimitWindow` |
| Session idle-timeout and absolute-lifetime expiry | `middleware/middleware_test.go`, all three cases |
| The size of `adminConsoleCommands` | unit assertion over the slice, not a count of rendered `<option>` elements |
| The schema-migration upgrade path | owed: an integration test with fixture dumps. `db/integration_test.go` today has only `TestIntegration_InitSchema_Idempotent`, which checks that re-running the schema is a no-op, not that an old-schema fixture upgrades cleanly |
| `getCertKeyType` branch coverage | owed: a unit test. No test currently calls this function |
| `/profile` theme and `update_info` field handling | owed: a unit test. No test currently exercises either field |
| Render-only admin pages | one parameterised render-smoke sweep |

### 1.1 Tiers

Every test carries a tier label naming its tier and its CI job.

**Selection rule:** a test runs in the PR tier if and only if its worst-case wall clock is under 60 seconds **of test-controlled time**, excluding bounded container start and healthcheck waits, and it contains no wait on a real-time clock longer than that budget. Everything else is nightly.

The exclusion is what makes the rule usable. A bootstrap test that waits 120 seconds for a container healthcheck is spending the stack's time, not the test's, and every job pays that cost once regardless of how many tests it then runs. Three tests carry a real-time wait inside the budget and say so in their own entries: E2E-BOOT-02 and E2E-BOOT-05 each assert a bounded ~30-second retry loop, and E2E-TEMP-01 polls the temp-user expiry ticker every 5s bounded at 90s. In all three the wait is the property under test rather than an incidental delay: the retry loops are the timing behaviour being asserted, and the ticker's phase relative to when its condition is set up is arbitrary, so the poll has to be bounded well past the ticker's own period rather than timed to it. E2E-TEMP-01's entry keeps its 90s bound for that reason.

The PR tier blocks both pull requests and pushes to `main`. There is no main-only tier, because a main-only tier makes `main` the first place a whole class of failure is observed.

**Oracle pairs.** Three pairs of tests are individually near-worthless and meaningful only together, because each half passes against a stub that the other half would catch: E2E-ADM-02 with E2E-ADM-03, E2E-HLTH-02 with E2E-HLTH-03, and E2E-PROV-01 with E2E-PROV-02. Each entry carries the tag and nothing more. Flake triage must not retire one half of a pair without the other.

### 1.2 Tests that cannot be green today

**Every application finding in Section 6 is closed as of 2026-08-10.** Two tests remain blocked, and neither is waiting on a security defect.

| Test | Blocker | Unblocked by |
|---|---|---|
| E2E-SEC-06 | `Cache-Control: no-store` is absent on all five routes | the decided `Cache-Control` prerequisite in Section 2.7.4, a global one-line addition to `mw.SecurityHeaders` |
| E2E-RENEW-01 | `uiIssueDuration` is a package constant | the decided `UI_CERT_DURATION` prerequisite in Section 2.7.4. If it fails to land, delete the test |

**E2E-SEC-06 carries an explicit skip-until-fixed contract, on the same discipline Section 3.0.2 applies to a flagged stack.** It stays rostered in `e2e-main` (Section 1.3) and in the Section 3.0.3 order, but calls `test.skip()` with the reason `Cache-Control: no-store not yet added, see Section 1.2` until the header lands, rather than running and failing. `e2e-gate` is the suite's only required check (Section 4.2), so an unconditionally red PR-tier test in that job would make the required check permanently red; a skip keeps the gate meaningful while the header is missing, and the entry stops skipping itself the day the fix ships. E2E-RENEW-01 does not need the same treatment, since it runs nightly, outside the required gate.

Nothing else in Section 3 is expected red. In particular E2E-AUTH-11 through E2E-AUTH-15, E2E-TEMP-01, E2E-TEMP-02, E2E-CERT-12, E2E-CFG-02, E2E-ADM-08 and E2E-LE-04 all assert fixed behaviour and are expected green.

### 1.3 Tier rosters

**PR tier, job `e2e-main`**, projects `api` then `ui`, one long-lived stack, in the Section 3.0.3 order: E2E-AUTH-01 to E2E-AUTH-07, E2E-AUTH-11, E2E-AUTH-12, E2E-AUTH-14, E2E-AUTH-15, E2E-RBAC-01, E2E-RBAC-02, E2E-CERT-01 to E2E-CERT-13, E2E-PROV-01, E2E-PROV-02, E2E-HIST-01 to E2E-HIST-03, E2E-SEC-01 to E2E-SEC-06, E2E-ADM-01 to E2E-ADM-05, E2E-ADM-07, E2E-ADM-08, E2E-BAK-01, E2E-HLTH-01 to E2E-HLTH-06, E2E-CSRF-01, E2E-CSRF-05, E2E-CFG-01, E2E-STATIC-01, E2E-TEMP-01, E2E-TEMP-02.

**PR tier, job `e2e-bootstrap`**, project `infra`, one disposable stack per scenario:

| Scenario | Tests |
|---|---|
| `selfsigned` | E2E-BOOT-04 |
| `provided` | E2E-BOOT-03 |
| `ca-down` | E2E-BOOT-02, E2E-BOOT-09 |
| `fingerprint` | E2E-BOOT-01, E2E-BOOT-05, E2E-BOOT-06, in that order |
| `fatals` | E2E-BOOT-07 |

**Nightly:**

| Leg | Tests |
|---|---|
| `renew` | E2E-RENEW-01 |
| `oidc-mail` | E2E-AUTH-08, E2E-AUTH-09, E2E-AUTH-13, E2E-AUTH-16, E2E-CFG-02, including its merged `TRUST_PROXY=false` phase (Section 3.12), E2E-NOTIF-01, and E2E-CFG-01's `LOCAL_LOGIN_ENABLED` and `USE_HTTPS` rows. The leg composes `compose.e2e-oidc.yml` and `compose.e2e-mail.yml` for the bulk of its tests, and additionally applies `compose.e2e-config.yml` for the `USE_HTTPS` row, which is the only test in the leg that needs it |
| `le` | E2E-LE-01 to E2E-LE-04, plus E2E-RBAC-01's manager and admin `/le/*` rows, which cannot run anywhere else (Section 3.3) |
| `bootstrap-extra` | E2E-BOOT-08, E2E-BAK-03, and E2E-CERT-01's full sixteen-combination cross, hosted here rather than in a dedicated leg (Section 3.4) |

**Mailpit hygiene within this leg.** E2E-AUTH-09 and E2E-NOTIF-01 share one mailpit instance and each asserts "exactly one message arrives." Every mail-consuming test in this leg purges mailpit's inbox, or records its current message count as a baseline, immediately before the step that expects delivery, and diffs against that baseline rather than asserting an absolute inbox size. E2E-AUTH-09 runs before E2E-NOTIF-01 in this leg: both configure SMTP through `POST /admin/notifications` as their own precondition, so E2E-NOTIF-01's settings POST is the one left in effect, and neither depends on the other's configuration surviving.

**Intra-leg order and env restoration (`oidc-mail`).** E2E-AUTH-08, E2E-AUTH-09, E2E-AUTH-13, E2E-AUTH-16 and E2E-NOTIF-01 run first, against the leg's stock OIDC-plus-mail override with no other env change beyond E2E-AUTH-09's own `PUBLIC_BASE_URL` phase, which that entry's own teardown restores. E2E-CFG-02 runs next, starting from `TRUST_PROXY=true` with an untrusted `TRUSTED_PROXY_CIDRS`, widening it mid-test, and ending its merged final phase on `TRUST_PROXY=false`; its own teardown restores both keys to the leg's starting values. E2E-CFG-01's `LOCAL_LOGIN_ENABLED` row runs next, starting from the leg's stock `LOCAL_LOGIN_ENABLED=true` and restoring it before handing off. E2E-CFG-01's `USE_HTTPS` row runs last and is terminal for the leg, since it leaves `step-ui`'s own healthcheck failing by design (Section 3.12); nothing in the leg runs after it, so it carries no restore step. Every row that flips an environment key states its own starting value and its own restore step in its own entry, on top of this order. The leg's stack starts from a stock `.env` plus the compose overrides named above, with no other pre-existing edit.

## 2. Test environment

### 2.1 Compose stack

Standard stack: `docker-compose.yml` with `postgres` (16-alpine), `step-ca` (`smallstep/step-ca:0.30.2`), `step-ui` (built from `step-ui-go/Dockerfile`). Bring up with:

```
cp .env.example .env
make setup                    # generates secrets/postgres_password, secrets/secret_key, secrets/ca_password
docker compose up -d --build
```

`make setup` only creates `secrets/*` files if absent (`FORCE=1` to regenerate). `secrets/ca_password` is read by `step-ca` at first init (`DOCKER_STEPCA_INIT_PASSWORD_FILE`) and by `step-ui` (`PROVISIONER_PASSWORD_FILE`) to write `PASSWORD_FILE` (`step-ui-go/entrypoint.sh`).

Six named volumes exist: `postgres-data`, `step-ca-data`, `step-ui-certs`, `step-ui-ssl`, `step-ui-data`, `step-ui-uploads` (`docker-compose.yml:142-148`). Five of them are backup components and E2E-BAK-01 asserts one tarball each: `step-ca-data`, `step-ui-data`, `step-ui-certs`, `step-ui-uploads`, plus `postgres-data` as a SQL dump rather than a tarball. `step-ui-ssl` is deliberately excluded from the bundle, since the UI's serving certificate is reissued on boot rather than restored.

`step-ca-data` is additionally mounted into `step-ui` **read-only** at `/home/step` (`docker-compose.yml:71`). Section 2.4 covers the consequences.

### 2.2 Fresh-volume vs reused-volume scenarios

Two distinct starting states. The bootstrap tests in Section 3.1 need the fresh one, most of the rest do not.

- **Fresh volumes** (`docker compose down -v` first, or a never-started stack): all six volumes empty. `step-ca`'s `DOCKER_STEPCA_INIT_*` env vars (name, DNS names, provisioner) take effect only on this path, since they are consumed by `step ca init` at first boot.
- **Reused volumes**: `step-ca-data` already holds `ca.json` plus root and intermediate certs, and `postgres-data` already holds a populated `users` table, so the `STEPUI_ADMIN_PASSWORD` seeding in Section 2.3 is skipped.

**The TLS duration claims are not part of that split.** `scripts/step-ca-bootstrap.sh` re-patches `ca.json` on **every** step-ca start: it reads `STEPCA_DEFAULT_TLS_CERT_DURATION` and `STEPCA_MAX_TLS_CERT_DURATION` from the environment and rewrites `defaultTLSCertDuration`/`maxTLSCertDuration` on the named provisioner via `jq` unconditionally (`scripts/step-ca-bootstrap.sh:26-60`), whether or not the volume is fresh. Changing either duration therefore requires `step-ca` to be **recreated** (`docker compose up -d step-ca`), not a fresh volume. `docker compose restart` reuses the container's existing environment and does not pick up an `.env` edit, so `restart` alone leaves the old duration in place; `up -d` re-reads `.env` and recreates the container with it. This materially cheapens every duration-boundary test: E2E-CERT-11 lowers `STEPCA_MAX_TLS_CERT_DURATION` and restores it with two recreates rather than two 90-second `down -v` cycles.

The same script also relaxes `$STEPPATH/certs` to 0755 and the `*.crt` files to 0644 on every start (`:20-24`), which is what makes the read-only volume share readable by step-ui's non-root uid at all.

Tear down fresh-volume tests with `docker compose down -v`. Reused-volume tests need only `docker compose down`, or targeted cleanup of a specific row and file. Two bootstrap tests need less than either: E2E-BOOT-03 and E2E-BOOT-04 care only about `/opt/step-ui/ssl`, so `docker volume rm <project>_step-ui-ssl` is sufficient and is roughly two orders of magnitude faster than a full `down -v`, and it avoids re-seeding the admin user.

### 2.3 Seed credentials flow

`db/schema.go` seeds the initial admin **only when the `users` table is empty**, and **fails startup (`log.Fatal`) if `STEPUI_ADMIN_PASSWORD` is unset at that point** (`schema.go:145-162`, `resolveAdminPassword`, `schema.go:187-195`):

```go
if pw == "" {
    return "", fmt.Errorf("[FATAL] No admin user exists and STEPUI_ADMIN_PASSWORD is not set. ...")
}
```

There is no default password. Under the stock `restart: unless-stopped` policy, a fresh volume with `STEPUI_ADMIN_PASSWORD` unset crash-loops `step-ui`, visible as the container never passing its healthcheck and `docker compose ps` showing it restarting. E2E-BOOT-07 case (b) runs under the `fatals` scenario's own `restart: "no"` override instead, so it observes one non-zero exit and an `exited` container rather than this crash loop.

Every fresh-volume test must set `STEPUI_ADMIN_PASSWORD` in `.env` before `docker compose up`, and both CI scenario drivers do. Seeded user: `username=admin`, `role=admin`, password equal to the env value.

In **local development**, remove `STEPUI_ADMIN_PASSWORD` from `.env` after the first successful login, since it is not re-read once a user row exists. In **CI** it stays in `.env` for the life of the job, because jobs are ephemeral and a later step may recreate the stack. The collector redacts it before any artifact is written (Section 4.6), and E2E-SEC-04 asserts that the redaction worked.

### 2.4 Root provisioning and the `UI_TLS_MODE` matrix

There are **four** root-provisioning modes, not three, and they are tried in a fixed order during `main`'s bootstrap block:

| Order | Mode | Trigger | Mechanism |
|---|---|---|---|
| 1 | Inline PEM | `CA_ROOT_CERT_PEM` non-empty | `writeInlineRootCert` writes it to `cfg.RootCert` and logs `wrote root CA certificate from CA_ROOT_CERT_PEM` (`tlsbootstrap.go`, `writeInlineRootCert`) |
| 2 | Pre-existing file | `cfg.RootCert` exists and is non-empty | `ensureRootCert` early-returns without touching the network. This is what the stock `step-ca-data:/home/step:ro` mount produces |
| 3 | Fingerprint fetch | no root file, `CA_FINGERPRINT` set | `stepca.FetchRootByFingerprint` retried `caBootstrapRetries` times at `caBootstrapInterval`, then `root CA certificate fetched and verified` or, on exhaustion, `could not fetch root CA certificate after retries — continuing without it` |
| 4 | None | no root file, no fingerprint | `ensureRootCert` returns `nil` immediately. `stepca.New` then fails, because `ca.WithRootFile` reads the file eagerly inside `ca.NewClient`, so `caClient` stays nil for the remainder of `main` |

`UI_TLS_MODE` then selects what happens to the UI's own serving certificate (`ensureUICert`):

| `UI_TLS_MODE` | CA client | Expected UI cert source |
|---|---|---|
| unset / `self-signed` | any | `generateSelfSignedCert` (EC P-256, 10 years) only if `cfg.SSLCert` is absent, otherwise a no-op |
| `provided` | any | No-op. The operator supplies the cert out of band |
| `stepca` | nil | Immediate self-signed fallback, logging `UI_TLS_MODE=stepca but no CA client is available — falling back to self-signed` |
| `stepca` | non-nil, CA answering | `obtaining UI leaf certificate from step-ca`, then `UI leaf certificate obtained` |
| `stepca` | non-nil, CA refusing | 30 attempts at 1s, then `step-ca certificate issuance failed after retries — falling back to self-signed` |
| `stepca` | non-nil, context expired mid-loop | `UI cert issuance aborted by context — falling back to self-signed` |

Three of those six rows end in a self-signed certificate, and all three log a message containing the substring `falling back to self-signed` (`tlsbootstrap.go:214`, `:230`, `:235`). **No test may treat that substring as a positive identification of a path.** Self-signed is the terminal state of every failure path and also the default-branch outcome. Match the exact full message, and pair it with the message that uniquely identifies the path, which for the retry loop is `obtaining UI leaf certificate from step-ca`. Asserting the substring's *count is zero* is a different and legitimate use, and E2E-BOOT-04 does exactly that.

`SSL_CERT` and `SSL_KEY` are **not** environment variables. `config.Load` hardcodes `/opt/step-ui/ssl/server.crt` and `/opt/step-ui/ssl/server.key` (`config/config.go:93-94`), with no `getEnv` for either. Tests that need a pre-seeded UI certificate must write to those two fixed paths inside the `step-ui-ssl` volume.

### 2.5 Which `.env` keys actually reach the container

`docker-compose.yml`'s `step-ui` environment block is the whole interface. A key that is absent from that block, or hardcoded in it, cannot be set from `.env`, and the edit fails silently rather than erroring.

| Key | Reaches the container | How a test sets it |
|---|---|---|
| `UI_TLS_MODE`, `CA_FINGERPRINT`, `CA_ROOT_CERT_PEM`, `UI_HOSTNAME`, `HOST_IP` | yes | `.env` |
| `UI_HTTPS_PORT` | **host-side only.** It sets the published host port in the `ports:` mapping (`docker-compose.yml:65`) and never appears in the `step-ui` environment block, so it changes what a host-context test dials, not anything the process reads | `.env`, consumed by tests as the port to connect to |
| `SESSION_SECURE`, `ENABLE_HSTS`, `PUBLIC_BASE_URL`, `STEPUI_ADMIN_PASSWORD` | yes | `.env` |
| `PROVISIONER`, `TZ`, `STEP_CA_IMAGE`, `STEPCA_DEFAULT_TLS_CERT_DURATION`, `STEPCA_MAX_TLS_CERT_DURATION` | yes | `.env`, then recreate the affected service (`docker compose up -d <service>`). `restart` reuses the existing container's environment and does not apply an `.env` edit |
| `SECRET_KEY_FILE` | yes, but hardcoded to `/run/secrets/secret_key` (`:100`) | not settable |
| `SECRET_KEY` | **no** key exists in the block at all | write the value into `secrets/secret_key` |
| `ROOT_CERT` | **no**, literal at `:86` | `compose.e2e-fingerprint.yml` |
| `CA_URL` | **no**, literal at `:85` | compose override |
| `PASSWORD_FILE` | **no**, literal at `:88` | compose override |
| all eleven `OIDC_*` | **no**, absent | `compose.e2e-oidc.yml` |
| `TRUST_PROXY`, `TRUSTED_PROXY_CIDRS`, `LOCAL_LOGIN_ENABLED` | **no**, absent | `compose.e2e-oidc.yml`. `TRUST_PROXY=true` without a usable `TRUSTED_PROXY_CIDRS` is fatal at boot |
| `USE_HTTPS` | **no**, absent | `compose.e2e-config.yml` |
| `ALLOWED_DOMAIN_SUFFIXES` | **no**, absent from the `step-ui` environment block (`docker-compose.yml:76-109`); the key exists only as a commented-out example at `.env.example:210`. Setting it in `.env` alone is the silent no-op this section warns about | `compose.e2e-config.yml`, then recreate `step-ui`. Comma-separated; empty means unrestricted |
| `UI_CERT_DURATION` | **not yet.** Owed by the Section 2.7.4 prerequisite. Once `config.Load` reads it, it needs the same treatment as `ALLOWED_DOMAIN_SUFFIXES`: a passthrough added to `compose.e2e-config.yml` | `compose.e2e-config.yml`, once the prerequisite lands |
| `SSL_CERT`, `SSL_KEY` | **no**, and not env vars in the code either | write to the fixed paths in `step-ui-ssl` |

**`.env.example` is stale in two places** and neither is a code defect:

| `.env.example` | Claim | Reality |
|---|---|---|
| `:31` | `STEPUI_ADMIN_PASSWORD` "If left empty, defaults to `Admin123!`" | there is no default, and startup fatals (Section 2.3) |
| `:124-125` | `SSL_CERT` and `SSL_KEY` are settable | both are hardcoded in `config.Load` |

### 2.6 Log-assertion mechanics

There is no `slog.SetDefault` anywhere in the tree, so the application uses slog's default handler: **INFO and above only**, to stderr, in the form `2026/08/10 10:00:00 INFO msg key=value`.

Consequences that bind every log-based assertion in this document:

- Every `slog.Debug` call is invisible. That includes both retry-progress lines (`tlsbootstrap.go:95` and `:227`), so a retry **count** can never be read directly. It can only be inferred from the second-resolution timestamps on the INFO/WARN lines bracketing the loop, which is why `--timestamps` is mandatory on every log capture.
- `grep -F` against an exact message string is the correct matching mode. Substring matching is not, because at least one message fragment (`falling back to self-signed`) is emitted from three distinct code paths.
- Any assertion of the form "this log line never appeared" is unfalsifiable on its own. It passes identically when the logger is misconfigured, when the pattern could never match anything, and when the code that would have logged it was deleted. Every such assertion in this document is paired with a **positive control** in the same run: an action that provably produces a matching line, taken immediately before or after the negative case, with a recorded log offset between them. If the positive control does not fire, the test fails rather than passing vacuously.

The same positive-control requirement applies to absence assertions over the database, over the CA's own request log, and over rendered pages.

**Recreates truncate `docker compose logs`.** A `docker compose up -d <service>` recreate replaces the container, and `docker compose logs` only ever shows the current container's own log, not any predecessor's. Several tests recreate `step-ui` mid-run (E2E-CERT-11, E2E-CERT-12, E2E-CFG-01, E2E-CFG-02, among others), so a full-history log sweep taken after any of them has already lost everything logged before the most recent recreate. The rule: **before every `step-ui` recreate, the harness appends the container's current log to a cumulative capture file**, then lets the recreate proceed. Any test or script that needs the run's whole log history reads that cumulative capture file rather than calling `docker compose logs` directly. E2E-SEC-03's full-log sweep and `collect.sh` (Section 4.6) both depend on this and are the two places it matters; this is the one place the rule is stated.

### 2.7 Required test infrastructure

Nothing in Section 3 is runnable until this list is built. Every item is referenced by at least one test, and every override a test names appears here.

#### 2.7.1 Compose overrides

| Override | What it changes | Unblocks |
|---|---|---|
| `compose.e2e-image.yml` | replaces `step-ui`'s `build:` block with `image: step-ca-ui:e2e` | every CI job. Without it each job pays its own `up -d --build`. It does not remove `build:` from the merged config, since compose overrides only add or replace keys by default; that is harmless here because CI never passes `--build`, so the unused `build:` block is never evaluated |
| `compose.e2e-fingerprint.yml` | drops the `step-ca-data:/home/step:ro` mount with `volumes: !override [...]` (compose's merge-override tag; a plain `volumes:` key would be merged with the base list rather than replacing it, and the read-only mount would survive), sets `ROOT_CERT: /opt/step-ui/data/root_ca.crt` on a writable volume | E2E-BOOT-01, E2E-BOOT-05, E2E-BOOT-06, E2E-BOOT-09 |
| `compose.e2e-nodeps.yml` | removes `step-ui`'s `depends_on` conditions on `step-ca` and `postgres` with `depends_on: !reset` (compose ≥ v2.24; a plain `depends_on:` key merges rather than clears). Where the pinned compose version predates `!reset`, the scenario driver runs `docker compose up --no-deps step-ui` instead | E2E-BOOT-02, E2E-BOOT-07 case (c) |
| `compose.e2e-oidc.yml` | adds `ghcr.io/navikt/mock-oauth2-server` plus a `mock-oidc-ready` readiness container, since the upstream image ships no healthcheck **and is distroless**, so the `wget` probe an earlier revision specified could never run and left `step-ui` waiting on it forever, and passes through all eleven `OIDC_*` keys plus `LOCAL_LOGIN_ENABLED`, `TRUST_PROXY` and `TRUSTED_PROXY_CIDRS`, gated on `E2E_OIDC_ENABLED=1` | E2E-AUTH-08, E2E-AUTH-13, E2E-AUTH-16, E2E-CFG-01's `LOCAL_LOGIN_ENABLED` row, E2E-CFG-02 |
| `compose.e2e-mail.yml` | adds `axllent/mailpit`, SMTP on 1025 and HTTP API on 8025, gated on `E2E_MAIL_ENABLED=1` | E2E-AUTH-09, E2E-NOTIF-01 |
| `compose.e2e-le.yml` | adds a local ACME server (`pebble` or equivalent) and an HTTP-01 challenge path routed to `step-ui`'s port 80, gated on `E2E_LE_ENABLED=1`. Passes through the `LE_ACME_DIRECTORY_URL` prerequisite from Section 2.7.4 pointed at the local server, plus `LEGO_CA_CERTIFICATES` so `lego`'s HTTP client trusts the local server's self-signed root | E2E-LE-01 through E2E-LE-04, and the manager and admin `/le/*` rows of E2E-RBAC-01. The viewer `403` half of those rows needs no override, since `RequireRole` rejects before any LE-specific code runs, and runs unconditionally in the PR-tier entry (Section 3.3) |
| `compose.e2e-config.yml` | passes through `USE_HTTPS`, `ALLOWED_DOMAIN_SUFFIXES` and, once the Section 2.7.4 prerequisite lands, `UI_CERT_DURATION` | E2E-CFG-01's `USE_HTTPS` row, E2E-CERT-12, E2E-RENEW-01 |

Without `compose.e2e-fingerprint.yml`, `CA_FINGERPRINT` is unreachable in this deployment: the root cert is always already present, so `ensureRootCert` early-returns and never reads the fingerprint, and even if it did the fetch would write back to a read-only mount and fail with `EROFS`. `docker-compose.yml:89-94` says as much in its own comment.

**The fingerprint override also removes `/home/step` from the container**, which disables `checkProvisionerPasswordSync` and removes the `step-ca-data` component from the backup bundle. E2E-HLTH-06 and E2E-BAK-01 must not run in that stack. This is the canonical statement of that constraint, and the two tests carry a pointer to it.

`compose.e2e-oidc.yml` must express `depends_on: service_healthy` on the mock IdP, and the override must define that healthcheck itself, since `ghcr.io/navikt/mock-oauth2-server` ships no `HEALTHCHECK` of its own. `h.initOIDC()` calls `gooidc.NewProvider` at `Handler` construction time and `log.Fatalf`s on discovery failure, so an IdP that is not yet listening prevents the whole application from starting.

#### 2.7.2 Harness and scripts

| Item | Purpose |
|---|---|
| `test/e2e` as its own Node project | Playwright Test in TypeScript, with its own `package.json`, `tsconfig.json` and `playwright.config.ts`. Kept outside both the Go module and the repo-root frontend tooling, so its dependencies never reach `govulncheck` or `trivy-fs`, both currently blocking. `@playwright/test`'s version here must match the tag on the harness image below, or the reporter and the runtime disagree |
| a pinned `mcr.microsoft.com/playwright` image | the base for the harness image below. Pinned by tag and digest, since an unpinned image changes browser version between runs. The digest is a placeholder until filled in before the workflow's first run |
| `test/e2e/Dockerfile`, the harness image | `FROM` the pinned Playwright image, adding `docker-ce-cli` and `docker-compose-plugin`, then `COPY package*.json ./` and `npm ci` before the rest of the project is copied in, so the `node_modules` tree is baked into an image layer rather than reinstalled per job. Built once by the `image` job alongside `step-ca-ui:e2e` and cached the same way. This is what lets the `api` and `ui` containers reach `docker`/`docker compose` over a mounted socket, which is the single mechanism this suite uses for every container-touching or database-touching assertion the `api`/`ui` projects need: `docker compose exec step-ui ...`, `docker compose logs ...`, `docker compose stop/start ...`, and `docker compose exec -T postgres psql ...` for database checks, rather than a second, separate mechanism for each. It is also what makes the warm-cache `npm ci` numbers in Section 4.2.1's cost table achievable: without the baked-in layer, every container's `npm ci` would pay the cold-cache cost |
| the repo root and `/var/run/docker.sock`, bind-mounted into the harness container | gives the container the compose file, `.env`, `secrets/*` and the ability to drive `docker compose` on the same stack it is a member of. `STEPUI_ADMIN_PASSWORD` and the `secrets/ca_password`/`secret_key`/`postgres_password` canaries are read directly off the mounted files rather than passed as additional environment variables, which is one fewer thing to keep in sync with Section 4.5's masking |
| `test/e2e/scenario.sh <scenario>` | one entry point per bootstrap scenario, naming a compose override and invoking the `infra` project with the scenario's grep filter. Sets `STEPUI_ADMIN_PASSWORD`, except for the `fatals` scenario's case (b) |
| `test/e2e/collect.sh <dir>` | the artifact collector, specified in Section 4.6. Redacts before it writes. Tolerates a dead or absent stack, since it also runs after a failure that killed the stack, and always emits at least a marker file, so `upload-artifact`'s `if-no-files-found: error` fails on a genuinely empty collection rather than masking the real failure behind a missing-artifact error |
| `test/e2e/assert-redacted.sh <dir>` | E2E-SEC-04's canary sweep over the collected artifact |
| an OTP library in the harness project | TOTP code generation for E2E-AUTH-04 through E2E-AUTH-07, behind the boundary-guard fixture in Section 4.1.4 |
| a QR decoder in the harness project | E2E-AUTH-04's `ui` companion decodes `GET /profile/2fa/qr` and compares it against the plaintext secret the page renders |

The `api` and `ui` projects must run **as a container on the compose network**, not on the host. `docker-compose.yml` names the network `step-net` as the compose service key but sets an explicit `name: step-network` (`docker-compose.yml:151-154`), so `step-network` is the network's actual name for anything outside the compose file itself, such as `docker run --network`; this document uses `step-net` only when referring to the compose-file key and `step-network` everywhere a command needs the runtime name. Running the harness there matters because both rate limiters key on the client IP, so a host harness is seen as the single docker gateway address and per-test rate-limit isolation is impossible. `TRUST_PROXY` is not passed through the stock compose file, so `X-Forwarded-For` cannot namespace it either. A container on `step-network` also resolves `https://step-ca:9443` and the mock IdP issuer URL identically to the way the application resolves them, which is what makes an OIDC discovery document validate for both parties and for the browser, and it is the only place E2E-CERT-05's mTLS probe can reach step-ca directly. E2E-AUTH-02 and E2E-AUTH-03 need a second such container with its own address on the same network, since they poison whichever address they run from; Section 4.3 and Appendix B name the second `docker run`.

The `infra` project runs on the host, because it drives `docker compose` and inspects published ports. Section 4.1.2 states both contexts.

#### 2.7.3 Makefile targets

| Target | Effect |
|---|---|
| `make e2e-restart-ui` | `docker compose restart step-ui`. Both rate limiters are process-local maps, so this converts two multi-minute real-time waits into a wait of roughly 20-30 seconds to healthy again, since the healthcheck's own `start_period` is 20s (`docker-compose.yml:117-122`) before probing even begins |
| `make e2e-reset-ssl` | removes the `step-ui-ssl` volume only |
| `make e2e-seed-history N` | inserts exactly N synthetic `cert_history` rows without disturbing the real ones |
| `make e2e-fresh` | `down -v` plus `up -d --wait` |
| `make e2e-main` | runs the `api` then the `ui` project against the long-lived stack, in the order given in Section 3.0.3 |
| `make e2e-quick` | the pre-push subset, Section 2.8 |
| `make e2e-install` | `npm ci` in `test/e2e`, plus `npx playwright install chromium` for a local run outside the container |

#### 2.7.4 Application prerequisites

All three items below are decided: the change will be made, not fallen back on. Each still carries a fallback contract, stated per item, for the case where the change fails to land.

**`UI_CERT_DURATION`. Decided: make.** E2E-RENEW-01 cannot be written until the UI's own certificate duration is configurable. `uiIssueDuration` is a package constant of `8760h` (`tlsbootstrap.go:44`) which `issueUICert` passes straight into `stepca.IssueRequest.Duration` (`tlsbootstrap.go:250-256`), and the renewal sleep is two thirds of the issued validity (`nextRenewalSleep`, `tlsbootstrap.go:289-311`), so a renewal cycle is roughly 5,840 hours. The exact shape:

- `config.Load` gains `UICertDuration`, read through a new `getEnvDuration` helper alongside the existing `getEnv`/`getEnvOrFile`/`getEnvList`/`getEnvBool` family. Default `8760h`. An invalid or non-positive value falls back to the default and logs a warning, the same failure posture `getEnvBool` already uses for a bad boolean.
- `tlsbootstrap.go` drops the `uiIssueDuration` constant entirely; `issueUICert` reads `cfg.UICertDuration` in its place.
- `main.go` warns at startup when the configured value is under 10 minutes, since that is a testing cadence rather than an operational one.
- An optional floor of 1 minute in `nextRenewalSleep`, so a very short configured duration cannot drive the renewal loop into a busy spin.
- No client-side hard floor beyond that: the CA's own `validityValidator` is the enforcer of what duration is actually acceptable, not this application.

This alone is not enough to make the value reachable in a test: `docker-compose.yml`'s `step-ui` environment block has no `UI_CERT_DURATION` key either, so the compose-side half of this prerequisite is the passthrough already specified on `compose.e2e-config.yml` in Sections 2.7.1 and 2.5. The two halves must land together; landing `config.Load`'s side without the compose passthrough (or vice versa) is a silent no-op, since the container never sees the variable, or the code never reads it, and E2E-RENEW-01 would still be exercising the fallback `8760h` while believing it configured something. E2E-RENEW-01's preconditions name both halves.

Lowering `STEPCA_MAX_TLS_CERT_DURATION` is not an alternative. `validityValidator.Valid` in smallstep/certificates v0.30.2 **rejects** an over-long request with a 403 and the message `requested duration of %v is more than the authorized maximum certificate duration of %v`, rather than clamping. Against a hardcoded `8760h` request and a short maximum, every issuance attempt fails, `ensureUICert` falls back to self-signed, and the renewer then computes its sleep from the ten-year self-signed certificate and waits about 6.7 years. The job hangs until the CI timeout on every run.

**Fallback contract, if this fails to land:** E2E-RENEW-01 is deleted rather than shipped red.

**`LE_ACME_DIRECTORY_URL`, for the Let's Encrypt leg. Decided: make.** `le/lego.go:41-42` hardcodes `LEProductionCA` and, when `cfg.Staging` is set, `LEStagingCA` (`:142-144`); there is no environment override for either, and nothing in the LE settings path can be pointed at a local ACME server. Section 3.14's flagged stack cannot exist without one. The exact shape:

- `config.Load` gains `LEACMEDirectoryURL`, default the production directory URL literal (`https://acme-v02.api.letsencrypt.org/directory`), so behaviour is unchanged until an operator (or this suite) sets it.
- `le/lego.go`'s `LEConfig` gains a `DirectoryURL` field. An empty string falls back to the current constant-selection logic (`LEProductionCA`, or `LEStagingCA` when `cfg.Staging` is set).
- The three construction sites that build an `le.LEConfig{}` pass it through: `handlers/le.go:88`, `handlers/le.go:141`, `handlers/le_renewer.go:75`.
- `main.go` warns loudly at startup when the configured value is non-default.
- `AddLELog` records the directory URL on issuance start, so the value actually in effect for a given issuance is DB-visible rather than something only startup logs assert.
- Env-only. This is never a DB-backed LE setting: a manager-editable setting must not be able to repoint where issuance actually dials.

Two corrections that belong in the LE discussion wherever it appears in this document:

- `LEConfig.Staging` is dead code at runtime. Nothing under `handlers/` ever sets it; the only place it is set is `le/le_offline_test.go`. `LEStagingCA` is consequently unreachable outside that unit test.
- The fallback contract below is narrower than "every `E2E-LE-*` entry dies." Only E2E-LE-02 (issuance against a local ACME server) and E2E-LE-03 (auto-renew toggle and delete, which depends on a real issued LE certificate) actually require dialing a local ACME server, so only those two are deleted. E2E-LE-01 (dashboard and settings round-trip) and E2E-LE-04 (credential-echo canary) issue nothing; they re-home to the PR tier (`e2e-main`) instead, since nothing in either depends on `LE_ACME_DIRECTORY_URL` or `compose.e2e-le.yml`. Section 1.3's `le` nightly leg and each LE entry's preconditions carry this split.

**Fallback contract, if this fails to land:** E2E-LE-02 and E2E-LE-03 are deleted rather than shipped red; E2E-LE-01 and E2E-LE-04 move to the PR tier (`e2e-main`) and run against the stock stack, issuing nothing.

**`Cache-Control: no-store`, global. Decided: make.** E2E-SEC-06 is blocked on it (Section 1.2). The change is one `Set` call, added globally in `mw.SecurityHeaders` rather than as a per-route list: `w.Header().Set("Cache-Control", "no-store")`.

This is safe against the one other `Cache-Control` write in the tree: the static-asset handler's `Cache-Control: public, max-age=3600` (`main.go:97`) is set after the middleware chain runs and so overwrites the middleware's value on static routes, which is the intended outcome, and it is the only other place in the codebase that sets this header.

Global, not per-route, is the deliberate choice. Section 3.6's SEC-06 lists five sensitive routes, but they are not the only ones that should never be cached: QR codes, key downloads and the backup bundle are sensitive and are not in that list, and a per-route allowlist goes stale every time a new sensitive route is added without a matching entry here. A blanket `no-store` also gives the whole application the bfcache-refetch property on back-navigation, which is the desired behaviour everywhere, not just on the five named routes.

E2E-SEC-06's skip-until-fixed contract (Section 1.2) stays in force until this line actually lands; this decision records intent, not landed behaviour. Once it lands, SEC-06 must assert that the header **contains** `no-store` rather than exact-matching it, since a future addition to the same header (a longer, legacy-compatible value such as `no-store, no-cache, must-revalidate`) would still satisfy the property the test exists to check.

**Fallback contract, if this fails to land:** E2E-SEC-06 keeps its skip-until-fixed contract from Section 1.2 indefinitely, rather than being deleted; the routes it names remain uncached in this application.

#### 2.7.5 One-time calibration

Both assertion lists below were captured 2026-08-10 on a fresh stock stack (no compose override). Both are environment-dependent and neither could be derived from source; the values recorded here are what the suite encodes.

- **E2E-BAK-01's component list.** `/home/step` is mounted read-only, while step-ui runs as uid 10001. `writeDirTGZ` propagates the walk error (`handlers/backup.go:297-299`), so a permission denial aborts that component. One backup on the fresh stock stack, read straight from `manifest.json`:
  - `components`: `postgres` (`postgres-stepui.sql`, `ok`), `step-ui-data` (`step-ui-data.tgz`, `ok`), `step-ui-certs` (`step-ui-certs.tgz`, `ok`), `step-ui-uploads` (`step-ui-uploads.tgz`, `ok`).
  - `warnings`: `["step-ca-data failed: open /home/step/config: permission denied"]`.
  - The denied path is `/home/step/config`, not `/home/step/secrets`. `step-ca-data.tgz` **is present** in the bundle, it is not omitted: it contains the `certs/` subtree but not `config/`, since the walk aborts partway through and `writeDirTGZ` still flushes what it archived before the error. It is excluded from `components` (no successful component entry for it) and named only in `warnings`, present but incomplete rather than absent.
- **E2E-ADM-07's check-name list.** `preflight` assembles its list from several helpers whose row counts are not fixed in source. Captured on the same fresh stock stack, in order:
  1. `PostgreSQL`
  2. `Step-CA API`
  3. `Root CA certificate`
  4. `Intermediate CA certificate`
  5. `Provisioner password file`
  6. `UI TLS certificate`
  7. `UI TLS private key`
  8. `Issued certificates directory`
  9. `Upload directory`
  10. `CA config`
  11. `Root CA integrity`
  12. `Intermediate CA integrity`
  13. `Full chain`
  14. `Provisioner password sync`
  15. `step-ca image pin`
  16. `Disk space`
  17. `Disk space`
  18. `Disk space`
  19. `Session cookie`

  On this stack, `CA config` and `Provisioner password sync` report `warn`; every other row reports `ok`. Both warnings trace to the same cause: `ca.json` is unreadable under the read-only mount's permissions. `checkCAConfig` returns early on that `EACCES`, so whatever downstream duration rows it would otherwise contribute never render on this stack; the 19-row list above is what actually appears, not a superset with extra rows suppressed. Rows 16 to 18 share the literal name `Disk space` and are distinguishable only by their detail text (which filesystem each checks), never by name; any assertion against this list must match by position or by detail text on those three rows, not by a name lookup.

#### 2.7.6 Deliberately not required

**A cookie-minting helper.** Forging a session cookie with backdated `session_created_at` and `last_activity` values would mean reimplementing Go's `securecookie` HMAC and AES encoding in TypeScript, and keeping that reimplementation correct against a dependency this suite does not control. It is not worth it, and nothing needs it. The idle and absolute lifetime checks it would have served are asserted directly in `middleware/middleware_test.go` (Section 1's delegation table), and every session-revocation property in the suite now uses a **real** cookie against a real `session_epoch` bump: E2E-AUTH-12 captures one, and E2E-AUTH-14, E2E-AUTH-15 and E2E-TEMP-02 watch a live session stop working. A forged cookie would be a weaker oracle than the ones already in place.

**`docker compose cp` for E2E-CERT-07:** the test plants its material through `/issue` instead.

**Any `apk add` inside the runtime image:** it runs as `USER stepui` (uid 10001, `Dockerfile:48`) so `apk add` cannot succeed, and `openssl` is already installed (`Dockerfile:29`).

### 2.8 Running a subset locally

`make e2e-quick` is the pre-push minimum. It runs `npx playwright test --project=api` with a grep filter over the IDs below, against the stock stack with no override. It needs no mock IdP, no mail catcher, no fresh volumes and no browser, and takes about **two minutes** after the stack is healthy.

Run `make e2e-install` once first. Locally the `api` project can run from the host rather than from the Playwright container, because none of the tests below depends on per-test rate-limiter isolation. Anything from Section 3.2's lockout pair, or from E2E-CFG-02, needs the container and therefore the full `make e2e-main`.

| Included | Why |
|---|---|
| E2E-AUTH-01, E2E-AUTH-11 | login and logout work at all. Everything else depends on them |
| E2E-CSRF-01 | the router-derived sweep. Catches a new POST route with no CSRF gate |
| E2E-RBAC-01, E2E-RBAC-02 | the route-by-role matrix, which is the fastest broad regression signal in the suite |
| E2E-CERT-01's `server` row at EC P-256, E2E-CERT-09 | one real issuance and one real download |
| E2E-HLTH-02 | the `/ready` probe any orchestrator's readiness gate depends on, without stopping anything. E2E-HLTH-01 is excluded here even though it shares the section: every case past its first stops step-ca or postgres, which this subset deliberately does not exercise |
| E2E-ADM-01 | the pinned library version, which a dependency bump changes |

Excluded, and why: the whole `ui` project (needs a browser and adds a download on a cold machine), everything that stops a container including E2E-HLTH-01 (needs the Section 3.0.4 barrier and adds a minute of restarts), everything on the 2FA subject (leaves state), E2E-AUTH-02 and E2E-AUTH-03 (poison the source IP for five minutes), and every test on a flagged override stack.

Bringing the stack up from cold is roughly 50 seconds to healthy. `make e2e-fresh` if the previous run left state behind, `make e2e-restart-ui` if a rate limiter is blocking you. On a failure, `npx playwright show-report` opens the trace for the failing test.

## 3. Test suites

### 3.0 Conventions, execution order and isolation

#### 3.0.1 Test-entry schema

Every entry in Section 3 uses the same fields, in this order. A field that is absent carries meaning.

| Field | When it appears |
|---|---|
| `*Tier:*` | always, plus the CI job or scenario the test belongs to |
| `*Objective:*` | only when the property under test is not evident from the title |
| `*Preconditions:*` | only when the test needs state or a stack the section preamble does not already establish |
| `*Steps:*` | always, except where a one-line entry states its own request and expectation |
| `*Assertions:*` | always. `*Assertions (current behaviour):*` marks an assertion that pins behaviour the team may decide to change, and names what would invert it |
| `*Not covered:*` | only where a reader would reasonably expect coverage that is deliberately absent |
| `*Teardown:*` | only when the test leaves state behind. Its absence means it leaves none |

A handful of entries carry additional fields beyond the core set above, each used only where it applies:

| Field | When it appears |
|---|---|
| `*Coverage:*` | only where the entry's scope is a derived list (a router sweep, a matrix) worth stating explicitly before the steps |
| `*Blocked on:*` | only where the test cannot be written at all until a named prerequisite (Section 2.7.4) lands |
| `*Matrix, ... tier.*` | only for a matrix-shaped entry, naming the table that follows it |
| `*Failure-triage note:*` | only where a flake and a genuine regression would otherwise present identically; states what the failure message must carry so the two are distinguishable without a re-run (Section 4.7) |
| `` *`ui` companion.* `` | only where the entry's `api` steps have a browser-driven counterpart that checks a property `APIRequestContext` cannot observe (Section 4.1.1) |

#### 3.0.2 Stacks

Three shapes.

1. **One long-lived stack**, brought up once and run sequentially, for most of Sections 3.2 to 3.13.
2. **Disposable single-purpose stacks** for Section 3.1 and for E2E-RENEW-01, each with its own compose override and its own volumes.
3. **Flagged override stacks** for the tests that need a service or an environment key the stock compose file does not provide: `compose.e2e-oidc.yml` (E2E-AUTH-08, E2E-AUTH-13, E2E-AUTH-16, E2E-CFG-01's `LOCAL_LOGIN_ENABLED` row, E2E-CFG-02), `compose.e2e-mail.yml` (E2E-AUTH-09, E2E-NOTIF-01), `compose.e2e-le.yml` (E2E-LE-01 to E2E-LE-04 and the manager/admin `/le/*` rows of E2E-RBAC-01), and `compose.e2e-config.yml` (E2E-CFG-01's `USE_HTTPS` row, E2E-CERT-12, E2E-RENEW-01). Section 2.7.1 defines each and names the gating flag: `E2E_OIDC_ENABLED` for the first, `E2E_MAIL_ENABLED` for the second, `E2E_LE_ENABLED` for the third.

Every test that depends on a flagged stack **skips with an explicit reason** when its flag is unset, and never silently passes. That contract binds E2E-AUTH-08, E2E-AUTH-09, E2E-AUTH-13, E2E-AUTH-16, E2E-CFG-02, E2E-NOTIF-01, E2E-LE-01 to E2E-LE-04, and the affected rows of E2E-RBAC-01 and E2E-CFG-01.

#### 3.0.3 Order within the long-lived stack

1. **Fixtures.** Create `viewer_user`, `manager_user` and the dedicated 2FA subject. Seed `cert_history` for E2E-HIST-01.
2. **E2E-AUTH-01, E2E-AUTH-11, E2E-CSRF-01, E2E-CSRF-05, E2E-RBAC-01, E2E-RBAC-02, E2E-STATIC-01, E2E-SEC-06.** These need a working login and nothing else. E2E-CSRF-05 needs two independent sessions established in the same step. E2E-AUTH-01 and E2E-AUTH-11 create and use their own throwaway users rather than the fixtures above, per the standing rule in Section 4.1.4.
3. **E2E-CERT-01 through E2E-CERT-13.** E2E-CERT-04 and E2E-CERT-05 must operate on **different** certificate ids, since revocation and renewal interfere. E2E-CERT-07 issues its own material rather than borrowing E2E-CERT-01's, because a revoked certificate keeps both its file and its row and a scan therefore finds nothing. E2E-CERT-11 recreates `step-ca` twice and E2E-CERT-12 recreates `step-ui` once, so both take the barrier. E2E-CERT-13 destroys `e2e-server-ec-p256`, so it runs after every test that reads it.
4. **E2E-PROV-01 then E2E-PROV-02**, adjacent and in that order. The first is the second's positive control.
5. **E2E-HIST-01 to E2E-HIST-03, E2E-SEC-01.**
6. **E2E-ADM-01 to E2E-ADM-05, E2E-ADM-07, E2E-ADM-08, then E2E-SEC-02.** E2E-SEC-02 asserts E2E-ADM-01's `console.run` row and must follow it.
7. **E2E-BAK-01, E2E-SEC-05.**
8. **E2E-HLTH-01 to E2E-HLTH-06**, behind the barrier.
9. **E2E-TEMP-01 then E2E-TEMP-02.** They run adjacently but each owns its own temporary user and its own session: E2E-TEMP-01's subject is expired and its one-shot credential already spent by the time E2E-TEMP-02 runs, so E2E-TEMP-02 creates a fresh temporary admin rather than reusing it.
10. **E2E-AUTH-14, E2E-AUTH-15.** Both mutate or delete users, so they follow every test that depends on the fixture users being intact, and both create and own the disposable subjects they mutate or delete rather than touching `viewer_user` or `manager_user` (Section 4.1.4).
11. **E2E-AUTH-04 to E2E-AUTH-07** on the dedicated 2FA subject, with a mandatory 2FA-disable teardown.
12. **E2E-AUTH-12**, which captures and reuses a raw cookie from a throwaway user it creates itself (Section 4.1.4's fixture contract), not the admin fixture.
13. **E2E-CFG-01**, second to last. Its `SESSION_SECURE=false` case contradicts the job-level `SESSION_SECURE=true` and requires a recreate in each direction, so it runs where nothing after it depends on the original setting. Its `LOCAL_LOGIN_ENABLED` and `USE_HTTPS` rows skip here and run in the nightly `oidc-mail` leg.
14. **E2E-SEC-03**, then E2E-SEC-04 as part of artifact collection.
15. **E2E-AUTH-02 and E2E-AUTH-03 last**, on their own harness address, because they poison it for roughly five minutes, and against a dedicated throwaway lockout user rather than the admin or manager fixtures (Section 3.2).

#### 3.0.4 The stop-a-service barrier

Every test that stops, restarts or kills a container runs behind a shared **start-and-wait-healthy barrier**, never in parallel with issuance, and must restore the service and wait for healthy before releasing it. This is the canonical list, and the individual tests carry only the tag:

E2E-PROV-02, E2E-ADM-03, E2E-CERT-11, E2E-CERT-12, E2E-CFG-01, E2E-CFG-02 (restarts `step-ui` between header variants and recreates it before its widened-CIDR phase), E2E-HLTH-01, E2E-HLTH-03, E2E-HLTH-04, E2E-HLTH-05, E2E-HLTH-06, E2E-AUTH-09 (which restarts `step-ui` to clear its reset budget), and E2E-AUTH-03's teardown fallback.

#### 3.0.5 Log-absence isolation

Tests that read the CA's log for an absence need a per-test `--since` marker or a recorded line offset, never a whole-log grep. That covers E2E-CERT-03 and the `/issue` and `/revoke/{id}` rows of E2E-CSRF-01.

The same rule covers a log gate that waits for a **new occurrence** of a line another test may already have produced, not only an absence. A whole-log `grep` for a line a prior test already emitted would pass immediately without waiting for this run's own occurrence. E2E-TEMP-02 is this case: E2E-TEMP-01 already emits `temp users expired` earlier in the Section 3.0.3 order, so E2E-TEMP-02 records the current line count (or a timestamp offset) for that string before it sets its own row's `expires_at` into the past, and then polls for the count to increase, or for a matching line after the recorded offset, rather than for the string's bare presence.

#### 3.0.6 Removed IDs

These IDs are not in use. They are recorded so that an external reference to one resolves to a disposition rather than reading as an omission.

| ID | Disposition |
|---|---|
| E2E-AUTH-10 | session idle timeout and absolute lifetime. Asserted in `middleware/middleware_test.go` |
| E2E-ADM-06 | admin-console allowlist size. A unit assertion over the `adminConsoleCommands` slice, not a count of rendered `<option>` elements |
| E2E-CSRF-02, -03, -04 | subsumed by E2E-CSRF-01, whose route list is derived from the router and therefore covers all twenty-three POST routes |
| E2E-RBAC-N+1, -N+2 | renumbered to E2E-RBAC-02 and E2E-RBAC-03 |
| E2E-RBAC-N+3 | subsumed by E2E-AUTH-14, which asserts privilege changes reaching a live session |
| E2E-BAK-02 | subsumed by E2E-RBAC-01 row and E2E-CSRF-01 row: the manager-403 case is a cell of E2E-RBAC-01's matrix (`/admin/backup/download` POST), and the no-token-303 case, including the gzip-body check, is the `/admin/backup/download` row of E2E-CSRF-01. E2E-SEC-05 keeps only its bundle-content assertions, having dropped its duplicate viewer/manager 403 steps |
| E2E-RBAC-03 | both halves are cells already in E2E-RBAC-01's matrix: the viewer `200` on `/certificates/{id}` and the viewer `403` on `/download/key/{id}`. The note that this pair is the RBAC boundary moved onto those two matrix rows |
| E2E-CFG-03 | its first two cases byte-duplicated E2E-BOOT-07 case (d) and are subsumed by it. Its unique third case, `TRUST_PROXY=false` boots healthy and a forged `X-Forwarded-For` is ignored, became a final phase of E2E-CFG-02 |

### 3.1 Startup and bootstrap matrix

Every test in this section runs against a **disposable single-purpose stack**.

Teardown differs by test, and the differences are load-bearing:

- **`docker compose down -v`** for E2E-BOOT-02, E2E-BOOT-07, E2E-BOOT-08 and E2E-BOOT-09.
- **`down -v` for every volume except `step-ca-data`** for E2E-BOOT-01, E2E-BOOT-05 and E2E-BOOT-06. All three depend on the CA identity the fingerprint was computed from, so destroying `step-ca-data` between them invalidates `CA_FINGERPRINT` and the three tests stop being comparable. Use `docker compose down` followed by targeted `docker volume rm` of the step-ui volumes.
- **`make e2e-reset-ssl`** for E2E-BOOT-03 and E2E-BOOT-04, which care only about `/opt/step-ui/ssl`. It is roughly two orders of magnitude faster than a full `down -v` and avoids re-seeding the admin user.

The steps below are written with `docker compose up -d --build`, which is what a developer runs locally. In CI the image is already built by the `image` job, so the scenario driver substitutes `-f compose.e2e-image.yml` and drops `--build`. That substitution is the driver's job and is not repeated in every test.


#### E2E-BOOT-01: `stepca` mode happy path from empty volumes via `CA_FINGERPRINT`

*Tier:* PR (e2e-bootstrap, scenario `fingerprint`).

*Objective:* Discharge the plan's own stated blind spot, the `CA_FINGERPRINT`-from-empty-volume path, and demonstrate that root fetch and leaf issuance both now happen in-process (Risk R1).

*Preconditions:*
1. Stack composed as `docker-compose.yml` **plus `compose.e2e-fingerprint.yml`**. Without the override this test passes without exercising anything: the root cert is already present via the read-only mount, `ensureRootCert` early-returns at its `os.Stat` guard, `CA_FINGERPRINT` is never read, and the grepped log line never appears.
2. `.env`: `UI_TLS_MODE=stepca`, `STEPUI_ADMIN_PASSWORD=<strong pw>`, `CA_ROOT_CERT_PEM` empty.
3. Obtain the fingerprint: `docker compose down -v`, then `docker compose up -d --wait step-ca`, then `docker compose exec step-ca step certificate fingerprint /home/step/certs/root_ca.crt`. Write it to `CA_FINGERPRINT` in `.env`. Do **not** `down -v` after this point, because `step-ca-data` must retain the cert the fingerprint was computed from. The `step` CLI is present in the `smallstep/step-ca` image. If a future image drops it, substitute `openssl x509 -in /home/step/certs/root_ca.crt -outform DER | openssl dgst -sha256`, which produces the same lowercase hex digest.

*Steps:*
1. `docker compose up -d --build`.
2. Poll `docker compose ps --format json step-ui` until `Health == "healthy"`. Bound at **180s**. The healthcheck's own ceiling is `start_period 20s + interval 10s × retries 10 = 120s` (`docker-compose.yml:117-122`), so 180s is a real timeout rather than an unbounded wait. On expiry, report the last observed health state and the last ten log lines.
3. `docker compose logs --no-color --timestamps step-ui | grep -F 'fetching root CA certificate via CA_FINGERPRINT'`.
4. `docker compose logs --no-color --timestamps step-ui | grep -F 'root CA certificate fetched and verified'`.
5. `docker compose logs --no-color --timestamps step-ui | grep -F 'UI leaf certificate obtained'`.
6. `docker compose exec step-ui test -s /opt/step-ui/data/root_ca.crt`.
7. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} -showcerts </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject -dates`.
8. `docker compose exec step-ui which step`.
9. `grep -cE '(^|[^-[:alnum:]])step +(ca|certificate|version|crypto)([^-[:alnum:]]|$)' step-ui-go/entrypoint.sh`.

*Assertions:*
- Container reaches `healthy` within the bound.
- All three log lines from steps 3 to 5 are present, in that order, in a single boot. Step 3's line is what proves the fingerprint path executed rather than the volume-mount path, and it is the assertion the stock stack cannot produce.
- The root cert file exists and is non-empty at the override's writable path.
- Issuer CN is the step-ca intermediate and differs from the subject. A self-signed fallback would have issuer equal to subject, so this is the discriminating comparison, not the presence of any particular string.
- `which step` exits non-zero. The `step` binary is not in the runtime image.
- The `entrypoint.sh` grep returns 0. Together with the previous assertion this discharges R1 in both directions: the binary is gone **and** nothing still tries to call it. The hyphenated `step-ca` occurrences in that file's comments are not matches.

*Failure-triage note:* the failure message must state whether `compose.e2e-fingerprint.yml` was actually applied. Without it this test passes without exercising the path it names, since the stock stack's read-only mount makes `ensureRootCert` early-return before `CA_FINGERPRINT` is ever read.

*Teardown:* `docker compose down -v`.


#### E2E-BOOT-02: CA down at boot, `UI_TLS_MODE=stepca` exhausts the retry loop and falls back

*Tier:* PR (e2e-bootstrap, scenario `ca-down`).

*Objective:* Prove that the leaf-issuance retry loop runs to exhaustion and then falls back, and that the process serves traffic throughout (Risk R2, the call-failure half).

*Preconditions:* Fresh volumes, but with a **root cert present and a CA that refuses connections**. Both conditions are required, and a stack that satisfies only the second is the trap here. Concretely:
1. `docker compose down -v`, then `docker compose up -d --wait step-ca postgres` so that `step-ca-data` is initialised and the root cert exists.
2. `docker compose stop step-ca`.
3. `.env`: `UI_TLS_MODE=stepca`, `STEPUI_ADMIN_PASSWORD` set.
4. `compose.e2e-nodeps.yml`, which removes `step-ui`'s `depends_on: step-ca: condition: service_healthy`. Without it `docker compose up step-ui` will not start at all against a stopped CA.

If the root cert is absent, `stepca.New` fails eagerly on the missing file, `caClient` is nil, `ensureUICert` takes the nil-client short-circuit, and the fallback is instantaneous. That variant is a legitimate scenario, but it is **E2E-BOOT-09**, not this test. Conflating the two produces a test that finishes in roughly zero seconds while claiming to exercise a thirty-second retry loop, which is why the timestamp gap below is an assertion rather than a note.

*Steps:*
1. `docker compose up -d --build step-ui`.
2. Poll `docker compose logs --no-color --timestamps step-ui` every 2s until the exact line `step-ca certificate issuance failed after retries — falling back to self-signed` appears. Bound at 90s.
3. `curl -sk -o /dev/null -w '%{http_code}\n' https://localhost:${UI_HTTPS_PORT:-443}/login`.
4. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject`.

*Assertions:*
- The log contains the exact full message `step-ca certificate issuance failed after retries — falling back to self-signed`. Not the substring `falling back to self-signed`, which three distinct code paths emit.
- That line is **preceded** by the exact line `obtaining UI leaf certificate from step-ca` (`tlsbootstrap.go:218`). This line is logged only when a non-nil CA client entered the loop, and it is the only observable that separates the retry path from the nil-client short-circuit.
- The gap between the two timestamps is **at least 28s**. The loop is 30 attempts at 1s, so anything materially under 30s means the loop did not run. The 2s tolerance absorbs container clock granularity.
- Step 3 returns `200`. TLS bootstrap failure is non-fatal, which is the R2 property.
- Issuer equals subject on the served certificate.

*Failure-triage note:* when this test fails, the failure message must include both timestamps and the full grep output, since a genuine regression and a slow container start present identically as a missing or late line.

*Teardown:* `docker compose down -v`, restore the `depends_on` override.


#### E2E-BOOT-03: `UI_TLS_MODE=provided` leaves an operator certificate untouched

*Tier:* PR (e2e-bootstrap, scenario `provided`).

*Objective:* Prove the `provided` branch is a genuine no-op, using a live negative control rather than an absence assertion.

*Preconditions:* `.env`: `UI_TLS_MODE=provided`, `STEPUI_ADMIN_PASSWORD` set. Seed a certificate and key at the two hardcoded paths `/opt/step-ui/ssl/server.crt` and `/opt/step-ui/ssl/server.key` before `step-ui` starts. Generate it **on the harness**, not inside the runtime image, and copy it into the `step-ui-ssl` volume with a throwaway alpine container. The image runs as uid 10001 and cannot `apk add`, and it would not need to since `openssl` is already present, but generating outside keeps the material under the harness's control:

```
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -days 1 -nodes \
  -subj /CN=test-provided -keyout server.key -out server.crt
```

*Steps:*
1. `docker compose up -d --build`, wait for healthy.
2. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -subject -fingerprint -sha256`.
3. **Negative control.** With the *same* pre-seeded certificate still in place, recreate the stack (`docker compose up -d --build`, not `restart`, since the point is a changed `UI_TLS_MODE`) with `UI_TLS_MODE=stepca` and a reachable CA.
4. Repeat the probe from step 2.

*Assertions:*
- Step 2 shows `CN=test-provided` and a SHA-256 fingerprint identical to the file the harness generated.
- Step 4 shows a **different** subject and a different fingerprint, and `docker compose logs --no-color --timestamps step-ui | grep -F 'UI leaf certificate obtained'` finds the line.

The negative control is what makes this test meaningful. Asserting only that no bootstrap log lines appeared in step 2 would pass just as well if the entire bootstrap block had been deleted from `main`.

*Teardown:* `make e2e-reset-ssl`.


#### E2E-BOOT-04: self-signed default when `UI_TLS_MODE` is unset

*Tier:* PR (e2e-bootstrap, scenario `selfsigned`).

*Objective:* Prove the default branch generates a self-signed certificate in-process, and that it arrived there by the default branch rather than by falling out of a failed `stepca` attempt.

*Preconditions:* Fresh `step-ui-ssl` volume. `.env`: `UI_TLS_MODE` commented out entirely, so `config.Load`'s `getEnv("UI_TLS_MODE", "self-signed")` default applies (`config/config.go:114`). `STEPUI_ADMIN_PASSWORD` set.

*Steps:*
1. `docker compose up -d --build`, wait for healthy.
2. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject -dates -ext subjectAltName`.
3. `docker compose logs --no-color --timestamps step-ui | grep -cF 'generated self-signed TLS certificate'`.
4. `docker compose logs --no-color --timestamps step-ui | grep -cF 'falling back to self-signed'`.

*Assertions:*
- Issuer equals subject.
- Public key is EC P-256.
- SAN list contains `IP Address:<HOST_IP>` and `DNS:localhost` at minimum, plus `DNS:<UI_HOSTNAME>` when `UI_HOSTNAME` is set (`generateSelfSignedCert`).
- `NotAfter - NotBefore` is approximately 10 years.
- Step 3 returns exactly **1**.
- Step 4 returns exactly **0**.

The last two assertions are the point of the test. Without them it passes against a completely dead bootstrap, because self-signed is also the terminal state of every failure path.

*Teardown:* `make e2e-reset-ssl`.


#### E2E-BOOT-05: wrong `CA_FINGERPRINT` exhausts the root fetch and is reported distinctly

*Tier:* PR (e2e-bootstrap, scenario `fingerprint`, runs after E2E-BOOT-01 in the same job).

*Objective:* Prove the root-fetch retry loop runs to exhaustion on a mismatched fingerprint, that the process still serves, and that `/ready` distinguishes this from a CA outage.

*Preconditions:* As E2E-BOOT-01, including `compose.e2e-fingerprint.yml`, but with `CA_FINGERPRINT` set to 64 hex zeros, which is well-formed and wrong. `step-ca` is running and healthy throughout.

*Steps:*
1. `docker compose up -d --build`.
2. Poll the logs every 2s for the exact line `could not fetch root CA certificate after retries — continuing without it`. Bound at 90s.
3. `docker compose ps --format json step-ca`.
4. `docker compose exec step-ca curl -sk https://localhost:9443/health`.
5. `docker compose exec step-ui test -e /opt/step-ui/data/root_ca.crt`.
6. `curl -sk https://localhost:${UI_HTTPS_PORT:-443}/ready` and **parse the JSON**.
7. `curl -sk -o /dev/null -w '%{http_code}\n' https://localhost:${UI_HTTPS_PORT:-443}/login`.

*Assertions:*
- The exact root-fetch exhaustion line is present, preceded by `fetching root CA certificate via CA_FINGERPRINT`, with at least 28s between the two timestamps.
- `/ready` returns `503` and its `ca` field is `unreachable`.
- **The disambiguating triple**, all in the same run: step 3 reports `step-ca` healthy, step 4 returns `{"status":"ok"}`, and step 5 exits non-zero because no root cert was ever written. Only with all three does `"ca":"unreachable"` mean *fingerprint mismatch*. On its own it is byte-identical to E2E-HLTH-03's assertion, which means the opposite thing: `checkCAReachability` returns `"unreachable"` for any `client.Do` error, and here the error is a TLS trust failure against a perfectly healthy CA (`handlers/health.go`, `checkCAReachability`).
- Step 7 returns `200`.

*Do not assert the total elapsed bootstrap time as 90 seconds.* The `2*caBootstrapRetries*caBootstrapInterval + 30s` figure in `main.go` is the **outer context ceiling**, not a retry budget. The two loops total at most 60s of retry, and under this scenario only the root loop runs to exhaustion, since `stepca.New` then fails on the absent root file and `ensureUICert` short-circuits on the nil client. Observed elapsed time is around 30s.

*Failure-triage note:* the failure message must state whether `compose.e2e-fingerprint.yml` was actually applied. Without it this test passes without exercising the path it names, for the same reason given in E2E-BOOT-01's note.

*Teardown:* `docker compose down -v`.


#### E2E-BOOT-06: `CA_ROOT_CERT_PEM` inline root provisioning

*Tier:* PR (e2e-bootstrap, scenario `fingerprint`, third case in the same job).

*Objective:* Cover the fourth root-provisioning mode, which exists for ECS and Kubernetes deployments where a shared volume is impractical, and which no other test touches.

*Preconditions:* `compose.e2e-fingerprint.yml` (so `ROOT_CERT` points at a writable path and no root cert pre-exists). `.env`: `UI_TLS_MODE=stepca`, `CA_FINGERPRINT` **empty**, `CA_ROOT_CERT_PEM` set to the literal PEM of the running CA's root, read out beforehand with `docker compose exec step-ca cat /home/step/certs/root_ca.crt`. `STEPUI_ADMIN_PASSWORD` set.

*Steps:*
1. `docker compose up -d --build`, wait for healthy.
2. `docker compose logs --no-color --timestamps step-ui | grep -F 'wrote root CA certificate from CA_ROOT_CERT_PEM'`.
3. `docker compose logs --no-color --timestamps step-ui | grep -cF 'fetching root CA certificate via CA_FINGERPRINT'`.
4. `docker compose exec step-ui cat /opt/step-ui/data/root_ca.crt` and compare byte-for-byte with the PEM supplied in `CA_ROOT_CERT_PEM`.
5. `docker compose logs --no-color --timestamps step-ui | grep -F 'UI leaf certificate obtained'`.
6. `curl -sk https://localhost:${UI_HTTPS_PORT:-443}/ready`.

*Assertions:*
- The inline-write log line is present.
- Step 3 returns 0. The positive control for that absence is E2E-BOOT-01, which produced the line under the same override earlier in this job. **The scenario driver must persist E2E-BOOT-01's captured log text to the job workspace before that test tears down**, or the control does not survive to be compared against.
- The written file matches the supplied PEM exactly.
- Leaf issuance succeeded, so the inline root satisfied `ca.WithRootFile` and the CA client constructed.
- `/ready` returns `200` with `{"status":"ready"}`.

*Teardown:* `docker compose down -v`.


#### E2E-BOOT-07: the deliberate startup fatals

*Tier:* PR (e2e-bootstrap, scenario `fatals`).

*Objective:* Pin the five intended `log.Fatal` paths, so that "never fatal on CA failure" is asserted against a known set of fatals that are intended.

*Preconditions:* Fresh volumes for cases (b) and (c). Each case is a separate `up` of `step-ui` alone with the rest of the stack already healthy. Unlike every other bootstrap scenario, `scenario.sh fatals` does **not** set `STEPUI_ADMIN_PASSWORD` for case (b), which needs it absent. **The scenario's own override sets `restart: "no"` on `step-ui`**, replacing the stock `restart: unless-stopped` (`docker-compose.yml:63`), for every case in this scenario, not only case (c). Against the stock policy a fatal exit restarts the container into the same fatal, and reading `ExitCode` mid-crash-loop races the next restart; with `restart: "no"` the container exits once and stays exited. Poll `docker inspect --format '{{.State.Status}}' "$(docker compose ps -q step-ui)"` until it reports `exited`, then read `ExitCode`. Resolve the container id through `docker compose ps -q` rather than the bare name `step-ui`, since a bare name is ambiguous whenever more than one compose project is live on the same host, which every bootstrap scenario running alongside others in CI is.

*Steps and assertions, one per case:*

| Case | Injected condition | Expected |
|---|---|---|
| (a) weak `SECRET_KEY` | write the literal `change-me-in-production-32chars!` into `secrets/secret_key`, then repeat with a string shorter than 32 bytes | Container exits non-zero. Logs contain `FATAL: SECRET_KEY is the default or shorter than 32 chars` (`main.go:132`). Both sub-cases tested |
| (b) empty users table with no admin password | fresh `postgres-data`, `STEPUI_ADMIN_PASSWORD` unset | Container exits non-zero once and stays `exited` under the scenario's `restart: "no"` override (see the preconditions above). Logs contain `No admin user exists and STEPUI_ADMIN_PASSWORD is not set` (`db/schema.go:145-162`) |
| (c) database unreachable | `docker compose stop postgres`, plus `compose.e2e-nodeps.yml` | Container exits non-zero. Logs contain `Cannot connect to database:` (`main.go:161`). Bound the wait at **90s**: `entrypoint.sh:70-77` runs a 60-iteration one-second wait-for-PostgreSQL loop before exec'ing the binary, so the fatal is delayed by up to 60s and is preceded by `PostgreSQL not reachable ... continuing; the app will retry.` |
| (d) `TRUST_PROXY=true` with no usable CIDR list | `TRUST_PROXY=true`, `TRUSTED_PROXY_CIDRS` unset, then again with `not-a-cidr` | Container exits non-zero. Logs contain `FATAL: TRUST_PROXY=true requires a usable TRUSTED_PROXY_CIDRS` (`main.go:142-145`). This is the suite's only coverage of both sub-cases (Section 3.0.6) |
| (e) bad `OIDC_DEFAULT_ROLE` with OIDC on | `OIDC_ENABLED=true`, `OIDC_DEFAULT_ROLE=nonsense` | Container exits non-zero. Logs contain `is not one of viewer, manager, admin` (`main.go:151-153`). The value is operator-supplied and reaches a role field without passing either `UsersPost` check, so it was a second route around the V9 invariant |

The `SECRET_KEY` mechanism in case (a) is not interchangeable with an `.env` edit. `docker-compose.yml:100` passes only `SECRET_KEY_FILE`, and there is no `SECRET_KEY` key in the `step-ui` environment block, so setting it in `.env` is the silent no-op described in Section 2.5. The value has to reach `secrets/secret_key`.

For every case, assert `docker inspect --format '{{.State.ExitCode}}' "$(docker compose ps -q step-ui)"` is non-zero and that `curl` against the UI port fails to connect. A container that logs the fatal message and keeps serving is a regression this test must catch.

*Positive control:* the same job runs one `up` with none of the five conditions injected and asserts the container reaches `healthy`. Without it, a compose override that simply prevented `step-ui` from starting at all would pass all five cases.

*Teardown:* `docker compose down -v`.


#### E2E-BOOT-08: SIGTERM drains in-flight requests

*Tier:* nightly (`bootstrap-extra` leg).

*Objective:* Cover the graceful-shutdown path (`signal.NotifyContext` on `SIGINT`/`SIGTERM`, then `srv.Shutdown`), which no other test reaches and which a refactor of `main`'s tail could silently remove.

*Preconditions:* Its own disposable stack, since the test terminates `step-ui`. Harness on `step-network`, admin session established. Seed 200 certificates via repeated `POST /issue` and run `make e2e-seed-history 5000` for the `cert_history` table. This is a starting recipe, not a promise: the point is a bundle that takes several seconds to stream, and the test calibrates against its own run rather than trusting this number, per step 1 below.

*Steps:*
1. `POST /admin/backup/download` once, untimed, to measure this stack's actual wall clock for the seeded bundle. If it completes in under three seconds, seed more certificates and history and repeat until it takes at least three seconds. This calibration run's bundle is discarded.
2. Start the timed request: `POST /admin/backup/download` again. Record the wall clock the request was issued.
3. Wait until the response has demonstrably started streaming before signalling: the response headers have been received and at least one body chunk has arrived, with more still pending. Signalling on a fixed delay (for example "500ms after the request is issued") is vacuous if the bundle already finished building and started streaming before that delay elapses, or if it has not yet started at all; this step is what makes "mid-write" a checked condition rather than an assumption.
4. `docker compose kill -s SIGTERM step-ui`.
5. Read the response to step 2 to completion.
6. Concurrently, issue a second request 1s after the signal.

*Assertions:*
- The in-flight request completes with `200` and a bundle that passes E2E-BAK-01's manifest hash check. A dropped connection or a truncated body is a failure.
- The request issued after the signal is refused at the TCP level or returns a connection error. It must not be served.
- The container's exit code is 0.

*Teardown:* `docker compose down -v`.


#### E2E-BOOT-09: nil CA client short-circuits, and the failure is not cached

*Tier:* PR (e2e-bootstrap, scenario `ca-down`, second case in the same job).

*Objective:* Cover the **construction**-failure half of Risk R2, which every other CA-related test misses. All of E2E-BOOT-02, E2E-ADM-03, E2E-PROV-02 and E2E-HLTH-03 exercise a reachable-but-down CA, which is the *call* failure path. R2 is about `stepca.New` failing on a missing or unreadable root PEM, and the stock stack makes that impossible because the root cert is always mounted.

*Preconditions:* `compose.e2e-fingerprint.yml` so that `ROOT_CERT` is a writable path with no file at it. `.env`: `UI_TLS_MODE=stepca`, `CA_FINGERPRINT` empty, `CA_ROOT_CERT_PEM` empty. `step-ca` running and healthy. Admin session available.

*Steps:*
1. `docker compose up -d --build step-ui`, wait for healthy.
2. `docker compose logs --no-color --timestamps step-ui | grep -F 'CA client construction failed during TLS bootstrap'`.
3. `docker compose logs --no-color --timestamps step-ui | grep -F 'UI_TLS_MODE=stepca but no CA client is available — falling back to self-signed'`.
4. Log in as admin, `POST /admin/console` with `command_id=ca.health`.
5. Write a valid root cert into place **at runtime, without restarting**: `docker compose exec step-ca cat /home/step/certs/root_ca.crt` piped into `docker compose exec -T step-ui sh -c 'cat > /opt/step-ui/data/root_ca.crt'`.
6. `POST /admin/console` with `command_id=ca.health` again, still with no restart.

*Assertions:*
- Both bootstrap log lines are present, and the elapsed time between the container start and the fallback line is under 2s. The nil-client branch must not enter the retry loop.
- Step 4's console result has `Success=false` and output **exactly** `CA client unavailable`. That string is produced only by `caHealthNativeFn`'s nil-client guard (`handlers/admin_console.go:190`) and is therefore the unique oracle for the construction-failure path. A reachable-but-down CA produces a dial error instead, never this string.
- Step 6's console result has `Success=true` and output `ok`. This is the second half of R2: `Handler.caClient()` caches only on success and re-attempts `stepca.New` on every call after a failure (`handlers/handler.go:103-117`), so a root cert appearing later is picked up with no restart. Nothing else in the suite tests this.

*Teardown:* `docker compose down -v`.

### 3.2 Auth flows

Shared preconditions unless noted: long-lived stack up, admin seeded, harness running as a container on `step-network` so that each test can own its source IP, and a second non-admin user created via `POST /admin/users` (Section 3.3 covers that endpoint).

**Ordering constraints for this section, which are load-bearing.** `security.RL` keys on the client IP and `IsBlocked` is consulted *before* credential verification (`handlers/auth.go:64-70`), so E2E-AUTH-02 and E2E-AUTH-03 poison their source IP for roughly five minutes for every subsequent login from it. Two remedies, and the suite uses both: give those two tests their own harness container with its own address, and run them last within the section. E2E-AUTH-04 through E2E-AUTH-07 leave TOTP enabled on their subject, so they use a dedicated user and a mandatory disable teardown. E2E-AUTH-09 consumes its own 3-per-15-minute budget and is bounded by `docker compose restart step-ui`, which clears both process-local limiter maps.

Session idle timeout and absolute lifetime are not re-tested here. `middleware/middleware_test.go` already asserts them directly (`TestRequireLogin_AbsoluteLifetimeExpired`, `TestRequireLogin_ExpiredSession_Redirects`, `TestRequireLogin_FreshSession_AbsoluteLifetimeNotExpired`), and `SessionTimeout=8h`/`SessionMaxLifetime=24h` are Go constants (`middleware/middleware.go:29,34`) that no environment variable can shorten.


#### E2E-AUTH-01: successful local login rotates session content

*Tier:* PR (e2e-main).

*Preconditions:* create a disposable local user, using the worker-scoped admin fixture's already-authenticated context (Section 4.1.4) to issue the `POST /admin/users` call. This test logs that throwaway user out in its own teardown, and `POST /logout` bumps `session_epoch` for every session the acted-on user holds (`handlers/auth.go:222`); running the flow below against `admin` itself would evict the worker-scoped admin fixture that the rest of the run depends on.

*Steps:*
1. `GET /login`, capture the `csrf_token` hidden input and the `step-ui` cookie into jar A. Call this token `T_pre`.
2. `POST /login` with `csrf_token=T_pre`, `username=<the throwaway user>`, `password=<its password>`, same jar.
3. `GET /login` again on the post-login jar and capture the new `csrf_token`, `T_post`.
4. `POST /profile` with `action=theme`, `theme=dark`, `csrf_token=T_pre`, using the post-login jar.

*Assertions:*
- Step 2 returns `302` with `Location: /`.
- `T_post != T_pre`. `completeLogin` resets `s.Values` and assigns a freshly generated CSRF token (`handlers/auth.go:192-202`).
- Step 4 is **rejected**: `303` to `/profile` with the flash `Session error. Please refresh the page.` The pre-login token must not validate against the post-login session.
- An `auth_log` row exists with `success=true` and an empty reason, visible at `/admin/security` with label `Login`.

Do **not** assert "the session cookie value changed". `securecookie` encrypts with a fresh random nonce on every encode, so the cookie value differs on every `Save` whether or not `s.Values` was reset. That assertion cannot fail and therefore proves nothing. The token comparison and the rejection in step 4 are the observable consequences of the rotation that actually happened.

*Teardown:* `POST /logout` on the post-login jar with the `csrf_token` scraped from the page's inline logout form. `GET /logout` must not be used here: `LogoutGet` is a bare redirect that ends nothing (`handlers/auth.go:209-211`), and only the CSRF-gated `POST /logout` bumps `session_epoch` (E2E-AUTH-11, E2E-AUTH-12). This bumps only the throwaway user's epoch, not admin's.


#### E2E-AUTH-02: failed logins count down and then lock out

*Tier:* PR (e2e-main). Runs on its own harness IP, near the end of the section.

*Preconditions:* a dedicated lockout user, created via the admin fixture's context, with a known password. E2E-AUTH-02 and E2E-AUTH-03 share it: E2E-AUTH-02 spends its five-attempt budget, and E2E-AUTH-03 immediately exercises the resulting block against the same username and IP. Using a disposable user rather than `admin` or `manager` keeps the five denial rows this test writes off a shared account and avoids poisoning any other test's lockout state.

*Steps:* `POST /login` five times with the dedicated lockout user's `username`, a wrong `password` and a valid `csrf_token`, from a single source IP. After each POST, follow the redirect to `GET /login` and capture the rendered page. A separate admin session on a different IP is established **before** the five failed attempts, from the main harness context, since `IsBlocked` gates only `/login` (`handlers/auth.go:64`) and an admin login from an unrelated address is never at risk of the block; that session then issues `GET /admin/security`.

*Assertions:*
- Attempts 1 through 4 each carry a flash, and the four texts in order are `Invalid username or password. Attempts remaining: 4`, then `3`, then `2`, then `1`. Asserting only the first message pins a compile-time constant (`security.LimitCount = 5`, `security/security.go:111`) and passes against a counter that never decrements. The descending sequence is what tests the counter.
- **Attempt 5 carries no flash.** The message arrives on the *following* `GET /login` as `.Error`, because `LoginGet`'s `IsBlocked` branch renders it and the fifth-attempt flash was deliberately removed so the page does not show two error boxes (`handlers/auth.go:98-101`, and see Section 6.12). Assert that the page after attempt 5 contains exactly **one** occurrence of `Too many attempts. Please wait 5 minutes.` A test that looks for a flash here fails, and a test that counts occurrences loosely would have passed against the duplicated rendering this replaced.
- Every attempt returns `302` to `/login`. The wrong-credential branch redirects (`handlers/auth.go:102`). This is not the shape of the blocked branch that E2E-AUTH-03 exercises, which renders inline at `200`, nor of the CSRF and policy paths, which also render inline.
- `/admin/security` contains exactly five rows for the dedicated lockout user's username with `success=false`, all labelled `Denied`. That count is scoped to this username: `auth_log` is global and append-only, so an unscoped query would also pick up rows other tests wrote. E2E-SEC-01 does not assert these rows, because it runs before this test.

*Teardown:* none of its own. The block this test leaves in place is what E2E-AUTH-03 exercises next; E2E-AUTH-03 clears it in its own teardown.

*Not covered:* `notifyAsync("auth.failed_burst", ...)`, the webhook dispatch this failed-login burst triggers. `sendNotification` returns `nil` and writes nothing observable, anywhere, unless `NotificationSettings.WebhookEnabled` is true with a non-empty `WebhookURL` (`handlers/notifications.go:198-200`), and no stack in this suite configures a webhook receiver. The event fires; there is nothing here that can see it fire.

**Flash delivery is load-bearing for this test and roughly thirty others.** `models.FlashMsg` went unregistered with `gob` until 2026-08-10, so `sess.Save` failed and every flash was discarded (Section 6.11). Attempts 1 through 4 above are the suite's most direct check that the mechanism works at all.


#### E2E-AUTH-03: a correct password is rejected while the IP is blocked

*Tier:* PR (e2e-main). Immediately follows E2E-AUTH-02 on the same IP. Its teardown fallback restarts `step-ui`, so it carries the Section 3.0.4 barrier tag conditionally, on that fallback path.

*Preconditions:* the dedicated lockout user and its known password, shared with E2E-AUTH-02 (that entry's precondition).

*Steps:* `POST /login` with the dedicated lockout user's **correct** credentials and a valid `csrf_token` from the blocked IP.

*Assertions:*
- **`200` with the page rendered inline**, not a redirect. The `IsBlocked` branch sets `data["Error"]` and `data["Blocked"] = true` and calls `h.render` (`handlers/auth.go:64-70`). There is no flash and no `Location` header.
- The rendered page contains `Too many attempts. Please wait 5 minutes.` exactly once, from the `.Error` channel. `login.html` renders `.Error` and `.Msgs` in separate blocks, and this text must reach only the first.
- The submit buttons render disabled, since `data["Blocked"]` drives that too.
- No new session cookie, and no `auth_log` row for this attempt. `LoginPost` consults `security.RL.IsBlocked` before it reads the username or verifies the credential, so a correct password cannot pass a blocked IP. That ordering is the security property and it is the only part of the lockout observable over HTTP.

*Not covered:* when the block actually clears. `clean()` consults only `LimitWindow = 5 * time.Minute` (`security/security.go:112`); the user-facing copy and the API's `Retry-After` are now both derived from that same window (`security.LockoutMessage`, `security.RateLimiter.RetryAfter`), and `BlockTime` is gone. It is a unit-level timing question and belongs in `security/security_test.go`, where the clock can be controlled. Observing it over HTTP costs six minutes of CI and races the five-minute boundary.

*Teardown:* the block is cleared over HTTP, not by waiting. `POST /admin/users` as admin with `action=unblock_ip` and `target_ip=<the blocked ip>` calls `security.RL.Clear` (`handlers/users.go:125-131`). Assert that a subsequent correct login from that IP now succeeds. This is the suite's only live exercise of `unblock_ip` against a genuinely blocked address; E2E-ADM-08's row of the same action runs earlier, against a scratch value, and asserts only the form's shape. `make e2e-restart-ui` is the fallback if the admin session is unavailable.


#### E2E-AUTH-04: TOTP enrollment

*Tier:* PR (e2e-main). Uses a dedicated user. Runs in `api`, with a `ui` companion for the QR assertion.

*Steps:*
1. Log in as the dedicated test user.
2. `POST /profile/2fa/start` with the `csrf_token` from `GET /profile/2fa`.
3. `GET /profile/2fa` and scrape the pending secret from the readonly input at `templates/profile_2fa.html:104` (`value="{{.U.TOTPPendingSecret}}"`). No database access is required.
4. Compute a current code from that secret, through the boundary-guarded TOTP fixture of Section 4.1.4.
5. `POST /profile/2fa/confirm` with `csrf_token` and `totp_code`.
6. `GET /profile/2fa` again.

*Assertions:*
- The confirm response renders `profile_2fa` with exactly **8** recovery codes matching `^[A-Za-z0-9]{6}-[A-Za-z0-9]{6}-[A-Za-z0-9]{6}$` (`generateRecoveryCodes(8)`, `handlers/totp.go:114`). Save them.
- Step 6 no longer offers enrollment, since TOTP is now enabled.
- Re-fetching `GET /profile/2fa` does not render the recovery codes a second time. They are shown exactly once.

*`ui` companion.* The `ui` project runs after the `api` project completes (Section 4.1.1, 4.3), by which point E2E-AUTH-07's mandatory teardown has already disabled TOTP on the dedicated subject, so the `api` steps' pending enrolment no longer exists. The companion therefore starts its own: log in as the dedicated 2FA subject, `POST /profile/2fa/start` to create a fresh pending enrolment, then load `/profile/2fa` in the browser, read the rendered `<img>` at `/profile/2fa/qr`, decode the PNG, and assert the `otpauth://` URI it carries names the **same secret** as the readonly input on the same page. Two independent renderings of one secret must agree. The `api` steps above deliberately take the plaintext path, so without this companion nothing would notice a QR image that encoded the wrong value or failed to render at all. The companion tears down its own enrolment (`POST /profile/2fa/disable` with the current password and a fresh code) rather than leaving the subject mid-enrolment, since nothing later in the `ui` project depends on it staying pending.

*Note for E2E-AUTH-05:* `Profile2FAConfirm` calls bare `totp.Validate` (`handlers/totp.go:109`) and does **not** go through the replay guard, so `totp_last_step` is still zero on entry to the next test. That is why the replay assertion there is meaningful rather than pre-poisoned.

*Teardown:* none in `api`. TOTP stays enabled on the dedicated subject with all eight recovery codes still unused; this is intentional residue that E2E-AUTH-05 and E2E-AUTH-06 consume, and E2E-AUTH-07's mandatory teardown is what finally disables it.


#### E2E-AUTH-05: login with 2FA, including replay rejection

*Tier:* PR (e2e-main).

*Preconditions:* subject has TOTP enabled (E2E-AUTH-04).

*Steps:*
1. `POST /login` with correct username and password.
2. `GET /login` on the same jar.
3. **Gate on the TOTP window.** Compute `30 - (unixtime % 30)`. If it is under 15, sleep until the next 30-second boundary. Then compute the code.
4. `POST /login` with `totp_code=<code>` and the same jar.
5. Establish a second, independent pending-2FA session in jar B for the same user, and `POST /login` there with the **same** code.

*Assertions:*
- Step 1 returns `302` to `/login`, not `/`. `user.TOTPEnabled` routes into the pending-2FA branch (`handlers/auth.go:117-123`), which stores `pending_2fa_user_id` in the session.
- Step 2 renders the TOTP and recovery-code fields (`templates/login.html:37,49`).
- Step 4 returns `302` to `/`.
- Step 5 is **rejected**: `302` to `/login` with flash `Invalid 2FA or recovery code` (`handlers/auth.go:150-156`). `validateTOTPWithReplayCtx` records the consumed step and refuses a repeat (`handlers/totp.go:206-230`). The window gate in step 3 makes this deterministic. Without it a code computed at second 29 expires between the two submissions and the test passes for the wrong reason.

*Teardown:* none of its own. TOTP stays enabled on the subject, and all eight recovery codes remain unused; this is intentional residue that E2E-AUTH-06 and E2E-AUTH-07 consume in turn.


#### E2E-AUTH-06: login with a recovery code

*Tier:* PR (e2e-main).

*Preconditions:* TOTP enabled, one saved recovery code from E2E-AUTH-04.

*Steps:* reach the pending-2FA state as in E2E-AUTH-05, then `POST /login` with `recovery_code=<code>` instead of `totp_code` (field name at `templates/login.html:49`, matched case-insensitively at `handlers/totp.go:183`). Then repeat with the same code on a fresh pending-2FA session.

*Assertions:* the first attempt returns `302` to `/` and writes an `auth_log` row with reason `Login with recovery code` (`handlers/auth.go:161`). The second attempt with the same code returns `302` to `/login` with flash `Invalid 2FA or recovery code`, since `appdb.UseRecoveryCode` marked it used. The remaining count of unused codes is 7.

*Teardown:* none of its own. TOTP stays enabled, with 7 of 8 recovery codes unused; E2E-AUTH-07's mandatory teardown is what disables 2FA on this subject.


#### E2E-AUTH-07: disabling 2FA requires the password and a fresh code

*Tier:* PR (e2e-main).

*Steps:* three `POST /profile/2fa/disable` requests with `csrf_token`: wrong `current_password`; correct password with a replayed `totp_code`; correct password with a fresh code.

*Assertions:* all three return `302` to `/profile/2fa`, since `Profile2FADisable` redirects on every branch (`handlers/totp.go:136-166`). The flashes are, in order, `Current password is incorrect`, then `Invalid TOTP code` (the replay guard applies here, `handlers/totp.go:154`), then `2FA disabled` with a matching `auth_log` entry. After each of the first two, re-fetch `/profile/2fa` and confirm it still reports 2FA as enabled, rather than trusting the flash alone. `/admin/security` renders that third row with label `2FA`, since `securityEventLabel` matches `strings.HasPrefix(reason, "2FA")` for it (`handlers/audit.go:32-33`); this is the row Section 3.6's E2E-SEC-01 forward-references as arriving only later in the run.

*Teardown:* mandatory. The subject must leave this test with 2FA disabled, or every later login as that user breaks.


#### E2E-AUTH-08: OIDC login against a mock IdP

*Tier:* nightly (`oidc-mail` leg). Skips with reason when `E2E_OIDC_ENABLED` is unset.

*Preconditions:* stack composed with `compose.e2e-oidc.yml`, which supplies `ghcr.io/navikt/mock-oauth2-server` and passes through all eleven `OIDC_*` variables plus `LOCAL_LOGIN_ENABLED`. `OIDC_ENABLED=true`. The harness must run on `step-network`, because the issuer URL in the discovery document has to resolve identically for the app and for the client. `h.initOIDC()` calls `gooidc.NewProvider` during `Handler` construction and `log.Fatalf`s on discovery failure (`handlers/handler.go:81-96`), so an unreachable issuer stops the whole application rather than just OIDC. Skip with an explicit reason, never silently, when the override is unavailable.

*Steps:*
1. `GET /auth/oidc/login`, registered only when `cfg.OIDCEnabled` (`main.go:226-229`). Follow the redirect to the IdP, capturing `state`, `nonce` and the PKCE challenge.
2. Complete authentication at the mock IdP as a subject whose group claim matches `OIDC_GROUP_MANAGER`.
3. Follow the callback to `/auth/oidc/callback`.
4. Repeat, tampering with `state` on the callback.
5. Repeat as a subject in no mapped group, with `OIDC_DEFAULT_ROLE` empty.
6. Repeat as a subject whose claim matches both `OIDC_GROUP_ADMIN` and `OIDC_GROUP_VIEWER`.
7. Change the mapped user's role to `viewer` via `POST /admin/users action=change_role`, then log in again through OIDC with `OIDC_SYNC_ROLE=true`.

*Assertions:*
- Step 3 completes login and `appdb.UpsertOIDCUser` created the user with role `manager`.
- Step 4 flashes `OIDC state mismatch — possible CSRF attack` and creates no session.
- Step 5 flashes `Access denied: your account is not in an authorised group` and creates **no user row**. Verify the absence directly against the `users` table, with step 3's successful creation in the same run as the positive control.
- Step 6 resolves to `admin`. Admin precedence over the other two mappings (`handlers/oidc.go:26-47`).
- Step 7 re-synchronises the role back to `manager`, since `OIDC_SYNC_ROLE` defaults to true.


#### E2E-AUTH-09: password reset request and completion

*Tier:* nightly (`oidc-mail` leg). Skips with reason when `E2E_MAIL_ENABLED` is unset. Restarts `step-ui`, so it takes the Section 3.0.4 barrier.

*Preconditions:*
- Stack composed with `compose.e2e-mail.yml` (mailpit, SMTP on 1025, HTTP API on 8025).
- `PUBLIC_BASE_URL` set. `resetLink` refuses to build a link without it and returns `PUBLIC_BASE_URL is not configured; refusing to send a password reset link` (`handlers/password_reset.go:256-266`). It deliberately never derives the origin from the request.
- SMTP configured through `POST /admin/notifications` with `SMTPEnabled=true` and non-empty `SMTPHost`/`SMTPFrom`, pointed at mailpit.
- **The subject must have an email address, and there is only one way to give it one.** `POST /admin/users action=create` has no email field. The subject must set it themselves via `POST /profile` with `action=update_info` and a non-empty `email`. Do this in the preconditions, not in the steps.
- Budget accounting: this test issues **four** `/forgot-password` requests and the limiter allows exactly three per IP per fifteen minutes (`passwordResetLimitCount=3`, `passwordResetLimitWindow=15m`, `handlers/password_reset.go:25-26`), so the fourth is the one expected to be refused. Issue that case last, and `make e2e-restart-ui` before any retry, since `passwordResetRL` is a process-local map.
- Purge mailpit's inbox, or record its current message count as a baseline, before step 1. Section 1.3's oidc-mail leg note states why: this instance is shared with E2E-NOTIF-01.

*Steps:*
1. `POST /forgot-password` with `csrf_token` and `identifier=<the subject's username>`.
2. `POST /forgot-password` with a **nonexistent** identifier, from the same cookie jar.
3. Diff the two response bodies after stripping the `csrf_token` hidden input from both.
4. Fetch the message from mailpit's API, extract `token` from the link.
5. `GET /reset-password?token=<token>`.
6. `POST /reset-password` three times: mismatched passwords; matching but policy-invalid; matching and policy-valid.
7. `POST /reset-password` again reusing the same `token`.
8. A **third** `POST /forgot-password` from the same IP, for a different nonexistent identifier. This is still within budget (steps 1, 2 and this one make three) and is expected to succeed generically, the same as step 2.
9. A **fourth** `POST /forgot-password` from the same IP. This is the one the budget in the preconditions refers to.
10. **The `PUBLIC_BASE_URL` refusal case.** Recreate `step-ui` (`docker compose up -d step-ui`; a `restart` would not apply the `.env` edit, per Section 2.5) with `PUBLIC_BASE_URL` empty. Record mailpit's message count as a fresh baseline, since the recreate also clears the password-reset rate limiter. `POST /forgot-password` with the subject's identifier again, against the recreated stack.

*Assertions:*
- Steps 1 and 2 both render `If an account with that login or email exists, a password reset link has been sent.` (`genericResetInfo`, `handlers/password_reset.go:29`) at `200`, and the stripped bodies are **byte-identical**. The CSRF token must be stripped before the comparison, since it is regenerated per response and would otherwise fail the diff for an unrelated reason. Both requests must share a cookie jar.
- Exactly one message arrives in mailpit above the precondition's baseline, for step 1. Step 2's non-delivery is the negative case and step 1 is its positive control. `/admin/security` renders step 1's own row with label `Reset`, since `securityEventLabel` matches `strings.HasPrefix(reason, "Password reset")` for its `Password reset email sent` reason (`handlers/audit.go:36-37`); this is the row Section 3.6's E2E-SEC-01 forward-references as arriving only later in the run.
- Step 8 returns the same generic response as steps 1 and 2, since it is only the third request against the three-per-window budget.
- Step 5 renders the form, not `This reset link is invalid or has expired. Please request a new one.`
- Step 6's first two sub-cases return **`200` with the error rendered inline**: `Passwords do not match.` with the trailing period, then the `security.ValidatePassword` policy message (`handlers/password_reset.go:170-182`). The third returns `303 See Other` to `/login` with flash `Password updated. Please sign in with your new password.` This success redirect is `303`, unlike most success redirects in the application, which are `302`.
- Step 7 yields `This reset link is invalid or has expired. Please request a new one.` (`MarkPasswordResetTokenUsed` plus `InvalidatePasswordResetTokens`, `handlers/password_reset.go:204-206`).
- Step 9 returns the **same generic response**, with no client-observable rate-limit signal, while `/admin/security` gains a row with reason `Password reset rate limited` for the pseudo-user `password-reset` (`handlers/password_reset.go:58`).
- The new password works at `/login` and the old one does not.
- Step 10 returns the **same generic response** as steps 1, 2 and 8, with zero mailpit messages above its own fresh baseline. As the positive control that the refusal actually happened rather than the request silently no-opping some other way, `docker compose logs --no-color --timestamps step-ui | grep -F 'password reset link could not be built'` finds the line (`handlers/password_reset.go:114`), and `/admin/security` gains a row for the subject with reason `Password reset link not built: PUBLIC_BASE_URL is not configured; refusing to send a password reset link` (`:256-266`).

*Teardown:* recreate `step-ui` with `PUBLIC_BASE_URL` restored to the leg's stock value.


#### E2E-AUTH-11: logout is a POST, and a GET to the same path logs nobody out

*Tier:* PR (e2e-main).

*Objective:* A session that has been logged out no longer authorises requests from the client that held it, and logout cannot be triggered cross-site.

*Preconditions:* a disposable throwaway user it creates and logs out itself, not `admin`. `POST /logout` bumps `session_epoch` for every session the acted-on user holds (`handlers/auth.go:222`); logging `admin` out here would evict the worker-scoped admin fixture the rest of the run depends on (Section 4.1.4).

*Steps:*
1. Log the dedicated user in on jar A and confirm `GET /` returns `200`.
2. `GET /logout` on jar A.
3. `GET /` on jar A.
4. `POST /logout` on jar A with no `csrf_token`.
5. `GET /` on jar A.
6. `POST /logout` on jar A with the `csrf_token` scraped from the page's inline logout form.
7. `GET /` on jar A.

*Assertions:*
- **Step 2 returns `302` to `/login` and ends nothing.** Step 3 still returns `200`. `LogoutGet` is a bare redirect (`handlers/auth.go:209-211`, routed at `main.go:224`), kept so an old bookmark degrades rather than breaking. This is the assertion that would catch a regression back to the V10 shape, and it is worth more than the happy path.
- **Step 4 returns `303` to `/`** with the session-error flash and ends nothing. Step 5 still returns `200`. `Logout` sits behind `requireCSRF` with `/` as its redirect target (`handlers/auth.go:215-217`).
- Step 6 returns `302` to `/login`. `RequireLogin` redirects rather than forbidding, and the difference between the two is itself a regression surface.
- **Step 7 confirms step 6's POST actually ended the session on the jar that issued it**: `302` to `/login`, not `403` or `200`. That is the full extent of what this entry asserts about revocation; that a session revoked here is also unusable from a **different** copy of the cookie captured beforehand is E2E-AUTH-12's property, not restated here.
- The response to step 6 carries a session cookie with `Max-Age=-1`, and an `auth_log` row with reason `Logout`.
- Both `base.html` and `admin_base.html` render an inline logout form with a `csrf_token`, so the POST is reachable from every authenticated page.

*`ui` companion.* Load one page from each base template in the browser, click the logout control, and assert the resulting navigation lands on `/login` with the session ended. Steps 1 to 7 above prove the route contract; this proves an operator can actually reach it. A logout form that renders but does not submit, or whose token is stale on a cached page, is invisible to every request-level assertion.

E2E-AUTH-12 covers the stronger property, that a **copy** of the cookie taken before logout is also revoked. `/logout` is one of the twenty-three routes E2E-CSRF-01 sweeps.


#### E2E-AUTH-12: logout revokes a cookie captured before it

*Tier:* PR (e2e-main).

*Objective:* The session epoch of Section 6.3 is a real revocation handle, not just a cleared browser cookie.

*Preconditions:* a disposable admin-role throwaway user it creates and logs out itself, not the shared `admin` fixture, for the same reason given in E2E-AUTH-11 (Section 4.1.4).

*Steps:*
1. Log the dedicated admin-role user in on jar A. Copy the raw `step-ui` cookie value to a variable, simulating a capture.
2. `GET /admin` with the captured value as a bare `Cookie` header and no jar, to establish that the capture is a working credential.
3. `POST /logout` on jar A with the `csrf_token` scraped from the page's inline logout form. `GET /logout` must not be used here: `LogoutGet` is a bare redirect that ends nothing (`handlers/auth.go:209-211`), and only the CSRF-gated `POST /logout` bumps `session_epoch` (`:215,222`).
4. Repeat step 2 with the same captured value.

*Assertions:*
- Step 2 returns `200`. This is the positive control. Without it, step 4's rejection is satisfied by a cookie that never worked in the first place.
- Step 4 returns `302` to `/login`. `Logout` bumps `session_epoch` (`handlers/auth.go:222`), and `RequireLogin` re-reads the user row on every request and rejects a cookie whose stamped epoch no longer matches (`middleware/middleware.go:120-125`).
- The response to step 4 also clears the session cookie, since `rejectSession` empties `sess.Values` and saves before redirecting (`middleware/middleware.go:133-137`).


#### E2E-AUTH-13: the viewer-to-admin escalation chain is refused at both gates

*Tier:* nightly (`oidc-mail` leg). Skips with reason when `E2E_OIDC_ENABLED` is unset.

*Objective:* Walk the former V1 escalation chain and assert it is refused at both of the gates that now close it.

*Preconditions:* OIDC stack. `OIDC_GROUP_ADMIN` mapped to a group. A local viewer account named `victim-viewer` with a known password. An OIDC subject with `preferred_username=oidc-admin` in the admin group who has **never logged in**, so no `users` row exists for that name.

*Steps:*
1. As the viewer, `POST /profile` with `action=update_info`, a new `display_name`, and `username=oidc-admin`.
2. `GET /profile` and read back the account's username, display name and role.
3. As admin, create a **local** account named `oidc-admin` with role `viewer` and a known password, so that the OIDC subject's `preferred_username` now collides with a local row.
4. Complete an OIDC login as the `oidc-admin` subject.
5. Read the `oidc-admin` row and `GET /admin/security`.
6. Log in at `/login` as `oidc-admin` with the **local** password from step 3 and check the tier it reaches.

*Assertions:*
- **Gate one, the rename.** Step 1 returns `302` to `/profile` with flash `Profile updated`, and step 2 shows the display name changed and the **username unchanged**. `ProfilePost action=update_info` no longer reads a `username` field at all, so the submitted value is ignored rather than rejected. Assert the username is still `victim-viewer`, not that an error was shown.
- **Gate two, the upsert.** Step 4 does not log in. It returns `302` to `/login` with flash `Access denied: that username belongs to a local account. Contact an administrator.` `UpsertOIDCUser` carries `WHERE users.auth_source = 'oidc'` on both `DO UPDATE` branches, so the collision updates nothing, `RowsAffected()` is 0, and the call returns `appdb.ErrOIDCLocalUser` (`db/users.go:266-309`).
- **The local row is untouched.** Step 5 shows `oidc-admin` still has role `viewer`, still has `auth_source='local'`, and still has its original `password_hash`. A silent promotion is exactly what this test exists to catch, so assert the role explicitly rather than inferring it from the denial.
- **The denial is recorded.** Step 5's `/admin/security` contains a row for `oidc-admin` with `success=false` and reason `OIDC: username collides with a local account` (`handlers/oidc.go:198-199`).
- Step 6 logs in as a **viewer** and `GET /admin` returns `403`.


#### E2E-AUTH-16: an unmapped OIDC subject gets the configured default role, and a disabled sync does not revert a changed role

*Tier:* nightly (`oidc-mail` leg). Skips with reason when `E2E_OIDC_ENABLED` is unset.

*Objective:* `mapGroupsToRole` returning empty is not automatically a denial: `OIDC_DEFAULT_ROLE` is consulted first (`handlers/oidc.go:187`) and only an equally-empty default falls through to the refusal branch. E2E-AUTH-08 already covers that empty-default denial (its step 5); this entry covers the case where the default is set, plus `OIDC_SYNC_ROLE=false`'s effect on a later login for the same subject, neither of which any other entry exercises.

*Preconditions:* OIDC stack. `OIDC_DEFAULT_ROLE=viewer`. `OIDC_GROUP_ADMIN`, `OIDC_GROUP_MANAGER` and `OIDC_GROUP_VIEWER` mapped to groups this test's subject does not belong to. A subject `oidc-unmapped`, in no mapped group, who has never logged in before this test.

*Steps:*
1. Complete an OIDC login as `oidc-unmapped`.
2. Read the `oidc-unmapped` row.
3. As admin, `POST /admin/users action=change_role` on `oidc-unmapped` to `manager`.
4. Recreate `step-ui` with `OIDC_SYNC_ROLE=false` (`docker compose up -d step-ui`; a `restart` would not apply the `.env` edit, per Section 2.5).
5. Complete a second OIDC login as `oidc-unmapped`, whose claims still carry no mapped group.
6. Read the `oidc-unmapped` row again.

*Assertions:*
- Step 1 completes login rather than being refused. `mapGroupsToRole` returns empty for an unmapped subject, but `role = h.cfg.OIDCDefaultRole` (`handlers/oidc.go:187`) supplies `viewer` before the empty-role denial branch is reached.
- Step 2 shows the new row with `role='viewer'` and `auth_source='oidc'`.
- Step 6 still shows `role='manager'`. With `OIDC_SYNC_ROLE=false`, `UpsertOIDCUser`'s `DO UPDATE` clause omits `role` from its `SET` list entirely (`db/users.go:289-299`), so a second login for a still-unmapped subject does not revert the admin's manual promotion. E2E-AUTH-08's own sync-back row (its step 7) runs with `OIDC_SYNC_ROLE=true` and a mapped group, so it does not cover this path.

*Teardown:* restore `OIDC_SYNC_ROLE` to the leg's default value and recreate `step-ui`.


#### E2E-AUTH-14: deactivation, demotion and deletion take effect on the next request

*Tier:* PR (e2e-main).

*Objective:* A privilege change reaches a live session immediately, rather than waiting out `SessionMaxLifetime`.

*Preconditions:* each round creates and owns its own disposable manager-role subject. None of the three touches `manager_user` or any other shared fixture: two of the three actions here (deactivate, delete) would otherwise break every later test in the run that depends on that fixture staying usable.

*Steps:* run three independent rounds, each against its own disposable subject, on a fresh manager session in jar A that has just confirmed `GET /issue` returns `200`.

| Round | Admin action | Then, on jar A |
|---|---|---|
| deactivate | `POST /admin/users action=toggle_active` | `GET /issue` |
| demote | `POST /admin/users action=change_role` to `viewer` | `GET /` and `GET /issue` |
| delete | `POST /admin/users action=delete` | `GET /issue` |

*Assertions:*
- **Deactivate:** `302` to `/login`. `SetUserActive` bumps the epoch in the same statement that clears the flag (`db/users.go:122`), and `RequireLogin` additionally rejects any user whose row reports `IsActive` false (`middleware/middleware.go:115`).
- **Demote:** `GET /` returns `200` and `GET /issue` returns `403` with body `403 Forbidden\n`. This is the one round that is **not** a logout. `UpdateUserRole` bumps the epoch (`db/users.go:115`), so the old session is rejected and the user must sign in again; on that new session `RequireRole` reads the role from the user row `RequireLogin` cached in the request context rather than from the cookie (`middleware/middleware.go:142-160`). Assert the `403` after re-login, and assert the `302` to `/login` on the first request after the demotion.
- **Delete:** `302` to `/login`, and **not** a `500`. The row is gone, so `loadUser` returns no user and `RequireLogin` rejects on the existence check before the epoch comparison is reached.
- In all three rounds, the response carries a cleared session cookie.


#### E2E-AUTH-15: a password change evicts other sessions but not the acting one

*Tier:* PR (e2e-main).

*Preconditions:* three disposable subjects it creates and owns, one per password-write path exercised below, none of them a shared fixture, since each round changes that subject's password and would otherwise leave a fixture's credential stale for every later test.

*Steps:*
1. Log the same (first disposable) user in on two independent jars, A and B. Confirm `GET /` returns `200` on both.
2. Change the password on jar A via `POST /profile action=change_password`.
3. Immediately issue `GET /profile` on jar A.
4. Issue `GET /` on jar B.
5. Repeat the whole sequence for an administrator's `POST /admin/users action=reset_password` against a third session on the second disposable subject, and for a completed `POST /reset-password` token flow on the third.

*Assertions:*
- Step 3 returns `200`. The acting session survives its own password change, because the handler re-stamps the session with the freshly bumped epoch straight after bumping it (`handlers/users.go:272-279`). A user who changes their password is not thrown out of the page they used to do it.
- Step 4 returns `302` to `/login`. Every other session the user holds is revoked.
- Step 5 gives the same eviction for the other two password-write paths (`handlers/users.go:142`, `handlers/password_reset.go:200`). Neither of those has an acting session to preserve, so all sessions for the target user are revoked.

### 3.3 RBAC boundaries

Three test users: `viewer_user`, `manager_user`, and the seeded `admin`. Create the first two via `POST /admin/users` as admin with `csrf_token`, `action=create`, `username`, `password`, `role`.

The table below is exhaustive against `main.go`'s router as of `e53236f`. A route missing from it is a route nobody is guarding.

**Authenticated routes.** `RequireRole` writes the body `403 Forbidden\n`, including the trailing newline, at `middleware/middleware.go:151` and `:155`. It reads the role from the user row `RequireLogin` loaded, so a role change applies to the next request rather than to the next login.

| Route | Method | viewer | manager | admin |
|---|---|---|---|---|
| `/`, `/dashboard`, `/api/status` | GET | 200 | 200 | 200 |
| `/certificates`, `/certificates/{id}`, `/history`, `/provisioners` | GET | 200 | 200 | 200 |
| `/profile` | GET | 200 | 200 | 200 |
| `/profile` | POST (valid CSRF) | 302 | 302 | 302 |
| `/profile` | POST (missing CSRF) | 303 | 303 | 303 |
| `/profile/2fa`, `/profile/2fa/qr` | GET | 200 | 200 | 200 |
| `/profile/2fa/start`, `/profile/2fa/disable` | POST (valid CSRF) | 302 | 302 | 302 |
| `/profile/2fa/confirm` | POST (valid CSRF) | 200 on success, 302 otherwise | same | same |
| `/profile/2fa/*` | POST (missing CSRF) | 303 | 303 | 303 |
| `/issue` | GET/POST | 403 | 200 / 302 | 200 / 302 |
| `/renew/{id}` | POST | 403 | 302 | 302 |
| `/import` | GET/POST | 403 | 200 / 302 | 200 / 302 |
| `/download/cert/{id}`, `/download/key/{id}` | GET | 403 | 200 | 200 |
| `/le`, `/le/issue`, `/le/settings`, `/le/logs` | GET | 403 † | 200 †† | 200 †† |
| `/le/issue`, `/le/{id}/renew`, `/le/{id}/delete`, `/le/{id}/autorenew`, `/le/settings` | POST | 403 † | 302 †† | 302 †† |
| `/le/download/cert/{id}`, `/le/download/key/{id}` | GET | 403 † | 200 †† | 200 †† |
| `/revoke/{id}` | POST | 403 | 403 | 302 |
| `/download/ca`, `/download/intermediate-ca`, `/download/full-chain` | GET | 403 | 403 | 200 |
| `/admin`, `/admin/activity`, `/admin/security`, `/admin/about`, `/admin/integrity`, `/admin/backup`, `/admin/console` | GET | 403 | 403 | 200 |
| `/admin/users`, `/admin/users-temp`, `/admin/notifications` | GET | 403 | 403 | 200 |
| `/admin/users/{id}` | GET | 403 | 403 | 200 |
| `/admin/users`, `/admin/users-temp`, `/admin/console`, `/admin/notifications`, `/admin/notifications/test`, `/admin/backup/download` | POST | 403 | 403 | 200 or 302 |

† The `/le/*` routes are registered unconditionally in the manager-role group (`main.go:299-308`), with no gate on any LE env flag, so the viewer `403` cells hold regardless of whether the LE stack exists and E2E-RBAC-01 runs them unconditionally in the PR tier. †† The manager and admin cells require the LE stack to be genuinely meaningful (Section 3.14) and run only in the nightly `le` leg, gated on `E2E_LE_ENABLED`.

**The pair `/certificates/{id}` and `/download/key/{id}`, both as viewer, is the RBAC boundary this table exists to state explicitly: a viewer can see that a certificate exists (`200`, metadata rendered) but cannot obtain its key (`403`).** `CertificateDetails` carries no role gate beyond `RequireLogin`, while `DownloadCert` and `DownloadKey` sit inside the `manager` group (`main.go:256-265`). Neither row states the boundary alone; the two together do.

**Unauthenticated routes.** These must stay reachable with no session cookie at all. A route silently acquiring `RequireLogin` is as much a bug as one silently losing `RequireRole`, and it is the more dangerous of the two here: `RequireLogin` creeping onto `/health` would break the container healthcheck (`docker-compose.yml:118`) and roll the deployment.

| Route | Method | Expected with no session |
|---|---|---|
| `/health` | GET | 200, body `{"status":"ok"}` |
| `/ready` | GET | 200 or 503, always a JSON body, never a redirect |
| `/login` | GET / POST | 200 / 302 or 200 |
| `/forgot-password`, `/reset-password` | GET / POST | 200 |
| `/logout` | GET | 302 to `/login`, ending no session (`main.go:224`) |
| `/logout` | POST | 303 to `/` with no session, since `requireCSRF` runs first (`main.go:225`) |
| `/static/*` | GET | 200. Registered at `main.go:331`, on the root router |
| `/auth/oidc/login`, `/auth/oidc/callback` | GET | 302 when `OIDC_ENABLED=true`, 404 when not registered |


#### E2E-RBAC-01: the route-by-role matrix, driven as data

*Tier:* PR (e2e-main).

*Preconditions:* the three test users. The `/le/*` rows split as marked in the table above: the viewer `403` cells run unconditionally, since `RequireRole` rejects before any LE-specific code runs regardless of the flag. The manager and admin cells need `E2E_LE_ENABLED=1` and the LE stack (Section 3.14); this PR-tier entry skips those cells with the reason `LE stack not enabled` when the flag is unset, and they instead run as part of the nightly `le` leg's roster (Section 1.3, 4.2), which lists them explicitly so they run somewhere.

*Steps:* one parameterised test iterating both tables. Authenticate as the role in question, or as nobody, issue the request, record the status and the body.

*Assertions:*
- The status in the cell, and for `403` cells the exact body `403 Forbidden\n`.
- **Every `200` cell additionally carries one content assertion.** A `200` alone proves only that a handler was reached, and this application renders its error paths inline at `200`, so a handler that failed on every request would satisfy a status-only matrix completely. One representative substring per route: `/certificates` contains the certificates table header, `/admin/security` contains a known audit row, `/provisioners` contains the configured provisioner name. Pick each substring so that a rendered error page fails it.
- The pair of `/certificates/{id}` and `/download/key/{id}`, both as viewer, is the RBAC boundary called out above the table; assert both cells even though each is otherwise an unremarkable row, since together they are the property that matters most in this matrix.

*Residue:* the POST rows in the table create state. `/issue`'s manager and admin cells each issue a certificate named `rbac-matrix-<role>`, distinct from any name E2E-CERT-01 or E2E-CSRF-01 uses; `/admin/users`, `/admin/users-temp` and `/admin/console`'s admin cells each create their own scratch user or `console.run` row. None of it is cleaned up: the extra certificates, temporary users and `auth_log` rows are left in place, the same as every other row-creating PR-tier test in this run.

#### E2E-RBAC-02: unauthenticated access to an authed route redirects, it does not 403

*Tier:* PR (e2e-main).

*Steps:* request `/`, `/issue` and `/admin` with no session cookie.

*Assertions:* `302` with `Location: /login` on all three. `RequireLogin` (`middleware/middleware.go:69-130`) redirects, it does not write `403`. A `403` here means the two middlewares have been transposed.

### 3.4 Certificate lifecycle

**Certificate names must satisfy `safeName`**, which allows only `[A-Za-z0-9._-]+` (`handlers/pathsafe.go:116-133`). A name of the form `e2e-server-EC:P-256` is rejected with `Invalid certificate name: ...contains disallowed characters...` rendered inline at `200`, and every issuance in the matrix below would fail before reaching the CA. Use `e2e-server-ec-p256`.

**Assert on issued material, never on the database row.** `IssuePost` writes `policy.KeyType` and `policy.Duration` into the `certificates` row (`handlers/certs.go:212-213`), and `Renew` copies `KeyType`/`IssueDuration` from the previous row (`:236-244`). Both echo the *request*. A build in which `IssueCertificate` ignored `KeyType` and always returned an EC P-256 key, which is precisely the Risk R4 regression this section exists to catch, would write a byte-identical row and pass a row-based assertion. Only the downloaded file is evidence.


#### E2E-CERT-01: issuance matrix

*Tier:* PR (e2e-main) for the seven-combination cross, plus the four-duration axis. Nightly (`bootstrap-extra`) for the full sixteen-combination cross as regression insurance, hosted on that leg's own disposable stack rather than a dedicated leg of its own (Section 1.3).

*Objective:* Exercise two independent risk axes, key generation and CSR shape on one, policy normalisation on the other, without paying for their cross product.

*Matrix, PR tier.* Eleven issuances.

| Purpose | Name | Template | Domain | Duration | Key type |
|---|---|---|---|---|---|
| key axis | `e2e-server-ec-p256` | `server` | `e2e-server.example.com` | `8760h` | `EC:P-256` |
| key axis | `e2e-server-ec-p384` | `server` | `e2e-server.example.com` | `8760h` | `EC:P-384` |
| key axis | `e2e-server-rsa2048` | `server` | `e2e-server.example.com` | `8760h` | `RSA:2048` |
| key axis | `e2e-server-rsa4096` | `server` | `e2e-server.example.com` | `8760h` | `RSA:4096` |
| policy axis | `e2e-internal-ec-p256` | `internal` | `e2e-internal.example.com` | `87600h` | `EC:P-256` |
| policy axis | `e2e-wildcard-ec-p256` | `wildcard` | `*.e2e-wildcard.example.com` | `8760h` | `EC:P-256` |
| policy axis | `e2e-client-ec-p256` | `client` | `e2e-client.example.com` | `8760h` | `EC:P-256` |
| duration axis | `e2e-dur-720h` | `server` | `e2e-dur.example.com` | `720h` | `EC:P-256` |
| duration axis | `e2e-dur-4380h` | `server` | `e2e-dur.example.com` | `4380h` | `EC:P-256` |
| duration axis | `e2e-dur-8760h` | `server` | `e2e-dur.example.com` | `8760h` | `EC:P-256` |
| duration axis | `e2e-dur-87600h` | `server` | `e2e-dur.example.com` | `87600h` | `EC:P-256` |

The seven-combination cross preserves both axes. The cut from sixteen to seven is **not** a runtime optimisation: an RSA-4096 keygen costs a median of well under two seconds even on a small runner, and all sixteen would add roughly twenty seconds. The reasons to cut are tail risk against the server's 60-second `WriteTimeout` (`main.go:404`) and repeated load on a CA that other tests in the same job depend on being responsive. The full sixteen-combination cross runs nightly against the `bootstrap-extra` leg's own disposable stack (Section 1.3), where it has no other job's CA load to compete with, rather than against the long-lived `e2e-main` stack.

The duration axis is separate and is the only reason a duration regression is detectable at all. `allowedIssueDurations` is `720h`, `4380h`, `8760h`, `87600h` (`handlers/cert_ops.go:69-71`), but three of the four templates default to `8760h` and only `internal` defaults to anything else (`:62-67`). A cross product over templates therefore requests just two distinct durations, so a build with a hardcoded duration passes twelve of sixteen combinations, and the four that would catch it are the ones sitting exactly on the CA's maximum. Four `server` certificates at four distinct durations is independent of both the template and the CA maximum.

*Order of execution.* Run `e2e-internal-ec-p256` **first, as a boundary probe**. Its `87600h` request equals `STEPCA_MAX_TLS_CERT_DURATION`'s default exactly, and `validityValidator` rejects only when the requested duration *exceeds* the maximum, so an exactly-equal request is expected to pass. It sits on the boundary, which is why it runs first: if the boundary behaves differently from the expectation, every other row fails for the same reason and the failure message should say so. The CA's one-minute backdate may or may not be supplying margin here. That has not been confirmed, and no assertion depends on it.

*Steps, per row:* `POST /issue` with `csrf_token`, `name`, `domain`, `template`, `key_type`, `duration`. Then `GET /download/cert/{id}` and `GET /download/key/{id}` as manager. Request timeout at least 120s, so that a 60-second cut is reported as the `WriteTimeout` finding it is rather than as a client timeout.

The eleven rows run in `api`, posting the form directly. That is the right level for the issuance contract, but it bypasses the page's JavaScript entirely, so it carries a `ui` companion.

*`ui` companion.* In the browser, click each of the four template cards on `/issue` in turn and read back the hidden `template`, `key_type` and `duration` inputs (`issue.html`'s picker). Assert each card sets the values the handler reads, and that the four templates produce four distinct triples. A field-name drift between the JavaScript and `normalizeIssuePolicy` would leave every `api` row green while the real form silently submitted a default.

*Assertions, per row:*
- `302` to `/issue` with flash `Certificate <name> for <domain> issued (<key_type>)!`.
- `openssl x509 -in certificate.crt -noout -text` parses without error.
- `openssl x509 -noout -subject -ext subjectAltName` shows `CN=<domain>` and a `subjectAltName` extension whose **`DNSNames` list has length exactly 1** and equals `[<domain>]`. This is Risk R4's CSR-shape half. "Contains the domain" is not sufficient and must never be substituted, because it is satisfied by both a CN-only CSR and a DNSNames-only CSR, which are exactly the two wrong shapes the plan names. The code under test sets `Subject.CommonName = domain` and `DNSNames = []string{domain}` (`stepca/issue.go:68-71`).
- The public-key algorithm, curve and modulus size read out of the **downloaded certificate** match the requested `key_type`.
- `openssl rsa -in private.key -check -noout` or `openssl ec -in private.key -check -noout` succeeds.
- Loading the downloaded certificate and key together as a TLS key pair succeeds. This cross-checks two artifacts produced by different code paths using a third-party parser.
- `NotAfter - NotBefore` is within a minute of the requested duration.

*Cross-row assertions, evaluated once over the whole matrix:*
- The four key-axis certificates yield **four distinct** public-key parameter sets: P-256, P-384, RSA-2048, RSA-4096. A build stuck on EC P-256 fails here and nowhere else.
- The four duration-axis certificates yield **four distinct** `NotAfter - NotBefore` values, ordered as requested.

*Teardown:* leave the certificates in place for E2E-CERT-04, E2E-CERT-05 and E2E-CERT-10, which consume them. Revoked certificates keep both their files and their rows, so E2E-CERT-07's scan will not find them.


#### E2E-CERT-02: the wildcard template rejects a non-wildcard domain

*Tier:* PR (e2e-main).

*Steps:* `POST /issue` with `template=wildcard` and `domain=not-a-wildcard.example.com`.

*Assertions:* **`200` with the error rendered inline**, not a redirect. `IssuePost` sets `data["Msgs"]` and calls `h.render` on the policy-error branch (`handlers/certs.go:175-178`). The rendered page contains `Policy error: wildcard template requires domain like *.example.com` (`normalizeIssuePolicy`, `handlers/cert_ops.go:77-96`). No new row in `certificates` and no new file under `CertsDir`.


#### E2E-CERT-03: an invalid domain is rejected before it reaches the CA

*Tier:* PR (e2e-main).

*Objective:* Assert a **negative wire event**: a malicious identifier must never reach the CA library, let alone the network.

*Steps:*
1. Record the CA's log offset: `docker compose logs --no-color --timestamps step-ca | wc -l`.
2. **Positive control.** Issue one valid control certificate named `e2e-control-<runid>` for `e2e-control.example.com`.
3. Assert the CA's log gained at least one line recording the control certificate's signing request. This proves the observable exists in this environment before anything is concluded from its absence. Whatever pattern is chosen here is the pattern step 6 searches for, so the two must be literally the same expression.
4. Record the new offset.
5. `POST /issue` with `domain=--foo`, then again with `domain=; rm -rf /`.
6. Assert the CA's log gained **zero** lines beyond the offset from step 4.

*Assertions:*
- Each request in step 5 returns `200` with the error rendered inline, containing `Error: identifier "--foo" starts with '-': possible flag injection` for the first and `contains disallowed characters` for the second (`validateIdentifier`, `handlers/identifiers.go:20-31`, reached from `issueCert` at `handlers/cert_ops.go:121`).
- Step 3 passes. If it does not, the test fails as inconclusive rather than reporting success.
- Step 6 finds no new CA-side activity.

Without steps 1 to 4 this test passes identically when the CA's request logging is off, when the search pattern could never match, and when the log rotated between the two reads.


#### E2E-CERT-04: renew

*Tier:* PR (e2e-main).

*Preconditions:* an active certificate from E2E-CERT-01, distinct from the one E2E-CERT-05 will revoke. Note its `id`, its serial read from the **file**, and its `expires_at`.

*Steps:* `POST /renew/{id}` with `csrf_token` as manager. Then read `/certificates`, the `certificates` table, and the on-disk certificate.

*Assertions:*
- `302` to `/certificates` with flash `Certificate renewed`.
- **`Renew` inserts a new row, it does not update the old one.** `handlers/certs.go:252-258` calls `appdb.InsertCert`. The previous row survives with its now-stale serial while the file on disk holds the new certificate. Assert on the **newest** row for that name, and read the serial from the file rather than from either row.
- The file's serial differs from the pre-renewal serial, and its `NotAfter` is later.
- The renewed certificate's key type and validity window, read from the file, match the original's. `Renew` reuses the stored `KeyType`/`IssueDuration` and falls back to `EC:P-256`/`8760h` only for rows predating those columns (`handlers/certs.go:236-244`).
- A `cert_history` row with action `renew` exists.

*Teardown:* none. The stale pre-renewal row, the new row and the `cert_history` row are intentional residue: E2E-CERT-13 and E2E-HIST-01/02/03 read the certificate suite's accumulated rows by count or by delta rather than assuming a clean slate.


#### E2E-CERT-05: revocation is enforced CA-side on reuse

*Tier:* PR (e2e-main). Runs in `api`, whose container is on `step-network` and is therefore the only place `step-ca:9443` is directly reachable.

*Objective:* Risk R7. `Revoke()` returning `nil` is not evidence, and neither is the flash text, which any dial error or missing file also produces.

*Preconditions:* two active certificates from E2E-CERT-01 with their `certificate.crt` and `private.key` available to the harness. Call them `victim` and `control`. Neither may be the certificate E2E-CERT-04 renewed.

*Steps:*
1. `POST /revoke/{id}` for `victim` with `csrf_token` as admin.
2. From the project's container, issue a client-certificate-authenticated `POST https://step-ca:9443/renew` presenting `control`'s certificate and key, and record the status.
3. The same call with `victim`'s certificate and key.
4. `docker compose logs --no-color --timestamps step-ca | grep -F 'path=/revoke'`.

*Assertions:*
- Step 1 returns `302` to `/certificates` with flash `Certificate revoked`, and the row's `status` becomes `revoked`.
- **Step 2 is the positive control** and must return `201` with a new certificate in the body. It proves the mTLS renewal endpoint is reachable and that a live leaf is accepted, so that step 3's rejection means what it claims.
- **Step 3 returns `401`** and the response body names the revoked serial. This is the assertion the test exists for. Assert on the HTTP status, never on any UI flash.
- Step 4 finds at least one line. `smallstep/certificates`' request logger (`logging.LoggerHandler`, vendored at `logging/handler.go`) writes one `key=value` line per request in its default text format, and the revoke route is mounted unprefixed at `POST /revoke` (`api/api.go:334`), so `path=/revoke` is the exact, CA-version-stable substring rather than the word "revoke", which also matches unrelated log content such as `Revoke()` error wrapping.

*Failure-triage note:* a network error and a CA rejection both fail step 3. The failure message must include the raw curl exit code, the HTTP status and the response body so the two are distinguishable without a re-run.

*Not covered:* the half of R7 that says the serial presented for revocation must match the peer certificate is unreachable through the UI at all: `stepca/revoke.go` always derives the serial from the presented leaf, so a mismatched pair cannot be constructed through any UI route. That half belongs in a unit test.


#### E2E-CERT-06: import by upload

*Tier:* PR (e2e-main).

*Steps:* generate a certificate and key out of band, once with EC and once with RSA. `POST /import` as multipart with `csrf_token`, `action=upload`, `name`, `domain`, `cert_file`, `key_file` (`templates/import.html:95,146`).

*Assertions:* `302` to `/import?tab=upload` with flash `Certificate <name> uploaded!`, note the exclamation mark. The stored `key_type` is `EC` for the first and `RSA` for the second. `getCertKeyType` returns one of `EC`, `RSA`, `Unknown`, or the empty string when the file cannot be read or parsed (`handlers/cert_ops.go:201-223`). It never returns a curve-qualified value such as `EC:P-256`, and asserting one is a guaranteed failure.

*Teardown:* none. The two imported certificates are left in place; nothing later in the run reads them by name, so their residue is harmless.


#### E2E-CERT-07: import by scan

*Tier:* PR (e2e-main).

*Objective:* Prove the scan finds unregistered material and deduplicates registered material. `No new certificates found` is what both a working deduplication and a scan that does nothing produce, so the pre-state has to be established.

*Preconditions:* issue two certificates through `/issue`, then delete their rows directly with `psql`, leaving the files under `CertsDir` intact. This is cheaper and more reliable than copying files in, and it guarantees the material is well-formed and parseable.

*Steps:*
1. Read the two serials from the files. Assert **neither is present** in `certificates`.
2. `POST /import` with `csrf_token`, `action=scan`.
3. `GET /certificates`.
4. `POST /import` with `action=scan` again.

*Assertions:*
- Step 1's absence check passes, establishing the pre-state.
- Step 2 flashes `Found and imported: 2`, with the exact count, not "at least one".
- Step 3's listing gained precisely those two names and nothing else.
- Step 4 flashes `No new certificates found`. `scanExistingCerts` filters on serial via `appdb.GetCertBySerial` (`handlers/cert_ops.go:225-254`, the `GetCertBySerial` call at `:244`).

*Teardown:* none. The two re-registered certificates are left in place.


#### E2E-CERT-08: import by manual path, with traversal rejected

*Tier:* PR (e2e-main).

*Steps:*
1. `POST /import` with `csrf_token`, `action=manual`, `name`, `domain`, `cert_path=../../../../etc/passwd`.
2. The same with a **relative** path to a real certificate under `CertsDir`, for example `cert_path=e2e-server-ec-p256/certificate.crt`.

*Assertions:*
- Step 1 returns **`200` with the error rendered inline**, containing `Invalid certificate path:` (`handlers/certs.go:514-517`, using `containedPath` from `handlers/pathsafe.go:48`). No row created.
- Step 2 returns `302` to `/import?tab=manual` with flash `Certificate <name> imported`. Note there is **no exclamation mark** here, unlike the upload path's message.
- Do not use an absolute path for the success case. `containedPath` rejects anything for which `filepath.IsAbs` is true (`handlers/pathsafe.go:68`), so an absolute path fails for a reason unrelated to what step 2 is testing.

*Teardown:* none. Step 2's imported row is left in place; it points at `e2e-server-ec-p256`'s existing material rather than new material, so it adds a row without adding a file.


#### E2E-CERT-09: downloads

*Tier:* PR (e2e-main).

*Steps:* `GET /download/cert/{id}` and `GET /download/key/{id}` as manager. `GET /download/ca`, `GET /download/intermediate-ca`, `GET /download/full-chain` as admin.

*Assertions:*
- `Content-Disposition: attachment; filename=...` matching the handler: `home-ca-root.crt`, `home-ca-intermediate.crt`, `home-ca-full-chain.crt`, and `<safename>.crt`/`.key` for the per-certificate routes.
- `full-chain` is the intermediate PEM followed directly by the root PEM (`handlers/certs.go:313-337`). Verify with `openssl crl2pkcs7 -nocrl -certfile full-chain.crt | openssl pkcs7 -print_certs -noout`, which must list exactly **two** certificates, intermediate first.
- The key download writes an audit row. E2E-SEC-02 asserts its contents.


#### E2E-CERT-10: the certificate-detail page's own validations

*Tier:* PR (e2e-main).

*Objective:* `GET /certificates/{id}` is the only place the application verifies that a leaf actually chains to the intermediate and root, that the private key matches the certificate's public key, and that the domain matches the SAN (`handlers/cert_details.go`, `buildCertDetail` and `validateCertificateChain`). E2E-CERT-01 parses issued material but never verifies it against the chain, so this is the real oracle for the trust half of Risk R4.

*Steps:* `GET /certificates/{id}` for a certificate issued in E2E-CERT-01, and for a certificate imported in E2E-CERT-06 whose issuer is not this CA.

*Assertions:*
- For the issued certificate, the rendered validations include `Hostname/IP match` as `ok`, `Private key pair` as `ok` with detail `private key matches certificate public key`, and `CA chain` as `ok` with detail `certificate verifies against intermediate/root CA`.
- For the foreign imported certificate, `CA chain` is `err`. This is the negative control that makes the positive result meaningful, and it also confirms the page does not simply report `ok` for everything.


#### E2E-CERT-11: duration normalisation and the CA's maximum

*Tier:* PR (e2e-main). Behind the Section 3.0.4 barrier.

*Steps:*
1. `POST /issue` with `template=server` and `duration=100000h`, a value not in `allowedIssueDurations`.
2. `POST /issue` with `template=internal` and `duration=87600h` after recreating `step-ca` (`docker compose up -d step-ca`) with `STEPCA_MAX_TLS_CERT_DURATION=8760h`.

*Assertions:*
- Step 1 **succeeds**, and the issued certificate's validity window is the template default of `8760h`. `normalizeIssuePolicy` silently ignores a duration outside the allowlist rather than rejecting it (`handlers/cert_ops.go:86-88`). That is the current contract and this test pins it, so that a future change to reject instead is a deliberate one.
- Step 2 **fails**, rendered inline at `200` with `Error:` followed by the CA's `requested duration of %v is more than the authorized maximum certificate duration of %v`. The CA rejects rather than clamping, which is the same behaviour Section 2.7.4 relies on.

*Teardown:* restore `STEPCA_MAX_TLS_CERT_DURATION` and recreate `step-ca` (`docker compose up -d step-ca`), which is sufficient because `scripts/step-ca-bootstrap.sh` re-patches the claim on every start.


#### E2E-CERT-12: the domain-suffix policy is unrestricted by default and binds both issue and renew

*Tier:* PR (e2e-main). Recreates `step-ui` between phases, so it takes the Section 3.0.4 barrier.

*Objective:* Section 6.6. `ALLOWED_DOMAIN_SUFFIXES` is a mechanism whose default reproduces the old behaviour, so both halves need asserting: that an empty key restricts nothing, and that a set key is enforced everywhere a name reaches the CA.

*Preconditions:* run in two phases against the long-lived stack, applying `compose.e2e-config.yml` and recreating `step-ui` (`docker compose up -d step-ui`) between them so the `ALLOWED_DOMAIN_SUFFIXES` edit actually reaches the container.

*Phase 1, key unset.*
1. As `manager_user`, `POST /issue` with `domain=login.microsoftonline.com`, template `server`.
2. Read the startup log.

*Assertions:*
- Step 1 returns `302` with the success flash and a certificate whose SAN is `login.microsoftonline.com`. Unrestricted is the documented default, and this asserts that an upgrade changes no behaviour until an operator opts in.
- The log contains `ALLOWED_DOMAIN_SUFFIXES is unset: certificate issuance is unrestricted and any manager can have the CA sign any name` exactly once (`main.go:154-156`). An operator running without the key is told so.

*Phase 2, `ALLOWED_DOMAIN_SUFFIXES=example.com`.*

| # | Request | Expected |
|---|---|---|
| 1 | `POST /issue` `domain=example.com` | succeeds. An exact match is inside the policy |
| 2 | `POST /issue` `domain=a.b.example.com` | succeeds. A subdomain at any depth is inside |
| 3 | `POST /issue` `domain=*.example.com` | succeeds. A wildcard is judged by the name under its `*.` prefix |
| 4 | `POST /issue` `domain=evil-example.com` | **refused.** The label-boundary check is the point: `HasSuffix(name, "."+suffix)` does not match, so a lookalike registered by somebody else cannot pass |
| 5 | `POST /issue` `domain=login.microsoftonline.com` | refused |
| 6 | `POST /renew/{id}` for the certificate issued in phase 1 | **refused.** This is the assertion that matters most |

*Assertions for phase 2:*
- Rows 1 to 5 (the `/issue` refusals) return **`200` with the error rendered inline**, containing `Error: domain "…" is not covered by ALLOWED_DOMAIN_SUFFIXES (example.com)`. `checkDomainPolicy` is reached from `issueCert` (`handlers/cert_ops.go:126`), so its error surfaces on `IssuePost`'s generic issuance-failure branch.
- Row 6 (the `/renew/{id}` refusal) instead returns **`302` to `/certificates`**, since `Renew` flashes and redirects on every failure branch rather than rendering inline (`handlers/certs.go:266-269`). Assert the flash reads `Error: domain "…" is not covered by ALLOWED_DOMAIN_SUFFIXES (example.com)`, and that the on-disk certificate and its `certificates` row are byte-for-byte and field-for-field unchanged from before the attempt. Row 6 is the regression guard for the placement decision: the check sits in `issueCert` rather than in `normalizeIssuePolicy` because `Renew` never calls the latter, so a check in the obvious-looking place would have left renewal as an open bypass.
- Row 4 must be asserted explicitly rather than folded into row 5. A naive `strings.HasSuffix(name, suffix)` passes rows 1, 2, 3 and 5 and fails only row 4.

*Teardown:* restore `ALLOWED_DOMAIN_SUFFIXES` to empty and recreate `step-ui`.

*Not covered:* anything a caller who bypasses the UI can do. This key constrains what the application asks the CA to sign, not what the CA will sign. A holder of the provisioner password talking to step-ca directly is bound only by an x509 `allow`/`deny` block in `ca.json`, which is step-ca configuration and outside this application.


#### E2E-CERT-13: an import name collision destroys the existing certificate

*Tier:* PR (e2e-main).

*Objective:* `importUpload` writes to `CertsDir/<safeName(name)>/certificate.crt` with `os.Create`, which truncates (`handlers/certs.go:436-448`, calling `saveUploadedFile` at `:444` and `:450`; the function itself is `handlers/cert_ops.go:256-265`). There is no collision check against existing names.

*Steps:*
1. Note the SHA-256 of `e2e-server-ec-p256`'s `certificate.crt` and `private.key` on disk.
2. As a manager, `POST /import action=upload` with `name=e2e-server-ec-p256`, a different `domain`, and an unrelated certificate and key.
3. Re-read both files.
4. `GET /certificates/{id}` for the original certificate's row.

*Assertions, stated as the current behaviour:* both files are overwritten, their hashes differ from step 1, and the original row now points at material that is not its own. Step 4's `Private key pair` validation reports `err`, which is the user-visible symptom. If a collision check is added, invert the assertions to a rejection and an unchanged pair of hashes.

### 3.5 Provisioners page

#### E2E-PROV-01: the provisioner list matches the CA's own configuration

*Tier:* PR (e2e-main). Oracle pair with E2E-PROV-02, and runs immediately before it.

*Steps:* `GET /provisioners` as viewer. Cross-check against `docker compose exec step-ca cat /home/step/config/ca.json | jq '.authority.provisioners'`. `jq` is present in the step-ca image, which is what `scripts/step-ca-bootstrap.sh` depends on. There is no `step-ca provisioner list` subcommand, and any step that invokes one will fail.

*Assertions:* every provisioner name and type from `ca.json` appears in the rendered table (`templates/provisioners.html:82-83`). With the stock compose setup that is exactly one entry, named for `PROVISIONER` (default `admin`) with type `JWK`. The page also renders `CAURL`, `RootCert` and `Provisioner` (`handlers/provisioners.go:19-21`). Assert all three against the resolved compose configuration.

#### E2E-PROV-02: the page degrades rather than failing when the CA is unreachable

*Tier:* PR (e2e-main). Oracle pair with E2E-PROV-01. Behind the Section 3.0.4 barrier.

*Steps:* `docker compose stop step-ca`, then `GET /provisioners`.

*Assertions:*
- `200`, not `500` and not an error page.
- The provisioner table is empty.
- **The three configuration values still render.** `provs` stays `nil` when either `h.caClient()` or `caClient.Provisioners()` errors (`handlers/provisioners.go:11-16`), and the template renders nil, a stubbed empty list and a genuinely empty CA identically. Asserting that `CAURL`, `RootCert` and `Provisioner` are still present proves the handler executed and reached the end of its body, which an empty table alone does not.
- Compared against E2E-PROV-01 in the same run, the row count went from N greater than zero to zero. Without that comparison the assertion is satisfied by a CA that never had any provisioners.

*Teardown:* `docker compose start step-ca` and wait for healthy before any other test runs.

### 3.6 History and security log

#### E2E-HIST-01: history pagination

*Tier:* PR (e2e-main).

*Preconditions:* an exact, known total in `cert_history`. Read the current row count `T` directly with `psql`, then `make e2e-seed-history N` to insert exactly `N` synthetic rows so that the total is a value with a partially-filled second page. Do not truncate the table: the genuine `issue`, `renew`, `revoke` and `import` rows written by Section 3.4 are what E2E-HIST-02 and E2E-HIST-03 depend on. Asserting a row count against whatever earlier tests happened to write is a flake generator, which is why the count is computed rather than assumed. `pageSize` is 30 (`handlers/history.go:10`).

*Steps:* `GET /history`, then `GET /history?page=2`.

*Assertions:* page 1 renders exactly 30 entries and reports `TotalPages == ceil((T+N)/30)`. Page 2 renders exactly the expected remainder. The two sets of entry IDs are disjoint.

*Teardown:* none. The `N` synthetic rows stay; they are additive to `T` and every later count in the run is computed against the database at that point rather than against an assumed baseline, so leaving them causes no drift.

#### E2E-HIST-02: the history action filter

*Tier:* PR (e2e-main).

*Steps:* in a single run, `GET /history?action=issue&action=revoke` and `GET /history`.

*Assertions, all three required:*
- The filtered response contains at least one `issue` **or** `revoke` entry. Zero rows satisfies "only issue and revoke were returned" trivially.
- The filtered response contains zero `renew` entries.
- The **unfiltered** response in the same run contains at least one `renew` entry.

Without the third clause the exclusion is unfalsifiable: a broken filter, a broken query and a swallowed database error are indistinguishable, and the error is in fact swallowed (`handlers/history.go:30` discards `GetHistory`'s error).

#### E2E-HIST-03: the history certificate-name filter

*Tier:* PR (e2e-main).

*Steps:* `GET /history?cert=e2e-server-ec-p256` and `GET /history` in the same run.

*Assertions:* the filtered response contains at least one entry and every entry names that certificate. The unfiltered response in the same run contains at least one entry for a **different** certificate. Same reasoning as E2E-HIST-02.

#### E2E-SEC-01: security-log pagination, search and filter

*Tier:* PR (e2e-main).

*Objective:* At this test's slot in the Section 3.0.3 order (after the fixtures, auth smoke tests and the certificate suite, before ADM/BAK/HLTH), nothing has deliberately produced a failed login, and nothing has seeded enough rows to guarantee a second page exists. Both are manufactured in-test rather than assumed.

*Preconditions:* a dedicated throwaway user it creates for its own seeding, distinct from E2E-AUTH-02/03's lockout user. `POST /logout` bumps `session_epoch` for every session the acted-on user holds (`handlers/auth.go:222`); seeding rows by repeatedly logging the shared `admin` fixture in and out would evict the worker-scoped admin fixture the rest of the run depends on (Section 4.1.4), so this test never logs `admin` out.

*Steps:*
1. `POST /login` once with the dedicated user's username and a wrong password, from this test's own session context, to guarantee at least one `success=false` row exists. This is the same shape of attempt E2E-AUTH-02 makes in bulk later; a single one here does not touch its five-attempt budget or that pair's own dedicated user.
2. `GET /admin/security` unfiltered and read `TotalOK`/`TotalFail` (`appdb.GetAuthStats`) to compute the current row total.
3. If the total is under 31, generate enough additional benign login/logout round trips **as the dedicated user**, not `admin`, to bring it to at least 31, so that a second page exists. Re-read the total after seeding.
4. `GET /admin/security`, then `?page=2`, then `?q=<username>`, then `?filter=ok` and `?filter=fail`, then `?filter=` unfiltered.

*Assertions:*
- Same pagination contract as history, `pageSize=30`. `?page=2` returns the remainder of the total computed in step 3, never an empty or out-of-range page, because step 3 guaranteed the total exceeds one page.
- `TotalOK` and `TotalFail` sum to the unfiltered total.
- `filter` accepts only `ok` and `fail` (`db/authlog.go:39-45`). Any other value behaves as unfiltered, so assert `filter=garbage` returns the unfiltered set rather than an error or an empty set.
- `filter=ok` returns at least one row and zero rows with `success=false`, and `filter=fail` returns at least one row, the one step 1 produced. Both directions, as with history.
- `q=` searches username **or** IP with `ILIKE` (`db/authlog.go:35`). Successful logins record `r.RemoteAddr` **including the port** while failed logins record the bare IP, so an IP search matches both forms and a search for an exact `ip:port` string matches only successes.
- Entries render with the correct labels drawn from `securityEventLabel` (`handlers/audit.go:25-43`), restricted to the labels this test's slot can actually produce: `Login` and `Logout` from the fixtures and the auth smoke tests that already ran (Section 3.0.3 items 1 to 2), and `Audit` from E2E-CERT-09's key-download row in the certificate suite (item 3). Do not assert `2FA`, `Reset` or `Denied` here: those labels come from E2E-AUTH-04 through E2E-AUTH-09 and E2E-AUTH-02/03, all of which run later in the order.

Do not assert here that the bulk failed-login rows from E2E-AUTH-02 are present. That test runs after this one by design, and its own assertion covers them.

#### E2E-SEC-02: audited privileged actions carry the `Audit:` prefix and the right payload

*Tier:* PR (e2e-main).

*Steps:* perform one key download (E2E-CERT-09) and one admin-console run (E2E-ADM-01), noting the certificate id, name and domain and the command id. Then `GET /admin/security`.

*Assertions:* both rows carry label `Audit` and badge `warn` (`auditPrefix = "Audit: "`, `handlers/audit.go:10,30-31,50-51`). Assert the payload **field by field**, not with a prefix match:
- `certificate.key_download id=<the exact id downloaded> name=<the exact name> domain=<the exact domain>` (`handlers/certs.go:385`).
- `console.run id=app.version command=... exit=0 timeout=false duration=<non-empty>` (`handlers/admin_console.go:237`).

A prefix match on a string the test itself caused to be written round-trips a `HasPrefix` and passes when the audit line was written for a different certificate entirely.

#### E2E-SEC-03: the provisioner password never appears on any UI surface

*Tier:* PR (e2e-main).

*Objective:* R9 is the only risk in the migration plan with no other mapped verification, and the leak surfaces are cheap to observe.

*Preconditions:* the harness knows the value of `secrets/ca_password`, which it generated. Call it the canary.

*Steps:* fetch and search each of these surfaces:

1. The cumulative `step-ui` log capture (Section 2.6), not a live `docker compose logs step-ui`, since earlier recreates in this run have already truncated the live view.
2. `docker compose logs step-ca`, in full. `step-ca` is never recreated mid-run, so its live log is already complete.
3. The output of all ten admin-console commands.
4. `GET /admin/about`.
5. `GET /admin/integrity`.
6. `GET /api/status`.
7. `GET /admin`.
8. The backup bundle's `manifest.json`.

*Assertions:*
- The canary appears in **none** of them.
- **Positive control:** the same search applied to `docker compose exec step-ui cat /opt/step-ui/data/provisioner_password` finds it. That proves the search is capable of finding the string in this environment, which is what makes the negatives above informative.

*Not covered:* the other half of R9, that the password byte slice is zeroed after `Token()` returns, is structurally unobservable from outside the process. It belongs in a unit test over `stepca`, and no black-box assertion can stand in for it.

#### E2E-SEC-04: the log artifact is safe to publish

*Tier:* PR (e2e-main). Runs between artifact collection and upload, and fails the job on a hit.

*Objective:* This is E2E-SEC-03's canary sweep pointed at the CI artifact rather than at the UI, and it is load-bearing for the pipeline rather than for the product. GitHub's `::add-mask::` masks the live log stream only. It does not touch the contents of an uploaded file. Without this test, publishing per-service logs and a database dump on every failure is a credential-disclosure mechanism.

*Steps:* after the collector has assembled the artifact directory but before `upload-artifact` runs, grep every file in it for: the `ca_password` canary, the `secret_key` canary, the `postgres_password` canary, the value of `STEPUI_ADMIN_PASSWORD`, and any `totp_secret` value present in the database at that moment.

*Assertions:*
- Zero hits across the artifact directory.
- **The canaries are expected to be present in the volume tarballs**, which is exactly why those tarballs are never collected as artifacts. Assert that `secrets/` is absent from the artifact tree and that no `*.tgz` from a backup bundle was collected.
- Positive control as in E2E-SEC-03: the same grep run against a file the collector deliberately excluded must find the canary.

#### E2E-SEC-05: the backup bundle is CA-key-equivalent and gated accordingly

*Tier:* PR (e2e-main).

*Objective:* `POST /admin/backup/download` returns the CA's root and intermediate private keys and the users table. Its role gate is E2E-RBAC-01's matrix row for this route; this entry keeps only the bundle-content assertions that row cannot make.

*Steps:* `POST /admin/backup/download` as admin. Extract the result.

*Assertions:*
- The admin bundle's `postgres-stepui.sql` contains the `users` table's `totp_secret` column values **in plaintext**. Assert this positively. It is a true statement about the artifact and the reason the previous assertion matters, and a future change that encrypts them at rest should have to update this test deliberately.
- On the stock stack, `step-ca-data.tgz` is always produced (Section 2.7.5), but truncated at `/home/step/config`, and never reaches `/home/step/secrets` (`filepath.WalkDir` visits `certs/`, then `config/`, then `secrets/` in lexical order and aborts on the first walk error). Assert positively that it contains `certs/`, and do **not** assert that it contains `secrets/root_ca_key` against this stack: that file is out of reach behind the same permission denial E2E-BAK-01 records in `manifest.json`'s `warnings`. If a future fix to the read-only mount permissions lets the walk complete, this row should be revisited to assert `secrets/root_ca_key` positively.

#### E2E-SEC-06: sensitive pages are not cacheable

*Tier:* PR (e2e-main). Carries the skip-until-fixed contract from Section 1.2: `test.skip()` with reason `Cache-Control: no-store not yet added, see Section 1.2` until the fix lands, rather than running red inside the required `e2e-gate` check.

*Steps:* issue a `HEAD` request against `/certificates/{id}`, `/download/key/{id}`, `/admin/users`, `/admin/backup` and `/profile/2fa`, and read the response headers.

*Assertions:* each response's `Cache-Control` header **contains** `no-store`, not an exact match, so a longer legacy-compatible value (`no-store, no-cache, must-revalidate`) still satisfies it. The header is **currently absent** on all five, which is why the test skips rather than asserts until the decided one-line addition to `mw.SecurityHeaders` (Section 2.7.4) lands.

### 3.7 Admin console and admin actions

Preconditions for the whole section: logged in as admin.

#### E2E-ADM-01: `app.version` pins the certificates library

*Tier:* PR (e2e-main).

*Steps:* `GET /admin/console`, confirm `app.version` and `ca.health` appear as native entries (rendered without a shell command string, `templates/admin_console.html:35,44`). `POST /admin/console` with `csrf_token` and `command_id=app.version`. Then `GET /admin/security`.

*Assertions:*
- `Result.Success=true`, `Result.ExitCode=0`.
- The **second** line of the output matches `^smallstep/certificates v0\.30\.2$` exactly (`appVersionNativeFn`, `handlers/admin_console.go:168-182`). This is the only runtime-observable check that the pinned library version in the built image is the intended one, which is Risk R3, and it changes the moment a dependency bump lands.
- Do **not** assert on the first line. The Dockerfile passes no `-X` ldflags, so `Version`, `BuildDate` and `GitCommit` are their compile-time defaults and asserting them tests a constant. For the same reason, `!= "unknown"` on the library version is too weak: it passes against any resolved version at all, including a downgrade.
- `/admin/security` gains a row containing `console.run id=app.version` with a non-empty `duration=` field. That row is the black-box shadow of Risk R6's allowlist invariant: a native command special-cased outside the common wrapper would still produce output but would emit no such row.

#### E2E-ADM-02: `ca.health` with the CA up

*Tier:* PR (e2e-main). Oracle pair with E2E-ADM-03.

*Steps:* `POST /admin/console` with `command_id=ca.health`, step-ca running.

*Assertions:* `Result.Success=true`, `Output` exactly `ok`. On its own this passes against a stubbed `Health()` that always returns nil. E2E-ADM-03 in the same run is what makes it meaningful.

#### E2E-ADM-03: `ca.health` with the CA down

*Tier:* PR (e2e-main). Oracle pair with E2E-ADM-02. Behind the Section 3.0.4 barrier.

*Steps:* `docker compose stop step-ca`, then `POST /admin/console` with `command_id=ca.health`.

*Assertions:*
- `Result.Success=false`, `Result.ExitCode=1`, and the output contains a dial-level error such as `connection refused` or a timeout.
- The output is **not** `CA client unavailable`. `h.caClient()` caches the client after its first success (`handlers/handler.go:103-117`), so by the time this test runs the client already exists and the failure necessarily comes from `Health()`, not from construction. `CA client unavailable` here would mean the cache had been lost, which is a different bug. E2E-BOOT-09 is where that string is the expected result.
- No panic, no `500`. This is the R2 property observed through the full HTTP stack rather than through a `FakeCA`.

*Teardown:* `docker compose start step-ca`, wait for healthy.

#### E2E-ADM-04: the OS diagnostic commands still run

*Tier:* PR (e2e-main).

*Steps:* run each of `system.date`, `system.hostname`, `system.identity`, `system.disk`, `system.processes`, `app.files`, `openssl.version`, `postgres.ready`.

*Assertions:*
- Every one returns `Success=true` and `ExitCode=0`. Assert this in a loop.
- `postgres.ready`'s output contains `accepting connections`.
- `system.identity`'s output names uid 10001, matching `USER stepui` in `Dockerfile:48`.

Nothing else about these outputs is asserted. "Sane output" for a date, a hostname or a process list has no falsifiable form, and asserting that `openssl.version` reports a real build string tests the contents of a base image this project does not build.

#### E2E-ADM-05: an unknown `command_id` is rejected and audited

*Tier:* PR (e2e-main).

*Steps:* note the maximum `auth_log` id. `POST /admin/console` with `csrf_token` and `command_id=rm.rf`. `GET /admin/security`.

*Assertions:* the page renders `Unknown command. Only allowlisted commands may be run.` and a new `auth_log` row above the recorded id contains `console.denied command_id=rm.rf` (`handlers/admin_console.go:227,229`). No `console.run` row is created for this attempt.

#### E2E-ADM-07: the `/admin/about` preflight

*Tier:* PR (e2e-main).

*Objective:* `preflight` assembles a substantially larger check list than `caIntegrity` (`handlers/health.go:171-213` against `:216-231`). E2E-HLTH-06 covers only the `caIntegrity` subset, which is the CA API check plus the chain, config, password-sync and image-pin groups. Everything else in `preflight`, including the database ping, the file and directory checks, the disk checks and the session-cookie check, is unexercised.

*Steps:* `GET /admin/about` (`AdminAboutGet`, `handlers/admin.go:92-99`, which is the handler that actually calls `h.preflight`; `AdminGet` at bare `/admin` is the unrelated dashboard overview and never calls it). Capture the full check list as an ordered name-to-status list and compare it against the list calibrated per Section 2.7.5.

*Assertions:*
- The check list is, in order, exactly the 19 rows calibrated in Section 2.7.5: `PostgreSQL`, `Step-CA API`, `Root CA certificate`, `Intermediate CA certificate`, `Provisioner password file`, `UI TLS certificate`, `UI TLS private key`, `Issued certificates directory`, `Upload directory`, `CA config`, `Root CA integrity`, `Intermediate CA integrity`, `Full chain`, `Provisioner password sync`, `step-ca image pin`, `Disk space`, `Disk space`, `Disk space`, `Session cookie`. Assert the ordered list, never a count and never a name-keyed map, so that a silently dropped or reordered check is caught.
- On the stock stack: `CA config` and `Provisioner password sync` report `warn` (both trace to `ca.json` being unreadable under the read-only mount); every other row reports `ok`, except `Session cookie`, which reports `ok` only when `SESSION_SECURE=true` and `warn` otherwise. Assert the value that matches the stack under test.
- Rows 16 to 18 all carry the literal name `Disk space` and cannot be told apart by name. Match them by position in the ordered list (or by their detail text, which names the filesystem each checks), never by looking up `"Disk space"` as a unique key.
- `checkCAConfig` returns early on the same `EACCES` that produces its `warn` row, so none of its downstream duration rows render on the stock stack; the 19-row list above already reflects that, it is not a subset of a larger list with rows hidden.
- `checkStepCAImagePin` agrees with the `STEP_CA_IMAGE` the stack was actually started with.

#### E2E-ADM-08: the non-create user actions

*Tier:* PR (e2e-main).

*Objective:* Cover the five `UsersPost` actions other than `create`, plus a validation gap on the `create` path. `unblock_ip` is the operator remedy for the E2E-AUTH-03 lockout.

*Steps and assertions,* each as admin with a valid `csrf_token`, against a disposable subject user:

| Action | Steps | Assertions |
|---|---|---|
| `change_role` | promote the subject viewer to manager, re-read `/admin/users` | the listed role changed. `UpdateUserRole` also bumps `session_epoch`, so the subject's live sessions are revoked; E2E-AUTH-14 asserts that half |
| `toggle_active` | deactivate, re-read, reactivate | the active flag flips both ways and the row survives |
| `reset_password` | reset the subject's password, then log in as the subject with the new value | login succeeds with the new password and fails with the old one |
| `unblock_ip` | with `target_ip` set to an arbitrary scratch address, since nothing is blocked at this test's slot in the Section 3.0.3 order | the action succeeds against the form's own contract: `302`/`303` with a success flash, and an `/admin/security` row recording the action. The live unblock, against a real blocked address, is exercised by E2E-AUTH-03's teardown, which calls the same `action=unblock_ip` (`handlers/users.go:125-131`) against the IP its own preceding test actually blocked; this row does not repeat that live case |
| `delete` | delete the subject, re-read `/admin/users` | the row is gone and a login attempt as that user fails |
| `create` with an invalid role | `role=nonsense` | see below |

Run the `delete` case last, since it destroys the subject.

**The invalid-role case.** `POST /admin/users` with `action=create` and `role=nonsense` is refused. `UsersPost` checks `appdb.ValidRole` before it hashes anything (`handlers/users.go:52`) and flashes `Role must be one of: viewer, manager, admin`. Assert three things: no row is created, **the message renders on `/admin/users` itself**, and the same rejection applies to `action=change_role` (`:92`). The second of those is the live check on Section 6.12's `admin_base.html` fix, since before it this error appeared on whatever non-admin page the administrator visited next. `db/users.go` enforces the same allowlist under the handlers, in `CreateUser`, `UpdateUserRole` and `UpsertOIDCUser`, so a future caller that skips the handler check still cannot write a bad role.

### 3.8 Backup

#### E2E-BAK-01: the bundle is valid, complete and self-verifying

*Tier:* PR (e2e-main). Must not run against the `compose.e2e-fingerprint.yml` stack, for the reason given in Section 2.7.1.

*Steps:* `POST /admin/backup/download` with `csrf_token` as admin. Save the body as `backup.tgz` and extract it.

*Assertions:*
- `Content-Type: application/gzip` and `Content-Disposition: attachment; filename="step-ca-ui-backup-<timestamp>.tgz"`.
- `tar tzf backup.tgz` lists `manifest.json`, `postgres-stepui.sql`, `step-ca-data.tgz`, `step-ui-data.tgz`, `step-ui-certs.tgz` and `step-ui-uploads.tgz`. `step-ca-data.tgz` is present as a file even though it is not a `components` entry, below.
- `manifest.json` has `format == "step-ca-ui-backup-v1"`. Its `components` object has exactly four entries on the stock stack, per Section 2.7.5's calibration: `postgres` (`postgres-stepui.sql`, `ok`), `step-ui-data` (`step-ui-data.tgz`, `ok`), `step-ui-certs` (`step-ui-certs.tgz`, `ok`), `step-ui-uploads` (`step-ui-uploads.tgz`, `ok`). `step-ca-data` is deliberately **not** among them; see below.
- `manifest.json`'s `warnings` array is exactly `["step-ca-data failed: open /home/step/config: permission denied"]`.
- **For each of the four `components` entries, recompute SHA-256 over the extracted file and compare it against the manifest's recorded value.** This is the only assertion in this suite that is fully independent of the code under test: it re-derives the property from the artifact rather than asking the application to confirm its own claim. `step-ca-data.tgz` has no manifest digest to check against on this stack, since the walk that produces it never completed; see the next point for what to assert about it instead.
- `step-ca-data.tgz` is **present but incomplete**, not absent: extracting it finds the `certs/` subtree but not `config/`. `filepath.WalkDir` visits `/home/step` in lexical order (`certs`, `config`, `secrets`, ...) and aborts on the first error, which is the `EACCES` on `/home/step/config`; `secrets/` is never reached either. Assert `certs/` is present and `config/` is absent inside the extracted `step-ca-data.tgz`, and do not assert a SHA-256 match for it, a `components` entry for it, or that it is missing from the tarball altogether.
- `postgres-stepui.sql` contains `COPY` or `INSERT INTO` statements for `certificates`, `users` and `cert_history`, and is plain SQL readable by `psql` rather than a custom-format dump.
- `/admin/security` gains a row containing `backup.download filename=...` (`handlers/backup.go:67`).

#### E2E-BAK-03: a backup bundle actually restores

*Tier:* nightly (`bootstrap-extra` leg), on its own disposable stack.

*Objective:* E2E-BAK-01 proves the bundle is internally consistent, that every component's hash matches the manifest's recorded value. Nothing else in this suite proves the bundle is actually restorable, which is the gap this entry closes, on the procedure `README.md` documents ("How do I back up and restore the data?"): unpack the archive, restore the PostgreSQL dump, and restore the Docker volumes.

*Preconditions:* a source stack with a known admin password. On a fresh source stack the `certificates` table is empty and `step-ui-certs.tgz` in any bundle taken from it is empty too, so this entry must **seed at least one issuance via `/issue`** on the source stack before it takes the backup it goes on to restore: that issuance is what puts a row in `certificates` and the cert plus key into `step-ui-certs.tgz`. Download the bundle from the source stack exactly as E2E-BAK-01 does, after the seed issuance. Fresh, empty named volumes for a second, disposable stack, none of them shared with the source stack.

*Steps:*
1. Untar the bundle's volume components (`step-ca-data.tgz`, `step-ui-data.tgz`, `step-ui-certs.tgz`, `step-ui-uploads.tgz`) directly into the fresh named volumes. Per Section 2.7.5, all four are present as files on the stock stack; `step-ca-data.tgz` is truncated (`certs/` present, `config/` absent), which this step restores as-is, incomplete, matching what a real restore from this stack actually has to work with.
2. Bring up `postgres` alone on the fresh stack and `psql` the bundle's `postgres-stepui.sql` dump into it.
3. Bring up the rest of the stack (`docker compose up -d --wait`).
4. Log in as admin with the source stack's known admin password.
5. `GET /certificates` and locate the row for the seeded issuance's certificate by name.
6. `GET /ready`.

*Assertions:*
- Step 4 succeeds: the restored `users` table carries a working password hash for admin.
- Step 5 finds the seeded certificate's row, restored from the dump, with its file present under the restored `step-ui-certs` volume.
- Step 6 returns `200` with `{"status":"ready"}`.

*Teardown:* `docker compose down -v` on the disposable restore stack. The source stack and its bundle are untouched throughout, since this test only ever reads from them.

### 3.9 Health and readiness transitions

Every test in this section runs behind the barrier defined in Section 3.0.4.

#### E2E-HLTH-01: `/health` is unconditional

*Tier:* PR (e2e-main). Behind the Section 3.0.4 barrier.

*Steps:* `GET /health` with everything up, then with step-ca stopped, then with postgres stopped.

*Assertions:* `200` with body exactly `{"status":"ok"}` in all three cases. `Liveness` performs no database and no CA check (`handlers/health.go:21-25`).

*Not covered:* the both-stopped case. Reaching it requires killing postgres after step-ui is already running and then also stopping step-ca, which adds no observable beyond the two single cases.

*Teardown:* restart both `step-ca` and postgres, and wait for both healthy, since the steps above leave neither running.

#### E2E-HLTH-02: `/ready` with everything healthy

*Tier:* PR (e2e-main). Oracle pair with E2E-HLTH-03.

*Steps:* `GET /ready`.

*Assertions:* `200`, body exactly `{"status":"ready"}`.

#### E2E-HLTH-03: `/ready` reports the CA down

*Tier:* PR (e2e-main). Oracle pair with E2E-HLTH-02. Behind the Section 3.0.4 barrier.

*Steps:* `docker compose stop step-ca`, then `GET /ready`.

*Assertions:* `503`, and the body **parsed as JSON** yields `status == "not ready"`, `db == "ok"`, `ca == "unreachable"`.

Do not string-compare the body. `Readiness` builds it with `json.Marshal` over a `map[string]string` (`handlers/health.go:53-57`), and Go sorts map keys, so the wire order is `ca`, `db`, `status`. Any literal written in the request order will never match.

`"ca":"unreachable"` is returned for **any** `client.Do` error (`checkCAReachability`, `handlers/health.go:97`), so this assertion means "the CA was not reachable" and nothing more specific. E2E-BOOT-05 asserts the same string for an entirely different cause and carries a disambiguating triple for that reason.

This entry leaves `step-ca` stopped. The restore is E2E-HLTH-04's own teardown, not this test's, since E2E-HLTH-04 depends on this end state and would otherwise need to tear it down only to immediately recreate it.

#### E2E-HLTH-04: `/ready` recovers when step-ca comes back

*Tier:* PR (e2e-main). Behind the Section 3.0.4 barrier.

*Preconditions:* runs immediately after E2E-HLTH-03 and depends on that test's end state: `step-ca` stopped, `/ready` already returning `503`. If E2E-HLTH-03 did not just run, stop `step-ca` first.

*Steps:*
1. From the CA-down state, `docker compose start step-ca`.
2. Poll two things every second, in the same loop, recording both: `GET /ready` on the UI, and `docker compose exec step-ca curl -sk https://localhost:9443/health` directly.
3. Bound at 60s.

*Assertions:*
- `/ready` returns `200` with `{"status":"ready"}` within the bound.
- The recorded traces show `/ready` recovering within one second of step-ca's own `/health` first answering. `checkCAReachability` does a live HTTPS GET on every `/ready` call with no caching, so there is no propagation delay beyond the handshake.

Do not "immediately GET" after `docker compose start`. step-ca's healthcheck has a 15-second start period and a 10-second interval (`docker-compose.yml:50-55`), so the container is not ready when `start` returns. Probing step-ca directly inside the same loop is what separates the two failure modes: a slow CA boot and a broken `checkCAReachability` both present as a `503`, and only the direct probe distinguishes them.

*Failure-triage note:* the failure message must include both traces from the dual poll, since a slow step-ca boot and a broken `checkCAReachability` both yield `503` and are otherwise indistinguishable from a bare timeout.

*Teardown:* confirm `step-ca` reports `healthy` at the container level (not only `/ready`'s own view of it) before releasing the Section 3.0.4 barrier. This restores the state E2E-HLTH-03 left stopped.

#### E2E-HLTH-05: `/ready` reports the database down

*Tier:* PR (e2e-main). Behind the Section 3.0.4 barrier.

*Steps:* `docker compose stop postgres`, then `GET /ready`.

*Assertions:*
- `503` with `db == "unreachable"` in the parsed body, within the 2-second `PingContext` bound (`handlers/health.go:36-40`).
- **Every authenticated request is now refused with `302` to `/login` while the database is down.** `RequireLogin` re-reads the user row on every request and treats a load error as a rejected session (`middleware/middleware.go:114-119`), so a database outage logs everyone out rather than serving stale sessions. That is fail-closed by design and this is the test that observes it. Assert it explicitly, so that a future change to serve stale sessions during an outage has to change this line.
- Sessions come back on the next request once postgres is healthy, without a re-login, provided the cookie has not aged past its idle or absolute limits.

step-ui's own healthcheck also starts failing around this point, because `GET /login` renders a page whose base data touches the database. Capture that interaction rather than treating it as noise.

**Cost note for the reader.** `RequireLogin`'s row re-read is one database query per authenticated request across the whole application. It is the price of the revocation property in Section 6.3.

*Teardown:* `docker compose start postgres`, wait for both services healthy.

#### E2E-HLTH-06: `/admin/integrity` tracks live CA availability and nothing else

*Tier:* PR (e2e-main). Behind the Section 3.0.4 barrier. Must not run against the `compose.e2e-fingerprint.yml` stack, for the reason given in Section 2.7.1.

*Steps:* `GET /admin/integrity` as admin with the CA up, capturing the full check list as an ordered name-to-status-and-detail list. Stop step-ca. Capture it again. Diff the two.

*Assertions:* **exactly one row differs** between the two captures, and it is `Step-CA API`, moving from `ok` to `err`. Every other row is byte-identical in name, status and detail.

Asserting instead that "the other checks stay ok" asserts an environment fact rather than a code property, because those rows depend on the read-only `/home/step` mount being present, which some stacks in this suite deliberately remove. The diff is stable under both.

*Teardown:* `docker compose start step-ca`, wait for healthy.

### 3.10 The UI-cert renewal goroutine

#### E2E-RENEW-01: the background renewer re-issues before expiry, with no downtime

*Tier:* nightly (`renew` leg), in its own disposable stack.

*Blocked on:* the `UI_CERT_DURATION` prerequisite in Section 2.7.4. If that change is not made, delete this test rather than ship it, since with the duration hardcoded the job hangs until the CI timeout on every run. It is the only coverage anywhere for the renewal flow.

*Preconditions:* fresh volumes. Stack composed with `compose.e2e-config.yml`, which is what makes `UI_CERT_DURATION` reach the container (Section 2.5); without it the key is a silent no-op like `ALLOWED_DOMAIN_SUFFIXES` without the same override. `.env`: `UI_TLS_MODE=stepca`, `STEPUI_ADMIN_PASSWORD` set, `UI_CERT_DURATION=6m`. The CA enforces a `minTLSCertDuration` of five minutes by default, so six minutes is the shortest safe value. No change to `STEPCA_*_TLS_CERT_DURATION` is needed, and consequently no fresh volume is needed on the step-ca side either, since `scripts/step-ca-bootstrap.sh` re-patches the claims on every start anyway.

*Steps:*
1. `docker compose up -d --build`, wait for healthy.
2. Confirm `stepca`-mode bootstrap succeeded using E2E-BOOT-01's log assertions.
3. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -dates -serial`. Record `notBefore`, `notAfter` and the serial.
4. `docker compose logs --no-color --timestamps step-ui | grep -F 'UI cert auto-renewer started'`.
5. Poll the same `s_client` probe every 15s, bounded at 6 minutes, recording every observed serial and every handshake outcome.

*Assertions:*
- Step 3's `notAfter - notBefore` is 6 minutes **plus step-ca's own backdate tolerance**, not exactly 6 minutes. `stepca-bootstrap.sh` and the CA both backdate `notBefore` by roughly a minute (the same tolerance E2E-CERT-01 states as "within a minute of the requested duration"), so a `6m` request yields an observed window of around 7 minutes. Assert the observed window is `6m` plus up to a minute of backdate, not equal to `6m`.
- The renewer goroutine started.
- The serial changes at approximately two thirds of the **observed** window from step 3, not two thirds of the nominal `6m` request, and **before** the original certificate's `notAfter`. Deriving the renewal point from the requested duration rather than the observed one would make this assertion fail on the backdate margin alone, for a reason unrelated to the renewer's own correctness. That ordering is the property: a renewer that fires after expiry is a renewer that causes an outage.
- **Every probe in the polling loop completes a handshake successfully**, with no connection error and no expired-certificate error. `certReloader.GetCertificate` re-stats both files on each handshake and reloads on an mtime change (`tlsreload.go`), so the swap is invisible to clients. A single failed handshake in the trace is a real finding.
- `docker compose logs --no-color --timestamps step-ui | grep -F 'UI cert renewed'` shows the line with `nextRenewalIn` of roughly two thirds of the observed window (around 4 to 5 minutes, not a fixed 4).
- If renewal fails, `UI cert renewal failed — will retry` appears with `retryIn=5m0s` (`uiCertRenewFailureBackoff`, `tlsbootstrap.go:35`). Treat its presence as a failure of this test, but capture it, since the backoff path is otherwise unobserved.

*Teardown:* `docker compose down -v`.

### 3.11 CSRF enforcement

Every state-changing `POST` route validates `csrf_token` with `subtle.ConstantTimeCompare` against the session-stored value (`csrfOK`, `handlers/handler.go:316-323`). Two response shapes exist and both are asserted:

- Twenty routes use `requireCSRF`, which flashes `Session error. Please refresh the page.` and redirects with **`303 See Other`** (`handlers/handler.go:325-332`).
- Three routes call `csrfOK` directly and render inline at **`200`**: `POST /login` sets `data["Error"] = "Session error. Please refresh the page."` (`handlers/auth.go:72-77`), `POST /reset-password` does the same (`handlers/password_reset.go:154-157`), and `POST /forgot-password` uses the slightly different text `Session error. Please refresh the page and try again.` (`handlers/password_reset.go:49-53`).

#### E2E-CSRF-01: every POST route rejects a missing and a wrong token

*Tier:* PR (e2e-main).

*Coverage:* all twenty-three POST routes registered in `main.go`: `/login`, `/logout`, `/forgot-password`, `/reset-password`, `/issue`, `/renew/{id}`, `/import`, `/revoke/{id}`, `/admin/users`, `/admin/users-temp`, `/admin/console`, `/admin/backup/download`, `/admin/notifications`, `/admin/notifications/test`, `/profile`, `/profile/2fa/start`, `/profile/2fa/confirm`, `/profile/2fa/disable`, `/le/issue`, `/le/{id}/renew`, `/le/{id}/delete`, `/le/{id}/autorenew`, `/le/settings`.

The route list is derived from the router at test time rather than hardcoded, so a new POST route without CSRF protection fails this test on the day it is added.

*Steps, per route:* authenticate at the tier the route requires, then issue the POST twice with otherwise-valid fields: once with `csrf_token` omitted, once with `csrf_token=wrong-value`.

*Assertions, per route:*
- The expected shape from the table above: `303` to the route's declared redirect target with the session-error flash, or `200` with the inline error.
- The action did not happen. This is asserted per route against the appropriate observable, not by trusting the status code.

*Route-specific side-effect assertions, which do not generalise and are therefore named individually:*

| Route | Additional assertion |
|---|---|
| `/login` | login does not succeed **even with correct credentials**. The CSRF check runs after the rate-limit check and before credential verification (`handlers/auth.go:63-76`). Follow with `GET /` on the same jar, which must redirect to `/login` |
| `/issue` | no certificate row, no file under `CertsDir`, and **no `/sign` request reached the CA**, verified with the same two-phase positive control as E2E-CERT-03: control-certificate issuance, offset, negative case, zero new lines. This control certificate is issued before E2E-CERT-01 runs, since CSRF-01 precedes the certificate suite in the Section 3.0.3 order; the `/renew/{id}` and `/revoke/{id}` rows below reuse its id |
| `/admin/console` | no `console.run` **and** no `console.denied` row in `auth_log` with an id greater than the maximum recorded immediately before the attempt. The id bound is required regardless of execution order: E2E-ADM-01 and E2E-ADM-05 write rows of exactly this shape later in the Section 3.0.3 order, and an unbounded query run after they have would fail spuriously on rows this test never caused. The id bound is what keeps the oracle sound independent of when in the run this row executes |
| `/renew/{id}`, `/revoke/{id}` | both target the control certificate from the `/issue` row above, since no certificate exists at this test's slot otherwise. The certificate's row and its on-disk files are byte-for-byte unchanged after each rejected attempt, and the CA was never contacted for a renewal or revocation, verified against the CA log with an offset |
| `/le/{id}/renew`, `/le/{id}/delete`, `/le/{id}/autorenew` | no Let's Encrypt certificate exists anywhere in the PR-tier run (Section 3.14 is nightly and flagged), so these three target a fabricated id and assert only the CSRF-rejection shape, not a side effect. `requireCSRF` runs before the id lookup in every LE handler (for example `le.go:115` for `LERenew`), so the fabricated id is never looked up and there is no row or file to assert "unchanged" against |
| `/admin/backup/download` | the response body is not a gzip stream |
| `/logout` | the session survives. Follow with `GET /` on the same jar, which must still return `200`. `requireCSRF` sends this one to `/`, not to `/login` (`handlers/auth.go:215-217`) |

#### E2E-CSRF-05: a token from a different session is rejected

*Tier:* PR (e2e-main).

*Steps:* establish two independent sessions in two cookie jars, each with its own token. `POST /issue` using jar B's cookie and jar A's token.

*Assertions:* rejected, with `303` to `/issue` and the session-error flash, and no certificate created. `csrfOK` compares the submitted value against `sess.Values["csrf_token"]` for *that request's* session, so a token minted for a different session must never validate.

The property is stated over two independent sessions, so no single-session bug and no globally shared token can satisfy it. No individual row of E2E-CSRF-01 states that.

### 3.12 Configuration switches, headers and static assets

Twelve environment keys change runtime behaviour. This is the complete list and where each is covered.

| Key | Effect | Covered by |
|---|---|---|
| `ENABLE_HSTS` | `Strict-Transport-Security` value | E2E-CFG-01 |
| `SESSION_SECURE` | `Secure` flag on the session cookie, and one preflight check | E2E-CFG-01 |
| `LOCAL_LOGIN_ENABLED` | whether `POST /login` verifies a credential or redirects to OIDC | E2E-CFG-01, nightly row |
| `USE_HTTPS` | overrides the `os.Stat` TLS auto-detection | E2E-CFG-01, nightly row |
| `TRUST_PROXY` | whether `middleware.RealIP` is installed at all | E2E-CFG-02 |
| `TRUSTED_PROXY_CIDRS` | which socket peers may set a forwarding header. Fatal at boot when `TRUST_PROXY=true` and it is empty or unparseable | E2E-CFG-02, E2E-BOOT-07 case (d) |
| `ALLOWED_DOMAIN_SUFFIXES` | which names the UI will ask the CA to sign. Empty means unrestricted | E2E-CERT-12 |
| `UI_TLS_MODE` | the UI's own certificate source | Section 3.1 in full |
| `CA_FINGERPRINT` | root-cert provisioning by fingerprint fetch | E2E-BOOT-01, E2E-BOOT-05 |
| `CA_ROOT_CERT_PEM` | root-cert provisioning inline | E2E-BOOT-06 |
| `OIDC_ENABLED` | whether the two `/auth/oidc/*` routes are registered at all | E2E-AUTH-08, E2E-RBAC-01's unauthenticated table |
| `PUBLIC_BASE_URL` | whether a password-reset link can be built at all; empty refuses the send rather than deriving an origin from the request | E2E-AUTH-09 |

`Content-Security-Policy`, `X-Frame-Options` and `X-Content-Type-Options` are unconditional rather than switched, and E2E-CFG-01 pins them in the default configuration.

#### E2E-CFG-01: the response-header and config-switch matrix

*Tier:* PR (e2e-main), except the `LOCAL_LOGIN_ENABLED` and `USE_HTTPS` rows, which need override stacks and run in the nightly `oidc-mail` leg. Runs second to last in the long-lived stack, per Section 3.0.3. Behind the Section 3.0.4 barrier: each configuration change recreates `step-ui`.

*Steps:* read the response headers for a representative route from every tier plus `/login`, `/health` and `/static/css/base.css`, once per configuration below. Each configuration change is applied by editing `.env` and **recreating** `step-ui` (`docker compose up -d step-ui`; `restart` reuses the existing container's environment and would leave the previous configuration in effect), and the original configuration is restored the same way in teardown.

*Assertions:* one row per configuration, in the table below.

| Configuration | Assertion |
|---|---|
| default | `X-Frame-Options: DENY` and `X-Content-Type-Options: nosniff` on every response (`middleware/middleware.go:40-41`) |
| default | `Content-Security-Policy` present and **byte-identical** to the literal at `middleware/middleware.go:56-61`, on every response. Compare the whole string, not a substring, and pin `default-src 'self'` with no `unsafe-inline` anywhere |
| default, `ui` companion | loading each page template in the browser produces **zero** console errors and **zero** `securitypolicyviolation` events. The header comparison above proves the policy is the intended string; this proves the intended string does not block an asset the page needs. A directive that breaks a stylesheet changes no status code and no header |
| `ENABLE_HSTS=true` | `Strict-Transport-Security: max-age=31536000; includeSubDomains` (`:45`) |
| `ENABLE_HSTS=false` | `Strict-Transport-Security: max-age=0` (`:47`). The header is present with a zero max-age, not absent |
| `SESSION_SECURE=true` | the session cookie carries `Secure`, and `GET /admin/about` reports the `Session cookie` preflight check as `ok` |
| `SESSION_SECURE=false` | the cookie has no `Secure` flag, the preflight check reports `warn`, and the startup log contains `SESSION_SECURE=false: session cookies will not carry the Secure flag` (`main.go:135`) |
| `LOCAL_LOGIN_ENABLED=false` with OIDC on | `POST /login` redirects to `/auth/oidc/login` instead of verifying a credential (`handlers/auth.go:50-53`). Needs the OIDC override. Starts from the leg's stock `LOCAL_LOGIN_ENABLED=true`; restore it and recreate `step-ui` before this row hands off to the next in the leg |
| `USE_HTTPS=true` with no cert on disk | the process binds the TLS listener and does not exit, and every handshake fails. The flag takes precedence over the `os.Stat` auto-detection (`main.go:394-398`), so `ListenAndServeTLS` starts with `certReloader.GetCertificate`, which has no last-good certificate to fall back on (`tlsreload.go:38-45`) and errors on every call. **Preconditions for this row only:** `UI_TLS_MODE=provided` and a fresh `step-ui-ssl` volume. Without `UI_TLS_MODE=provided`, the default `self-signed` branch generates a working certificate at boot whenever `cfg.SSLCert` is absent (`tlsbootstrap.go:239-243`), which puts a cert on disk before the server ever starts and defeats this row's premise; `provided` is a no-op (`tlsbootstrap.go:207-208`), so the fresh volume stays empty. Because no cert ever exists, `step-ui`'s own healthcheck (`https://localhost:8443/login`, `docker-compose.yml:118`) fails by design for the life of this row: wait for the container to reach `running`, not `healthy`, before proceeding. Assert that the container stays up and that `openssl s_client` fails, not that anything is served. Starts from `USE_HTTPS` unset; no restore step, since this row is terminal for the leg (Section 1.3) and nothing after it depends on the value |

`ENABLE_HSTS` and `SESSION_SECURE` reach the container through `.env`. `LOCAL_LOGIN_ENABLED` needs `compose.e2e-oidc.yml` and `USE_HTTPS` needs `compose.e2e-config.yml`, per Section 2.5.

*Teardown:* restore `ENABLE_HSTS` and `SESSION_SECURE` to the job-level values and recreate `step-ui`.

#### E2E-CFG-02: a forwarding header is believed only from a trusted peer

*Tier:* nightly (`oidc-mail` leg). Needs `compose.e2e-oidc.yml`, which is the override that passes `TRUST_PROXY` and `TRUSTED_PROXY_CIDRS` through. Skips with reason when `E2E_OIDC_ENABLED` is unset. Restarts or recreates `step-ui` between phases, so it takes the Section 3.0.4 barrier.

*Objective:* Section 6.4. The rate limiter and the audit log must key off an address the client cannot choose. The final phase below additionally covers the configuration-failure mode E2E-BOOT-07 case (d) does not reach from the runtime side: that a bad `TRUSTED_PROXY_CIDRS` is fatal is BOOT-07's property; that `TRUST_PROXY=false` boots healthy and ignores a forged header regardless is this entry's.

*Preconditions:* the leg's starting env, with no prior env edit in this run (Section 1.3). `TRUST_PROXY=true`. `TRUSTED_PROXY_CIDRS` set to a block that does **not** contain the project container's address on `step-network`. The requests therefore arrive from an untrusted peer, which is the case that mattered.

*Steps:*
1. Issue twenty failed logins for a valid username, rotating `X-Forwarded-For` through twenty distinct addresses.
2. `make e2e-restart-ui`, so the socket peer's login-attempt count starts fresh for the next variant; `IsBlocked`/`RL.attempts` is a process-local map (`security/security.go`), and every variant below arrives from the same socket peer, so without a restart between them the second and third variants find the peer already blocked from the first and never reach their own fifth-attempt boundary.
3. Repeat step 1 with `X-Real-IP`, then `make e2e-restart-ui` again, then repeat step 1 with `True-Client-IP`.
4. `GET /admin/security`.
5. **Recreate** `step-ui` (`docker compose up -d step-ui`, not `restart`) with `TRUSTED_PROXY_CIDRS` widened in `.env` to include the harness's address. A `restart` reuses the existing container's environment and would leave the narrower list in effect, and the recreate also clears the limiter, which is required regardless since the harness's own address was blocked by the untrusted-peer variants above.
6. Re-run step 1's pattern against the recreated stack, with `X-Forwarded-For: 203.0.113.7, <harness address>`.
7. **Final phase (Section 3.0.6).** Recreate `step-ui` again with `TRUST_PROXY=false` and `TRUSTED_PROXY_CIDRS` unset.
8. Against that recreated stack, issue five failed logins for a valid username with a forged `X-Forwarded-For` header.

*Assertions:*
- The lockout fires on the **fifth** attempt in every one of the three header variants in steps 1 and 3. `clientFromHeaders` returns early unless the socket peer is itself inside a trusted block (`middleware/realip.go:56-59`), so none of the headers is read.
- Every `/admin/security` row from steps 1 and 3 records the **harness's real socket address**, not any forged value.
- Step 6, where the peer *is* trusted, attributes the attempts to `203.0.113.7`. `X-Forwarded-For` is walked right to left with trusted hops skipped, so the first untrusted address wins (`:62-70`). This is the positive control: without it, the assertions above are satisfied by a middleware that ignores headers unconditionally, which would break every real deployment behind a proxy.
- A fifth failed attempt in step 6 blocks `203.0.113.7` and leaves the harness's own address unblocked, proving the limiter really is keyed on the forwarded value.
- Step 7's recreate starts healthy. `TRUST_PROXY=false` installs no `RealIP` middleware at all, so an absent or unparseable `TRUSTED_PROXY_CIDRS` is irrelevant and must not be fatal under this mode; E2E-BOOT-07 case (d) is what pins the fatal behaviour when `TRUST_PROXY=true` instead.
- Step 8's forged header changes nothing: the lockout still fires on the fifth attempt and `/admin/security` records the harness's real socket address, not the forged one.

*Teardown:* recreate `step-ui` with `TRUST_PROXY` and `TRUSTED_PROXY_CIDRS` restored to the leg's starting values before the next row in the `oidc-mail` leg runs (Section 1.3).

#### E2E-STATIC-01: static assets are served with correct MIME types and resist traversal

*Tier:* PR (e2e-main).

*Objective:* `mimeByExt` exists (`main.go:46-65`) because the distribution `mime.types` in a minimal image maps `.css` to `text/plain`. A regression there leaves every page returning `200` with correct-looking bytes while the browser refuses to apply the stylesheet, and no status-code assertion anywhere in this suite would notice.

*Steps:* read the response headers for one asset of each served extension. Then, over a raw TCP socket rather than through an HTTP client library, issue a request line reading literally `GET /static/../templates/base.html HTTP/1.1`, plus the same for `/static/%2e%2e/templates/base.html` and `/static/....//templates/base.html`. Also request `/static/css/base.css` in the same run as an in-subtree positive control.

*Assertions:*
- The `Content-Type` for each in-subtree asset equals the value `mimeByExt` maps its extension to, exactly. The positive control returns `200`, proving the raw-socket request path itself works.
- Every traversal attempt returns `404` or `400`, never `200`, and never any byte of `templates/base.html`. `main.go`'s `//go:embed templates static` (`:40-41`) embeds `templates/` as a sibling of `static/`, not under it, so it is a real file that a genuine boundary escape could reach, unlike `main.go` itself, which is Go source and is never embedded at all: a target that cannot exist in any FS the binary holds would 404 whether or not the traversal defence works, which is why `templates/base.html` is the target here rather than `main.go`. The handler serves from `fs.Sub(embeddedAssets, "static")` (`main.go:326-331`), so a traversal escape would be a serious regression.
- The raw-socket delivery is required, not incidental: a request built through a normal HTTP client or `fetch` may normalise `/static/../templates/base.html` to `/templates/base.html` before it ever leaves the client, in which case the server never sees a literal `..` in the request line and the test would silently stop exercising the traversal defence it claims to.

### 3.13 Temporary users

#### E2E-TEMP-01: a temporary user is handed over by one-shot token and expires on the ticker

*Tier:* PR (e2e-main).

*Objective:* the temp-user expiry goroutine (`main.go:338-347`) is the second background goroutine in the application and the only one whose period is short enough to observe in real time. It has no coverage.

*Steps:*
1. `POST /admin/users-temp` as admin with a role, a note and the shortest available expiry.
2. Follow the `303` redirect to `/admin/users-temp?cred=<token>` (`handlers/admin_temp.go:145`) and capture the username and password from the **rendered page body**. No cookie carries them; the credential handover is server-side one-shot state, spent by `tempCreds.take` on this GET (`handlers/temp_creds.go:49-63`).
3. Log in as the temporary user and confirm access at its tier.
4. Set the row's `expires_at` into the past directly with `psql`.
5. Poll `/admin/users-temp` every 5s, bounded at 90s.

*Assertions:*
- The temporary user is created with the requested role, and a role value outside `admin`/`manager`/`viewer` falls back to `viewer` (`handlers/admin_temp.go:90-92`).
- Within 90s of step 4, the row is reported expired and `docker compose logs --no-color --timestamps step-ui | grep -F 'temp users expired'` finds the line, with a `count` of at least 1.
- The bound is 90s rather than 60s because the ticker's phase relative to step 4 is arbitrary.

*Assertions on the credential handover* (Section 6.7):
- The POST returns **`303 See Other`** to `/admin/users-temp?cred=<token>`. Post/redirect/get is preserved, so re-issuing the GET does not create a second temporary user. Assert the user count is unchanged after a refresh.
- **No `new_temp_cred` cookie is set**, and neither the generated password nor the token appears in any `Set-Cookie` header. Positive control for that absence: the session cookie *is* present on the same response, so the header was read.
- The redirected GET renders the username and password exactly once. **A second GET of the identical URL renders neither**, because `take` deletes the entry before it checks the expiry (`handlers/temp_creds.go:49-63`). Assert the second response contains the username of the new temporary user in the list table but not its password.
- A GET with a fabricated `?cred=` value renders no credential and does not error.
- Neither the token nor the generated password appears in `docker compose logs --no-color --timestamps step-ui`, in `auth_log`, or in the `users` row. E2E-SEC-04's canary sweep covers the artifact side of the same property.
- The error paths of `POST /admin/users-temp` render their flash on `/admin/users-temp` itself, each exactly once. Section 6.12 records the defect this corrects.

#### E2E-TEMP-02: an expired temporary admin loses access immediately

*Tier:* PR (e2e-main).

*Objective:* Expiry ends a temporary account's access, not merely its row's active flag. Use a temporary **admin**, since that is the case where the gap mattered.

*Steps:* as E2E-TEMP-01 but with its own fresh `role=admin` temporary user, keeping that user's session in jar A and confirming `GET /admin` returns `200` before expiry. Before setting the row's `expires_at` into the past, record the current `docker compose logs --no-color --timestamps step-ui | grep -cF 'temp users expired'` count, since E2E-TEMP-01 already emitted this line earlier in the Section 3.0.3 order and a bare presence check would pass immediately without waiting for this row's own occurrence (Section 3.0.5). Set `expires_at` into the past, then poll the same grep until its count exceeds the recorded baseline. That new occurrence is the gate: only once the count has increased does this test issue `GET /admin` on jar A again, since it is the only observable confirmation the ticker has actually run against this row rather than a still-pending one, or against the row E2E-TEMP-01 already expired.

*Assertions:*
- The pre-expiry request returns `200`. Positive control.
- The post-expiry request, issued after the new `temp users expired` occurrence is observed, returns `302` to `/login`. `ExpireOverdueTempUsers` sets `is_active = false` and `session_epoch = session_epoch + 1` in one statement (`db/users.go:371`), so `RequireLogin` rejects the live session on both its inactive-user check and its epoch check.
- The rejection is observed on the **first** request after the new occurrence, not after a further delay.

### 3.14 Let's Encrypt

Behind `E2E_LE_ENABLED`, skipped with an explicit reason when it is not set, on the same discipline as E2E-AUTH-08. LE issuance needs an ACME endpoint and a solvable challenge, so the flagged stack supplies `pebble` or an equivalent local ACME server plus a DNS or HTTP-01 responder it controls. Do not point these tests at the real Let's Encrypt service.

**Blocked on the `LE_ACME_DIRECTORY_URL` prerequisite in Section 2.7.4.** Without it the application always dials the real Let's Encrypt production (or staging) directory (`le/lego.go:41-42,142-144`), and no test in this section can be written against a local server at all. If that prerequisite is not made, this entire section is deleted rather than shipped red, the same contract E2E-RENEW-01 carries for `UI_CERT_DURATION`. Every entry below assumes `compose.e2e-le.yml` (Section 2.7.1) is composed in, which sets `LE_ACME_DIRECTORY_URL` at the local server, adds `LEGO_CA_CERTIFICATES` so `lego` trusts it, and routes its HTTP-01 challenge to `step-ui`'s port 80.

#### E2E-LE-01: the LE dashboard and settings round-trip

*Tier:* nightly (`le` leg). Skips with reason when `E2E_LE_ENABLED` is unset.

*Steps:* as `manager_user`, `GET /le`, `GET /le/settings`, `POST /le/settings` with an email and a challenge provider, then `GET /le/settings` again.

*Assertions:* `200` on all reads. The posted email and provider persist and re-render. `GET /le/logs` returns `200`.

#### E2E-LE-02: LE issuance against a local ACME server

*Tier:* nightly (`le` leg). Skips with reason when `E2E_LE_ENABLED` is unset.

*Steps:* `POST /le/issue` with a domain the local responder can satisfy. Poll `/le` until the entry is issued.

*Assertions:* the certificate appears on the dashboard, `GET /le/download/cert/{id}` returns parseable PEM, `GET /le/download/key/{id}` returns a key that loads with it as a TLS key pair, and the issuer is the local ACME server's intermediate.

#### E2E-LE-03: auto-renew toggle and delete

*Tier:* nightly (`le` leg). Skips with reason when `E2E_LE_ENABLED` is unset.

*Steps:* `POST /le/{id}/autorenew` to toggle, re-read `/le`, then `POST /le/{id}/delete`.

*Assertions:* the toggle persists and is reflected in the listing. Delete removes both the entry and its material, and the downloads then `404`. All five LE POST routes go through `requireCSRF` (`handlers/le.go:46,115,165,184,245`), so E2E-CSRF-01 already covers their token handling.

#### E2E-LE-04: DNS-provider credentials are never echoed to the client

*Tier:* nightly (`le` leg). Skips with reason when `E2E_LE_ENABLED` is unset.

*Objective:* Section 6, finding V2.

*Steps:*
1. As `manager_user`, which is the lowest tier that can reach the route, `POST /le/settings` with a distinctive canary value in `cf_token` and another in `r53_secret`. The form field is `r53_secret`, not `r53_secret_key` (`templates/le_settings.html:89`, read at `handlers/le.go:290`); the struct field it lands in is `R53SecretKey`, and using the struct name as the form key silently submits an empty value.
2. `GET /le/settings` and search the **raw response body** for both canaries.
3. `POST /le/settings` again with those two fields left blank and everything else changed.
4. `GET /le/settings` and confirm the provider settings still work.

*Assertions:*
- Step 2 finds **neither** canary anywhere in the raw response body. `templates/le_settings.html` renders no `value` attribute for `cf_token` or `r53_secret`, and both carry `placeholder="leave blank to keep existing"` and `autocomplete="new-password"` (`:53-55`, `:89-91`). `type="password"` would have masked pixels, not bytes, so the absence of the attribute is what is asserted.
- **Positive control for that absence:** search the same response for the LE account email, which **is** legitimately rendered (`:19-21`). If it is not found, the search is broken and the test fails as inconclusive rather than passing.
- Step 4 shows the provider settings still working, and a fresh issuance attempt still authenticates against the DNS provider. `parseLESettingsFields` carries both secrets over from the loaded row when their fields come back blank (`handlers/le.go:270`), and `LESettingsPost` aborts with a flash rather than saving over them if that load fails (`:248-252`).
- `r53_key_id` **does** still render its value (`:74-76`). That is deliberate: an AWS access key ID is an identifier rather than a credential, and it gets the same treatment as `smtp_username`. Assert its presence, so that a future change which hides it is a deliberate one.

### 3.15 Notifications

#### E2E-NOTIF-01: notification settings round-trip and test send

*Tier:* nightly (`oidc-mail` leg). Needs `compose.e2e-mail.yml`. Skips with reason when `E2E_MAIL_ENABLED` is unset.

*Preconditions:* purge mailpit's inbox, or record its current message count as a baseline, before `POST /admin/notifications/test`. Section 1.3's oidc-mail leg note states why: this instance is shared with E2E-AUTH-09, which runs earlier in the leg.

*Steps:* as admin, `GET /admin/notifications`, `POST /admin/notifications` with SMTP settings pointed at mailpit, `GET /admin/notifications` again, then `POST /admin/notifications/test`.

*Assertions:*
- The settings persist and re-render, with the SMTP password **not** rendered back and preserve-on-blank honoured on a second POST that leaves it empty (`handlers/notifications.go:149-156`).
- `POST /admin/notifications/test` delivers exactly one message to mailpit above the precondition's baseline.
- An invalid `smtp_security` value is rejected with `SMTP security must be one of: none, starttls, tls`.

## 4. Automation and CI

### 4.1 The harness

The suite is Playwright Test in TypeScript. `test/e2e/` is its own Node project carrying `package.json`, `tsconfig.json`, `playwright.config.ts`, `fixtures/`, `helpers/` and `specs/`. It is not part of the `step-ui` Go module and not part of the repo-root frontend tooling, so its dependencies never reach `govulncheck` or the `trivy` filesystem scan, both of which gate today.

Specs mirror this document's own sections, one directory per section, so a reader holding a test ID can find its file without an index: `specs/bootstrap/`, `specs/auth/`, `specs/rbac/`, `specs/certs/`, `specs/provisioners/`, `specs/history/`, `specs/admin/`, `specs/backup/`, `specs/health/`, `specs/renewal/`, `specs/csrf/`, `specs/config/`, `specs/temp/`, `specs/le/`, `specs/notifications/`. **Every `test()` title begins with its E2E ID**, so a failure in `junit.xml` names the ID directly and the check UI is usable without cross-referencing.

#### 4.1.1 The three projects

One config, three `projects` selected by `testMatch`. A test's project determines both how it drives the application and where it runs.

| Project | Drives | Owns |
|---|---|---|
| `api` | `APIRequestContext` over HTTP, no browser | the majority of the suite: Section 3.2 auth flows, 3.3 RBAC, 3.4 certificates, 3.6 history and security log, 3.7 admin, 3.8 backup, 3.9 health, 3.11 CSRF, 3.12 config switches, 3.13 temporary users, 3.14 Let's Encrypt, 3.15 notifications |
| `ui` | Chromium | what is genuinely browser-shaped, and nothing else |
| `infra` | Node driving child processes | `docker compose` lifecycle, `openssl s_client`, container log assertions, `psql`, and the fresh-volume bootstrap scenarios of Section 3.1, plus Section 3.10 |

**The `ui` project is deliberately small.** Four tests are in it, and each is there because its property cannot be observed over HTTP:

| Test | Why it needs a browser |
|---|---|
| E2E-CERT-01 | `issue.html`'s template picker sets the hidden `template`, `key_type` and `duration` inputs from JavaScript. A companion spec clicks through the picker once per template and asserts the resulting form values, which is the only check that catches a JavaScript-to-handler field-name mismatch. The eleven-row issuance matrix itself stays in `api` |
| E2E-AUTH-04 | the QR image at `GET /profile/2fa/qr` must decode to the same secret the page renders in its readonly input. That is a rendering property of a PNG |
| E2E-AUTH-11 | logout is a POST from an inline form in both base templates. Whether that form is present, carries a token and submits is a page property |
| E2E-CFG-01 | the CSP header is compared exactly in `api`. The `ui` companion loads each page template with a browser and asserts **zero** console errors and **zero** CSP violation events, which catches a directive that blocks a real asset without changing any status code |

Everything else stays out of `ui`. The admin-console dropdown is server-rendered, so a form POST is equivalent. The RBAC matrix, the CSRF sweep and every certificate assertion are status codes and response bodies, which `APIRequestContext` reports directly and faster.

#### 4.1.2 Two execution contexts

| Project | Runs | Because |
|---|---|---|
| `api`, `ui` | **in a container on `step-network`**, from the harness image built on the pinned `mcr.microsoft.com/playwright` base (Section 2.7.2) | both rate limiters key on the client IP, so a harness on the host is seen as the single docker gateway address and per-test rate-limit isolation is impossible. The OIDC issuer URL must also resolve identically for the application and for the browser, which holds only inside the compose network, and `step-ca:9443` is reachable for E2E-CERT-05's mTLS probe only from there |
| `infra` | **on the host** | it drives `docker compose` and inspects published ports. It asserts on container and TLS state rather than on authenticated request behaviour, so it needs no application-visible identity |

#### 4.1.3 Configuration that is not default

| Setting | Value | Why |
|---|---|---|
| `retries` | `0` in every project | Playwright retries in CI by default. Section 4.7's no-retry policy is deliberate, and a default left in place would quietly defeat it |
| `fullyParallel` | `false` for `api` and `infra` | the execution order and state-bleed analysis in Section 3.0.3 are load-bearing, and one long-lived stack cannot support parallel workers. `ui` may parallelise only where its tests are provably independent |
| `forbidOnly` | `true` in CI | a stray `test.only` fails the run rather than silently shrinking it |
| `timeout`, `expect.timeout` | explicit | auto-waiting covers the browser tier and does nothing for a container healthcheck or a certificate serial, so the bounded-poll helpers of Section 4.7 still exist and still report the last observed value on expiry |
| `trace` | `retain-on-failure` | see Section 4.6 |
| `screenshot` | `only-on-failure` | |
| `video` | `retain-on-failure` | |
| `ignoreHTTPSErrors` | `true`, for both `api` and `ui` | `BASE_URL=https://step-ui:8443`, but the UI's serving certificate never carries a SAN for `step-ui`: the self-signed fallback's SANs are `localhost` and `HOST_IP` (`tlsbootstrap.go:134-144`), and a `stepca`-issued leaf's hostname comes from `resolveUIHostname`, which resolves to `UI_HOSTNAME`, the OS hostname, or `localhost` (`tlsbootstrap.go:189-197`), never to the compose service name the harness dials. Without this the handshake fails on every request, in both projects |
| reporters | `list`, `junit`, `html` | `list` for the job log, `junit` for the check UI, `html` uploaded as an artifact. `playwright.config.ts` pins `junit`'s `outputFile` to land inside the path `actions/upload-artifact` collects, since the two are configured independently and a mismatch silently drops the file the check UI depends on |

#### 4.1.4 Fixtures and helpers

Worker- and test-scoped fixtures the specs depend on:

| Fixture or helper | Purpose |
|---|---|
| role-scoped authenticated context | performs the login flow once for `viewer`, `manager` and `admin` and hands back a context carrying the session. **Standing rule: lazy re-auth.** On any `302` to `/login` from a request made through a fixture-authenticated context, the fixture re-runs its login flow once and retries the request once, rather than surfacing the redirect as the test's own result. This is what makes the fixture safe to keep worker-scoped: nothing in Section 3 logs the shared `viewer`/`manager`/`admin` fixture users out (every logout-exercising test creates and owns a disposable user instead, per each entry's own precondition), but a role change or an unrelated session-epoch bump on the shared user would otherwise silently break every test that runs after it in the same worker |
| CSRF token extraction | pulls `csrf_token` from a fetched page, used by every state-changing request |
| TOTP generation | computes a code with a boundary guard, so a code is never consumed inside its last few seconds. E2E-AUTH-05's replay assertion depends on that guard |
| `psql` query helper | pre-state checks, seeding, and the failure-time dump |
| `docker compose` helper | up, down, stop, start, restart, logs-since, inspect |
| `openssl s_client` helper | returns parsed issuer, subject, SAN list, serial and validity |
| log-assertion helper | takes a since-marker and an **exact** string, since Section 2.4 forbids matching on the ambiguous substring |

**"Jar A" and "jar B"** throughout Section 3 mean two independent, isolated request contexts, each with its own cookie storage. In `api` that is a second `APIRequestContext`; in `ui` a second browser context. Several tests state properties that only hold across two of them, E2E-CSRF-05 and E2E-AUTH-15 most obviously, so the isolation is part of the assertion rather than an implementation convenience.

The skip-with-reason contract of Section 3.0.2 is `test.skip()` with a reason naming the missing infrastructure, so an unavailable override stack is visible in the report rather than silently passing.

### 4.2 Topology

One image build, fanned out to two blocking jobs, fanned in to one required check.

```
image ──┬──→ e2e-main       (one long-lived stack, sequential)
        │
        └──→ e2e-bootstrap  (matrix x5, disposable stacks, fail-fast: false)

  e2e-main ──┐
             ├──→ e2e-gate   (the single required check)
e2e-bootstrap ┘
```

The `e2e-gate` job carries `name: e2e` (Appendix B). `e2e` (the job's display name, not its id) is the only check branch protection names, so adding or renaming a matrix leg never requires a branch-protection edit.

Both jobs block pull requests and pushes to `main`, per Section 1.1.

The bootstrap matrix runs in parallel with `e2e-main` and typically finishes inside its shadow, so it usually costs no additional wall clock; Section 4.2.1 gives the `fingerprint` scenario's realistic cost and the case where that is not quite true. It carries no `paths` filter, since it covers the migration plan's stated blind spot and filtering it would save nothing.

**Nightly** runs a four-leg matrix plus a report job that opens or updates a single tracking issue on failure. Section 1.3's nightly table is the canonical leg roster; this section does not repeat it.

The matrix key in both matrices is a single `scenario` or `leg` string naming a compose override plus a harness selector, never an environment tuple, so adding a leg is one line in one place.

#### 4.2.1 Cost

Re-derived for the Playwright harness, because the dominant term changed. The `mcr.microsoft.com/playwright` base is roughly 1.3GB of compressed layers, and it is now the base of the harness image the `image` job builds and caches rather than an image pulled fresh into each job, which moves that cost out of `e2e-main`'s critical path and into the shared `image` job.

Assumptions: GitHub-hosted `ubuntu-latest`, 4 vCPU. Runner boot and checkout 23s. `npm ci` in `test/e2e` 20s the first time it runs in a job, roughly 8s on a warm npm cache thereafter. Each of the application and harness images re-materialised from its `type=gha` layer cache, 30s apiece. Base-image pulls and `docker compose up -d --wait` to healthy 50s. `down -v` 6s.

**`e2e-main`**, which is the critical path:

| Step | Wall clock |
|---|---|
| runner boot and checkout | 23s |
| materialise the application image, then the harness image, from their layer caches | 60s |
| bring up the stack (`up -d --wait`) | 50s |
| `api` project, excluding the lockout pair (`npm ci` inside the container, 20s, plus the tests) | 4m10s |
| the lockout pair, its own container (`npm ci` again, warm cache, ~8s, plus the two tests) | 40s |
| `ui` project, its own container (`npm ci` again, ~8s, plus four tests and browser launch) | 1m00s |
| artifact collection and the redaction assertion | 15s |
| **total** | **≈ 8m45s** |

Three containers each paying their own `npm ci` is the price of E2E-AUTH-02/03 needing an isolated address and of not guaranteeing cross-project order inside one Playwright invocation (Section 3.0's ordering rules, Appendix B). A warm npm cache inside the harness image keeps the second and third `npm ci` calls cheap. Caching the Playwright base through `actions/cache` as a `docker save` tarball was considered and rejected: restoring 1.3GB from the cache measures within a few seconds of pulling it from the registry, so it trades a well-understood pull for cache-key maintenance and buys nothing; building it into the harness image once per workflow run buys more, since it also carries the docker CLI the containers need.

**`e2e-bootstrap`** runs the `infra` project on the host, five scenarios in parallel with `fail-fast: false`. `infra` launches no browser, so the job sets `PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD=1` and uses `actions/setup-node` rather than the Playwright image. Four of the five scenarios are short:

| Step | Wall clock |
|---|---|
| runner boot and checkout | 23s |
| `setup-node` plus `npm ci`, no browser download | 25s |
| application image from the layer cache | 30s |
| the scenario itself, including `down -v` and bring-up | 1m30s to 2m30s |
| artifact collection | 15s |
| **total, `selfsigned`/`provided`/`ca-down`/`fatals`** | **≈ 3m05s to 4m05s** |

**The `fingerprint` scenario is the long pole and the table above understates it.** It runs three tests (E2E-BOOT-01, E2E-BOOT-05, E2E-BOOT-06) in sequence inside one scenario: a `down -v`, a `step-ca`-only `up --wait` to read the fingerprint, then `step-ui`'s own `up`, and E2E-BOOT-05 alone carries a 90-second retry-exhaustion bound before it fails over. Realistic wall clock for the scenario step is **5 to 7 minutes**, not 1m30s to 2m30s, giving a scenario total of roughly **6 to 8 minutes** once the fixed overhead above is added. `timeout-minutes: 15` on the job still covers it with margin. Because this scenario can run close to or past `e2e-main`'s own critical path (Section 4.2.1's `≈8m45s`), "sits inside `e2e-main`'s shadow" holds on the typical run but is not a guarantee on every run; the two jobs are fanned out in parallel regardless; either can be the one a reviewer is waiting on.

**Per pull request:** `image` (1m warm, 5m cold, now building both the application and the harness image) then `e2e-main` at ≈8m45s then `e2e-gate` at 10s. **Roughly 9m55s warm and 13m55s cold**, taking the repository from about five minutes of feedback to about fourteen at worst. Billed runner-minutes are roughly **33**: nine for `e2e-main`, twenty-one across the five bootstrap scenarios (the `fingerprint` scenario alone accounts for six to eight of those), and the rest for `image` and the gate. The five scenarios each pay their own boot and `npm ci`, which is the price of `fail-fast: false` and of one broken scenario not masking the other four.

### 4.3 Jobs

`image` builds `step-ca-ui:e2e` once and writes the buildx layer cache to `type=gha,scope=e2e`. Both downstream jobs re-materialise the image from that cache rather than recompiling, and then compose with `compose.e2e-image.yml` so that `step-ui`'s `build:` block is never used.

`e2e-main` generates secrets in-job, brings up the long-lived stack with `--wait`, and then runs three separate `docker run` invocations of the harness image, each attached to `step-network`, in this order: the `api` project excluding E2E-AUTH-02 and E2E-AUTH-03, then the `api` project's E2E-AUTH-02/E2E-AUTH-03 pair alone in a second container so they poison their own address rather than the first container's, then the `ui` project. Three invocations rather than one `--project=api --project=ui` command, because Playwright does not guarantee cross-project ordering within a single invocation and `ui`'s four tests depend on fixtures and application state the `api` project establishes; a single command that happened to run `ui` first would fail for a reason unrelated to what it was testing. Between the second and third invocations, the workflow runs an unconditional `make e2e-restart-ui`: the lockout pair's container address is freed when that container exits, and without the restart the `ui` project's own container risks being assigned that same now-unblocked address, which would carry over the lockout pair's rate-limit state. Appendix B carries the exact commands.

`e2e-bootstrap` runs the `infra` project **on the host**, one scenario per matrix entry, with `fail-fast: false` so one broken scenario does not mask the others. Its runner sets `STEPUI_ADMIN_PASSWORD` for every scenario except `fatals` case (b), which requires it absent.

Both jobs collect artifacts and run the redaction assertion under `if: always()`, then upload. `e2e-gate` fails unless both report `success`.

Appendix B carries the workflow file.

### 4.4 Changes to the existing workflows

- **`security.yml`'s `trivy-image` job** should stop calling `docker build` directly (`security.yml:154-155`) and build through `docker/build-push-action` with `cache-from: type=gha,scope=e2e`. That removes a third cold build from the repository's CI and guarantees trivy scans the byte-identical image e2e runs.
- **`lint-meta.yml`'s `style` job** gains the harness: `tsc --noEmit` and `eslint` over `test/e2e`, so it is not the only unlinted code in the repository. The repository already lints `step-ui-go/static/js` with a flat eslint config in that job, so follow that pattern rather than introducing a second style.
- **Branch protection** gains exactly one required check, `e2e`.
- **`test/e2e` is a separate Node project** for the reason given in Section 2.7.2, and is deliberately outside the repo-root frontend tooling.
- e2e must not duplicate build, vet, test, lint, coverage, gosec, trivy or gitleaks. It never compiles the application. It consumes the image the `image` job produced.
- Every `uses:` is pinned to a 40-character commit SHA with a trailing `# vX.Y.Z` comment, matching the convention in all five existing workflows, and reuses the SHAs already present there.
- No inline flow mappings. `.github/.yamllint.yml` extends `default` and overrides only `line-length` and `truthy`, so the default `braces` rule applies at error level with `max-spaces-inside: 0`, and `lint-meta.yml:48` runs yamllint over `.github/`.

### 4.5 Secrets

All three `secrets/*` files and `STEPUI_ADMIN_PASSWORD` are generated **in-job** with `make setup FORCE=1` and `openssl rand`, and registered with `::add-mask::`.

**Never store them as repository secrets.** A stored CA provisioner password is a long-lived credential with no rotation story that is readable by any workflow using `secrets: inherit`, and it protects the CA's private keys.

Mock IdP and mailpit credentials are fixed, visibly fake values committed in the override files. They authenticate nothing real.

`::add-mask::` masks the **live log stream** only. It does not touch the contents of an uploaded artifact. That is why E2E-SEC-04 runs between collection and upload and fails the job on a hit.

### 4.6 Artifacts

Two sources. Playwright emits the first group. The collector emits the second and knows nothing about Playwright, so none of that group changed. `collect.sh` tolerates a dead or absent stack and always writes at least a marker file (Section 2.7.2), so `if-no-files-found: error` is a backstop against the collector itself failing outright, not against a dead stack producing nothing to collect; either way, a silently empty artifact directory fails the job rather than masking the real failure.

**From Playwright:**

- **`junit.xml` from the `junit` reporter,** so triage starts in the checks UI rather than in a log file. Every test title begins with its E2E ID, so the failing row names the ID.
- **Traces, screenshots and video, retained on failure,** plus the `html` report. A trace already carries every request with its headers and body, the response, and for `ui` tests the DOM at each step. That is what makes a failure diagnosable here: this application renders its error paths inline at `200`, so a status code alone never identifies the failure, and the trace carries the rendered body that does.

**From the collector:**

- **Per-service logs** captured with `--no-color --timestamps`. Timestamps are mandatory, since every `slog.Debug` line is invisible and retry counts are inferable only from the timestamps on the surrounding INFO lines. For `step-ui`, `collect.sh` reads the cumulative capture file (Section 2.6), not a live `docker compose logs step-ui`, since a mid-run recreate truncates the live view to the current container alone.
- **`docker inspect` for every container, including `State.Health.Log`.** That is the only place a failing healthcheck probe's own output exists, and `docker compose down` destroys it.
- **A `psql` dump of `certificates`, `cert_history`, `auth_log` and `users` at failure time.**
- **The resolved `docker compose config`.**
- **The live `ca.json`,** since `scripts/step-ca-bootstrap.sh` rewrites its claims on every start and the file on the volume is not the file in the repository.
- **`openssl s_client -showcerts` output and the served leaf PEM.**
- **`.env` with secrets redacted.** `secrets/` is never collected, and neither is any backup bundle.
- Artifact names include `github.run_attempt`, or a re-run fails with a 409 on the existing name.

### 4.7 Flake policy

**No test-level retries.** A green-on-retry end-to-end test is indistinguishable from a real intermittent regression, and catching intermittent behaviour is a large part of why this suite exists.

Retries are permitted only on steps that assert nothing: image pull and cache materialisation. The base-image pull is the most likely flake in the pipeline and is entirely outside the suite.

**Every `sleep N` becomes a poll-until-predicate with a hard bound that reports the last observed value on expiry.** A fixed sleep either wastes time or races, and when it races it produces exactly the kind of failure this policy forbids retrying.

**Quarantine rule:** a PR-tier test that fails twice in a rolling thirty-run window without an intervening code change moves to the nightly tier within one working day, with a tracking issue. It is never left in place under `continue-on-error`. A permanently yellow required job trains everyone to ignore the check, which is worse than having no check.

**Too flaky to gate:** E2E-RENEW-01, because it waits four minutes on a real clock. It is the only test in that category.

**Failure messages that must carry their own evidence,** because a flake and a regression present identically without it:

| Test | Evidence the failure message must include |
|---|---|
| E2E-BOOT-02 | both matched timestamps and the full grep output, since three code paths emit a similar-looking line |
| E2E-BOOT-01, E2E-BOOT-05 | whether the fingerprint override was actually applied. Without it both tests pass without exercising the path they name |
| E2E-HLTH-04 | both traces from the dual poll, since a slow step-ca boot and a broken `checkCAReachability` both yield `503` |
| E2E-CERT-05 | the curl exit code, the HTTP status and the response body, to separate a network error from a CA rejection |

## 5. Traceability

Three tables, because this suite answers to three different things. Table 5.1 answers to the migration's acceptance criteria. Table 5.2 answers to the migration's risk register and says honestly which risks are only partly discharged. Table 5.3 is the project-suite coverage map, which is where the tests that predate no plan bullet belong.

### 5.1 Migration acceptance criteria

Only the runtime-observable criteria from the removed step CLI to `ca` library swap plan appear here. The code-grep, `go build`/`vet`/`lint`/`test` and `go.mod` pinning criteria are CI and code-review concerns and are deliberately unmapped.

| Acceptance criterion | Covering tests | Verdict |
|---|---|---|
| `/health` and `/ready` report CA status correctly with step-ca up and stopped | E2E-HLTH-01, E2E-HLTH-02, E2E-HLTH-03, E2E-HLTH-04, E2E-HLTH-05, E2E-HLTH-06 | discharged |
| `/issue` issues for each template and key type; each pair loads via `openssl`/`tls.LoadX509KeyPair` and matches the requested domain, duration and key type | E2E-CERT-01, with E2E-CERT-10 for chain and key-pair verification | discharged. E2E-CERT-02 and E2E-CERT-11 cover rejection and normalisation, not issuance coverage, and are not credited here |
| Renew and revoke succeed against a live CA, and a revoked serial is rejected on subsequent use, verified CA-side | E2E-CERT-04, E2E-CERT-05 | discharged for the rejection. The routes are `POST /renew/{id}` and `POST /revoke/{id}`. The `/certificates/{id}/renew` and `/certificates/{id}/revoke` spellings do not exist in the router |
| `/provisioners` renders the same provisioner name and type as before | E2E-PROV-01, E2E-PROV-02 | discharged |
| `/admin/console`'s `step version` and `step-ca health` equivalents still run | E2E-ADM-01, E2E-ADM-02, E2E-ADM-03 | discharged, with the caveat that "sane output" is asserted only where it has a falsifiable form |
| Fresh `docker compose up` from empty volumes with `UI_TLS_MODE=stepca` and `CA_FINGERPRINT` set completes bootstrap without the `step` binary in the image | E2E-BOOT-01 | discharged **only under `compose.e2e-fingerprint.yml`**. Under the stock stack this criterion is structurally unverifiable, because the read-only `step-ca-data` mount makes the fingerprint path unreachable |
| `validateIdentifier` is preserved and still enforced before every CSR-bound domain reaches the library | E2E-CERT-03 | discharged, including the negative wire event |
| `entrypoint.sh` contains zero `step` invocations and no TLS-acquisition logic | E2E-BOOT-01 steps 8 and 9 | discharged |

### 5.2 Risk register

| Risk | Covering tests | Verdict |
|---|---|---|
| **R1** the `step`-backed flows move in-process | E2E-BOOT-01 (root fetch and leaf issuance in one boot, no `step` binary, no `step` invocation in `entrypoint.sh`), E2E-BOOT-04 (self-signed generation in-process), E2E-RENEW-01 (renewal) | **conditional.** Four flows actually moved: root fetch, leaf issuance, self-signed generation and renewal. The first three are discharged. Renewal is discharged only if E2E-RENEW-01 is written, which depends on the Section 2.7.4 prerequisite; without it that flow has no coverage. `which step` failing proves the binary is gone, not that the flows moved, which is why the log-line assertions carry this row |
| **R2** CA client construction must never crash the process | E2E-BOOT-09 (construction failure, `CA client unavailable`, and the no-negative-caching property), E2E-BOOT-02, E2E-ADM-03, E2E-PROV-02, E2E-HLTH-03 | **discharged only with E2E-BOOT-09.** The other four exercise a reachable-but-down CA, which is the *call* failure path. R2 is about construction failure on a missing or bad root PEM, which the stock stack makes impossible |
| **R3** `smallstep/certificates` pinned to v0.30.2 | E2E-ADM-01 | discharged. The exact-tag regex on the second output line is the only runtime-observable pin check that exists, and a Dependabot bump changes it |
| **R4-keytype** local key generation must honour the requested key type | E2E-CERT-01 (per-row public-key assertion plus the cross-row distinctness check) | discharged. The distinctness check is what catches the stuck-on-EC-P-256 regression the risk describes |
| **R4-shape** the locally built CSR must match `CreateSignRequest`'s subject and SAN shape | E2E-CERT-01 (`DNSNames` length exactly 1, equal to `[domain]`, plus `CN=domain`) | discharged **only** with the length assertion. "Contains the domain" is satisfied by both a CN-only and a DNSNames-only CSR, which are precisely the two wrong shapes |
| **R5** larger transitive dependency graph | none | out of scope by design. `go mod graph` and the image scanners own this |
| **R6** the console allowlist stays the single definition point | E2E-ADM-01 (the `console.run` audit row with a non-empty `duration`), E2E-SEC-02 (its field-by-field payload) | **partially discharged.** The audit row is a black-box shadow: a native command special-cased outside the shared wrapper would emit no such row. Whether the allowlist is still the only definition point is a code-structure property and is not black-box observable |
| **R7** a revoked serial must be rejected on subsequent use | E2E-CERT-05 | **discharged for the rejection half only.** The other half, that `RevokeRequest.Serial` must match the presented peer certificate, is unreachable through the UI: `stepca/revoke.go` always derives the serial from the presented leaf, so no UI route can construct a mismatched pair. That half belongs in a unit test |
| **R8** test migration must not break the `handlers` package build | none | out of scope by design. `go build` and the coverage gate own this |
| **R9** the provisioner password must not leak from the long-lived process | E2E-SEC-03 (the UI, console and log surfaces, with a positive control), E2E-SEC-04 (the CI artifact) | **partially discharged.** The leak surfaces are covered. The other half, zeroing the `[]byte` after `Token()` returns, is structurally unobservable from outside the process and belongs in a unit test over `stepca` |
| **Plan's named blind spot:** nobody exercises `CA_FINGERPRINT`-from-empty-volume or `UI_TLS_MODE=stepca` | E2E-BOOT-01, E2E-BOOT-05, E2E-BOOT-06 | discharged **only under `compose.e2e-fingerprint.yml`**. Under the stock compose stack all three pass without exercising the path |

**Backup-bundle restorability is not one of the migration's own numbered risks, but E2E-BAK-01 alone left it open.** E2E-BAK-01 proves a bundle is internally consistent; nothing proved it restores. E2E-BAK-03 discharges that gap by actually restoring a bundle onto a fresh stack and confirming admin login, a known certificate row, and `/ready`.

### 5.3 Project-suite coverage by area

The suite is larger than the migration, and this table is the map for all of it, organised by what is being protected rather than by which plan bullet asked for it. Tests that also appear in 5.1 or 5.2 appear here too, because a test can serve both purposes. Every test in Section 3 appears in at least one row.

| Area | Property | Covering tests | Tier and CI job |
|---|---|---|---|
| Bootstrap | four root-provisioning modes, three TLS modes, all terminal states distinguishable | E2E-BOOT-01 to E2E-BOOT-06, E2E-BOOT-09 | PR `e2e-bootstrap` |
| Bootstrap | the deliberate startup fatals | E2E-BOOT-07 | PR `e2e-bootstrap` |
| Bootstrap | graceful shutdown | E2E-BOOT-08 | nightly `bootstrap-extra` |
| TLS lifecycle | the UI's own certificate renews on schedule with no dropped handshakes | E2E-RENEW-01, conditional on Section 2.7.4 | nightly `renew` |
| Authentication | local login, rate limiting, lockout ordering | E2E-AUTH-01, E2E-AUTH-02, E2E-AUTH-03 | PR `e2e-main` |
| Authentication | TOTP enrollment, use, replay rejection, recovery codes, disable | E2E-AUTH-04 to E2E-AUTH-07 | PR `e2e-main` |
| Authentication | federated login, group mapping, role sync | E2E-AUTH-08, E2E-AUTH-16 | nightly `oidc-mail` |
| Authentication | password reset end to end, no user enumeration, single-use tokens, reset rate limiting, refusal with no configured origin | E2E-AUTH-09 | nightly `oidc-mail` |
| Authentication | logout is a CSRF-protected POST, and session revocation via `session_epoch` on logout, deactivation, demotion, deletion and password change | E2E-AUTH-11, E2E-AUTH-12, E2E-AUTH-14, E2E-AUTH-15 | PR `e2e-main` |
| Authorization | the full route-by-role matrix, both directions | E2E-RBAC-01, E2E-RBAC-02 | PR `e2e-main` |
| Identity provisioning | self-rename is impossible and an OIDC upsert cannot take over a local row | E2E-AUTH-13 | nightly `oidc-mail` |
| Certificates | issuance, renewal, revocation, chain and key-pair validation | E2E-CERT-01, E2E-CERT-04, E2E-CERT-05, E2E-CERT-10 | PR `e2e-main`, full matrix nightly `bootstrap-extra` |
| Certificates | input validation, policy normalisation, duration handling | E2E-CERT-02, E2E-CERT-03, E2E-CERT-11 | PR `e2e-main` |
| Certificates | import by upload, scan and manual path, including traversal and collision | E2E-CERT-06, E2E-CERT-07, E2E-CERT-08, E2E-CERT-13 | PR `e2e-main` |
| Certificates | downloads and chain assembly | E2E-CERT-09 | PR `e2e-main` |
| Certificates | the domain-suffix policy: unrestricted default, enforced on issue and renew | E2E-CERT-12 | PR `e2e-main` |
| Provisioners | the CA config is rendered faithfully and degrades without failing | E2E-PROV-01, E2E-PROV-02 | PR `e2e-main` |
| Listings | pagination and filtering, asserted in both directions | E2E-HIST-01, E2E-HIST-02, E2E-HIST-03, E2E-SEC-01 | PR `e2e-main` |
| Audit | privileged actions are recorded with the right payload | E2E-SEC-02 | PR `e2e-main` |
| Secret containment | no credential reaches a UI surface or a CI artifact | E2E-SEC-03, E2E-SEC-04, E2E-SEC-05 | PR `e2e-main` |
| Caching | sensitive pages are not cacheable | E2E-SEC-06 | PR `e2e-main` |
| Admin operations | console commands, preflight, user administration | E2E-ADM-01 to E2E-ADM-05, E2E-ADM-07, E2E-ADM-08 | PR `e2e-main` |
| Backup | bundle completeness, self-verifying integrity, and actual restorability | E2E-BAK-01, E2E-BAK-03 | PR `e2e-main` (E2E-BAK-01), nightly `bootstrap-extra` (E2E-BAK-03) |
| Health | liveness, readiness, recovery, integrity | E2E-HLTH-01 to E2E-HLTH-06 | PR `e2e-main` |
| CSRF | every POST route, plus cross-session token rejection | E2E-CSRF-01, E2E-CSRF-05 | PR `e2e-main` |
| Configuration | headers, HSTS and session flags | E2E-CFG-01 | PR `e2e-main`, two rows nightly `oidc-mail` |
| Configuration | forwarding headers are believed only from a trusted peer, a bad proxy list is fatal, and `TRUST_PROXY=false` is unaffected | E2E-CFG-02 | nightly `oidc-mail` |
| Static assets | MIME correctness and traversal resistance | E2E-STATIC-01 | PR `e2e-main` |
| Temporary users | creation, the one-shot credential handoff, the expiry ticker, and post-expiry access | E2E-TEMP-01, E2E-TEMP-02 | PR `e2e-main` |
| Let's Encrypt | settings, issuance, lifecycle, credential handling including E2E-LE-04's canary | E2E-LE-01 to E2E-LE-04 | nightly `le` |
| Notifications | settings round-trip and test delivery | E2E-NOTIF-01 | nightly `oidc-mail` |

### 5.4 Source file to test

Reverse index. If you changed a file on the left, the tests on the right are the ones that observe it end to end. Files with no e2e observer are listed too, since that is itself the answer.

| Source | Covering tests |
|---|---|
| `main.go` (router) | E2E-RBAC-01, E2E-RBAC-02, E2E-CSRF-01 |
| `main.go` (startup guards, bootstrap block) | E2E-BOOT-01 to E2E-BOOT-07, E2E-BOOT-09 |
| `main.go` (server, shutdown, `useHTTPS`) | E2E-BOOT-08, E2E-CFG-01 |
| `main.go` (`mimeByExt`, `staticHandlerFromFS`) | E2E-STATIC-01 |
| `step-ui-go/static/js/pages/issue.js` | E2E-CERT-01's `ui` companion |
| `main.go` (temp-user ticker) | E2E-TEMP-01 |
| `tlsbootstrap.go` | E2E-BOOT-01 to E2E-BOOT-06, E2E-BOOT-09, E2E-RENEW-01 |
| `tlsreload.go` | E2E-RENEW-01, E2E-CFG-01's `USE_HTTPS` row |
| `config/config.go` | E2E-CFG-01, E2E-BOOT-04, E2E-BOOT-07 cases (d) and (e), E2E-CERT-12, E2E-RENEW-01 (once `UI_CERT_DURATION` is read there, Section 2.7.4) |
| `middleware/middleware.go` (`SecurityHeaders`) | E2E-CFG-01, E2E-SEC-06 |
| `middleware/realip.go` | E2E-CFG-02, E2E-BOOT-07 case (d) |
| `middleware/middleware.go` (`RequireLogin`, `RequireRole`, `UserLoader`) | E2E-RBAC-01, E2E-RBAC-02, E2E-AUTH-11, E2E-AUTH-12, E2E-AUTH-14, E2E-AUTH-15, E2E-TEMP-02, E2E-HLTH-05 |
| `security/security.go` | E2E-AUTH-02, E2E-AUTH-03, E2E-CFG-02. E2E-ADM-08's `unblock_ip` row exercises only the form's shape against a scratch address; E2E-AUTH-03's teardown is the suite's only live exerciser of `security.RL.Clear` against a genuinely blocked address |
| `stepca/bootstrap.go` | E2E-BOOT-01, E2E-BOOT-05 |
| `stepca/client.go` | E2E-ADM-02, E2E-ADM-03, E2E-BOOT-09, E2E-PROV-01, E2E-PROV-02, E2E-HLTH-03 |
| `stepca/issue.go` | E2E-CERT-01, E2E-CERT-04, E2E-CSRF-01 |
| `stepca/revoke.go` | E2E-CERT-05 |
| `handlers/auth.go` | E2E-AUTH-01 to E2E-AUTH-03, E2E-AUTH-05, E2E-AUTH-06, E2E-AUTH-11, E2E-CSRF-01 |
| `handlers/totp.go` | E2E-AUTH-04 to E2E-AUTH-07 |
| `handlers/oidc.go` | E2E-AUTH-08, E2E-AUTH-13, E2E-AUTH-16 |
| `handlers/password_reset.go` | E2E-AUTH-09 |
| `handlers/users.go` | E2E-ADM-08, E2E-AUTH-13, E2E-AUTH-14, E2E-AUTH-15 |
| `handlers/admin_temp.go` | E2E-TEMP-01, E2E-TEMP-02 |
| `models/models.go` (`gob.Register(FlashMsg{})`) | every test asserting flash text, most directly E2E-AUTH-02 |
| `templates/base.html`, `templates/admin_base.html` | E2E-AUTH-02, E2E-AUTH-11, E2E-ADM-08, E2E-TEMP-01, E2E-STATIC-01 (`templates/base.html` is the traversal target its raw-socket probe reaches for) |
| `handlers/certs.go` | E2E-CERT-01 to E2E-CERT-09, E2E-CERT-13, E2E-CSRF-01 |
| `handlers/cert_ops.go` | E2E-CERT-01, E2E-CERT-02, E2E-CERT-03, E2E-CERT-06, E2E-CERT-07, E2E-CERT-11 |
| `handlers/cert_ops.go` (`checkDomainPolicy`) | E2E-CERT-12 |
| `handlers/temp_creds.go` | E2E-TEMP-01 |
| `handlers/cert_details.go` | E2E-CERT-10, E2E-CERT-13 |
| `handlers/identifiers.go` | E2E-CERT-03 |
| `handlers/pathsafe.go` | E2E-CERT-01, E2E-CERT-08 |
| `handlers/health.go` (`Liveness`, `Readiness`) | E2E-HLTH-01 to E2E-HLTH-05 |
| `handlers/health.go` (`preflight`) | E2E-ADM-07 |
| `handlers/health.go` (`caIntegrity`) | E2E-HLTH-06 |
| `handlers/admin_console.go` | E2E-ADM-01 to E2E-ADM-05, E2E-BOOT-09 |
| `handlers/backup.go` | E2E-BAK-01, E2E-BAK-03, E2E-SEC-05, E2E-BOOT-08, E2E-CSRF-01 |
| `handlers/audit.go`, `handlers/seclog.go` | E2E-SEC-01, E2E-SEC-02 |
| `handlers/history.go` | E2E-HIST-01 to E2E-HIST-03 |
| `handlers/provisioners.go` | E2E-PROV-01, E2E-PROV-02 |
| `handlers/notifications.go` | E2E-NOTIF-01, E2E-AUTH-09 |
| `handlers/le.go` (`parseLESettingsFields`) | E2E-LE-01, E2E-LE-04 |
| `handlers/le_renewer.go` (domain policy) | E2E-CERT-12, by inspection only |
| `handlers/le.go` (issuance, lifecycle) | E2E-LE-02, E2E-LE-03 |
| `le/lego.go` | E2E-LE-02. `LEProductionCA`/`LEStagingCA` and the `LE_ACME_DIRECTORY_URL` prerequisite (Section 2.7.4) are what make this file's ACME calls reachable from a local server at all |
| `handlers/handler.go` (`csrfOK`, `requireCSRF`) | E2E-CSRF-01, E2E-CSRF-05 |
| `db/schema.go` (admin seed) | E2E-BOOT-07 case (b) |
| `db/schema.go` (`auth_source`, `session_epoch` migrations) | E2E-AUTH-13, E2E-AUTH-14 |
| `db/users.go` (`UpsertOIDCUser`, `auth_source`) | E2E-AUTH-13, E2E-AUTH-16 |
| `db/users.go` (`session_epoch`, `BumpSessionEpoch`) | E2E-AUTH-12, E2E-AUTH-14, E2E-AUTH-15, E2E-TEMP-02 |
| `db/users.go` (roles, activation) | E2E-ADM-08 |
| `db/authlog.go` | E2E-SEC-01 |
| `templates/le_settings.html` | E2E-LE-04 |
| `templates/profile_2fa.html` | E2E-AUTH-04, including its `ui` companion's QR decode |
| `entrypoint.sh` | E2E-BOOT-01, E2E-BOOT-07 case (c) |
| `scripts/step-ca-bootstrap.sh` | E2E-CERT-11, E2E-PROV-01 |
| `docker-compose.yml` | every bootstrap scenario |
| `handlers/le_renewer.go` (24h ticker) | **no e2e observer.** The ticker itself is a unit concern |
| `handlers/safego.go` | **no e2e observer** beyond the goroutines it wraps |

## 6. Application findings

These are findings about the product, not about the tests. Several of them determine whether a test in Section 3 can be green at all.

**Every finding below is closed.** All twelve were fixed on 2026-08-10. The write-ups are kept because they are the rationale for the tests that now assert the fixed behaviour, and each carries a *Fixed* paragraph naming the mechanism.

| ID | Severity | Finding | Covering test |
|---|---|---|---|
| V1 | High | privilege escalation from viewer to admin | E2E-AUTH-13 |
| V2 | High | DNS-provider credentials echoed in cleartext | E2E-LE-04 |
| V3 | Medium | logout did not invalidate a captured cookie | E2E-AUTH-12 |
| V4 | Medium | `TRUST_PROXY=true` handed the rate limiter to the client | E2E-CFG-02 |
| V5 | Medium | deactivation, deletion and temp expiry did not evict sessions | E2E-AUTH-14, E2E-TEMP-02 |
| V6 | Medium, design | no X.509 name policy | E2E-CERT-12 |
| V7 | Low | temporary credentials in a cleartext cookie | E2E-TEMP-01 |
| V8 | Low | password change did not evict other sessions | E2E-AUTH-15 |
| V9 | Low | `action=create` did not validate `role` | E2E-ADM-08 |
| V10 | Low | `GET /logout` carried no CSRF token | E2E-AUTH-11, E2E-CSRF-01 |
| V11 | High, correctness | flash messages were never delivered | every test in Section 3 that asserts flash text |
| V12 | Medium, correctness | three flash-rendering defects, masked by V11 | E2E-AUTH-02, E2E-ADM-08, E2E-TEMP-01 |

V11 is the one with the widest reach into this document. Around thirty assertions in Section 3 check flash text, and until it was fixed every one of them would have failed against otherwise-correct code.

### 6.1 V1 High, fixed: privilege escalation from viewer to admin

*Evidence.* `POST /profile` with `action=update_info` lets any authenticated user rename themselves to any username not currently taken (`handlers/users.go:226-247`). The handler is gated by `RequireLogin` only and performs no role check. Separately, `UpsertOIDCUser` executes `ON CONFLICT (username) DO UPDATE` setting `role = EXCLUDED.role` and **does not touch `password_hash`** (`db/users.go:266-309`).

*Impact.* A viewer renames themselves to the `preferred_username` of an OIDC administrator who has not yet logged in. That administrator's first single sign-on promotes the attacker's existing row to `admin` while leaving the attacker's own bcrypt hash in place. The attacker then signs in locally as an administrator. End state is the CA's root and intermediate private keys via `POST /admin/backup/download`. No non-default setting is required beyond OIDC being enabled with a group mapping, which is the intended production configuration.

*Fixed, 2026-08-10,* at both ends of the chain.

- **The rename is gone.** `ProfilePost action=update_info` no longer reads a username, `UpdateUserInfo` lost its username parameter, `UsernameExistsExceptID` was deleted, and the input is out of the profile template. A user can change their display name and email and nothing else.
- **The upsert can no longer take over a local row.** A new `users.auth_source` column (`VARCHAR(10) NOT NULL DEFAULT 'local'`, backfilled to `'oidc'` where `password_hash='oidc:jumpcloud'`) is written as `'oidc'` on insert, and both `UpsertOIDCUser` branches carry `WHERE users.auth_source = 'oidc'` on the `DO UPDATE`. A collision against a local row updates nothing, `RowsAffected()` is 0, and the function returns `appdb.ErrOIDCLocalUser`. `OIDCCallback` treats that as a denied login: it writes `auth_log` reason `OIDC: username collides with a local account`, flashes a message pointing at an administrator, redirects to `/login`, and never reaches `completeLogin`.

*What the suite does.* E2E-AUTH-13 walks the former chain and asserts it is refused at both gates.

### 6.2 V2 High, fixed: DNS-provider credentials echoed to the client in cleartext

*Evidence.* `templates/le_settings.html` rendered `value="{{if .LESettings}}{{.LESettings.CFToken}}{{end}}"` for the Cloudflare token field, and did the same for `.LESettings.R53SecretKey` on the Route53 secret field. `type="password"` masks pixels, not bytes: the value was in the HTML source, in the browser cache, and in any proxy log that captures bodies. `LESettingsPost` round-tripped the echoed value back.

*Impact.* The route is manager-gated, so this is not an unauthenticated disclosure, but a Cloudflare API token or an AWS secret key is a credential for a system outside this application entirely. The blast radius extends past the CA.

*The correct pattern already exists next door.* `templates/admin_notifications.html` renders no value for the SMTP password, and `handlers/notifications.go:149-156` implements preserve-on-blank. Apply the same shape to both LE fields.

*Fixed, 2026-08-10.* `le_settings.html` renders no `value` for `cf_token` or `r53_secret`, and both carry `placeholder="leave blank to keep existing"` and `autocomplete="new-password"`. `LESettingsPost` loads the current row first and `parseLESettingsFields` preserves either secret when its field comes back blank, and the handler aborts with a flash rather than saving over them if that load fails. `r53_key_id` deliberately still renders: an AWS access key ID is an identifier rather than a credential, which is the same treatment `smtp_username` gets.

*What the suite does.* E2E-LE-04 asserts the canaries never reach the page and that a blank resubmission preserves them.

### 6.3 V3 Medium, fixed: logout did not invalidate a captured session cookie

*Evidence.* The session store is a client-side `gorilla/sessions` `CookieStore`. There was no server-side session record, so `Logout` could do nothing but ask the browser to drop its copy. A cookie captured before logout stayed valid until the idle timeout at 8h or the absolute lifetime at 24h, and nothing short of rotating `SECRET_KEY` revoked it.

*Fixed, 2026-08-10, by a session epoch.* A new `users.session_epoch INTEGER NOT NULL DEFAULT 0` column is the server-side handle the cookie store lacks.

- `completeLogin` stamps the user's current epoch into the session (`handlers/auth.go:199`).
- `RequireLogin` now takes a `UserLoader` (`middleware/middleware.go:19,69`). After its existing cookie, absolute-lifetime and idle checks it re-reads the user row and rejects the session when the user is missing, is inactive, or carries an epoch that does not match the cookie's (`:114-125`). Rejection clears the session and redirects to `/login` (`rejectSession`, `:133`).
- The loaded user is cached in the request context, and **`RequireRole` now reads the role from that cached user rather than from the session** (`:142-160`), so a demotion takes effect on the very next request. It fails closed with `403` if it is ever mounted outside a `RequireLogin` group.
- The epoch is bumped on logout (`handlers/auth.go:222`), profile password change (`handlers/users.go:272`), admin password reset (`handlers/users.go:142`), completed password reset (`handlers/password_reset.go:200`), deactivation and role change (`db/users.go:115,122`), and temp-user expiry (`db/users.go:371`).
- A profile password change re-stamps the acting session immediately afterwards, so a user who has just changed their own password is not bounced out of the page they are standing on.

This closes V3, V5 and V8 together. E2E-AUTH-12, E2E-AUTH-14, E2E-AUTH-15 and E2E-TEMP-02 are its acceptance criteria and all four now assert the revocation rather than its absence.

**Two consequences a reader will hit.** `RequireLogin` issues one database query per authenticated request, which is a real cost on every page. And a database outage now logs everyone out rather than serving stale sessions, because an unreadable user row is treated as a rejected session. Both are fail-closed by design. E2E-HLTH-05 stops postgres and is the place that interaction is observed.

### 6.4 V4 Medium, fixed: `TRUST_PROXY=true` handed the rate limiter and the audit log to the client

*Evidence.* `chiMiddleware.RealIP` was installed whenever `cfg.TrustProxy` was set. It has no trusted-proxy allowlist, and `clientIP` keys both `security.RL` and `LogAuth` off its result.

*Impact.* Rotating `X-Forwarded-For` gave unlimited password, TOTP and reset-token guessing, and wrote forged source addresses into the audit log, which is the record an incident responder relies on.

*Fixed, 2026-08-10,* by replacing it with `middleware/realip.go`.

- A forwarding header is believed **only when the socket peer's own address falls inside a block in the new `TRUSTED_PROXY_CIDRS` key** (`clientFromHeaders`, `middleware/realip.go:55-59`). A client connecting directly can set any header it likes and none of them are read.
- `X-Forwarded-For` is walked **right to left with trusted hops skipped**, so the first untrusted address wins (`:62-70`). Entries an attacker prepends sit to the left of the real client and are never reached.
- `X-Real-IP` and `True-Client-IP` are consulted only after `X-Forwarded-For` yields nothing, under the same peer rule (`:12`, `:71-75`).
- A malformed CIDR is an error rather than a skipped entry (`ParseTrustedProxies`, `:19-36`), and `TRUST_PROXY=true` with an empty or unparseable list is **fatal at boot** (`main.go:142-145`), alongside the existing `SECRET_KEY` check.

*What the suite does.* E2E-CFG-02 asserts that a forged header from an untrusted peer is ignored and that the audit log records the socket peer.

### 6.5 V5 Medium, fixed: deactivation, deletion and temporary-user expiry did not terminate sessions

Same root cause as V3, and sharper: the temporary-user feature's entire purpose is time-boxed access, and an expired temporary **admin** kept admin for up to 24 hours.

*Fixed, 2026-08-10,* by the session epoch in Section 6.3. `UpdateUserRole` and the deactivation path bump it inline (`db/users.go:115,122`), `ExpireOverdueTempUsers` bumps it as part of the same statement that deactivates the row (`db/users.go:371`), and a deleted user fails `RequireLogin`'s existence check rather than its epoch check. Asserted by E2E-AUTH-14 and E2E-TEMP-02.

### 6.6 V6 Medium, design, fixed: no X.509 name policy anywhere

Any manager could have the CA sign a certificate for any name, including one belonging to somebody else. Defensible as accepted risk for an internal CA whose root is in no public trust store, but undocumented and unasserted, so nobody could say whether it was a decision or an omission.

*Fixed, 2026-08-10, as a mechanism whose default is the old behaviour.* A new `ALLOWED_DOMAIN_SUFFIXES` key.

- **Empty means unrestricted**, which is exactly what the application did before, and the process logs one startup warning saying so (`main.go:154-156`). Upgrading changes no behaviour until an operator opts in.
- When set, a requested name must equal an entry or be a subdomain of one, matched on **label boundaries** via `name == suffix || strings.HasSuffix(name, "."+suffix)` (`checkDomainPolicy`, `handlers/cert_ops.go:105-116`), so `evil-example.com` does not satisfy `example.com`. A wildcard is judged by the name under its `*.` prefix.
- Enforced in `issueCert` rather than in `normalizeIssuePolicy` (`handlers/cert_ops.go:126`). `Renew` never calls the latter, so putting the check there would have left renewal open, which is the whole shape of the bypass.
- Also enforced on the Let's Encrypt side: `LEIssuePost` (`handlers/le.go:61`), `LERenew` (`:127`), and the background renewer (`handlers/le_renewer.go:54`), which logs the skip to both `slog` and the LE log page and carries on with the remaining certificates rather than aborting the run.

**Scope, and this matters.** The key constrains what the **UI** asks the CA to sign. It is not an x509 name policy in `ca.json`, so a caller holding the provisioner password and bypassing the UI is bound by nothing here. Closing that requires a `x509` `allow`/`deny` block on the provisioner, which is a step-ca configuration change and is out of this application's reach.

*What the suite does.* E2E-CERT-12 asserts the unrestricted default and the enforcement on both the issue and the renew paths.

### 6.7 V7 Low, fixed: temporary credentials handed out in a cleartext cookie

The generated username and password were returned to the browser as a `username|password` value in a `new_temp_cred` cookie scoped to `Path=/`. Short-lived and admin-only, but it put a live credential in a cookie jar and in any intermediary that logs headers.

*Fixed, 2026-08-10,* by a one-shot server-side handoff. `AdminUsersTempPost` mints a token with `security.GenerateToken`, files the credential in a process-local mutex-guarded store with a two-minute TTL, and redirects with the token in the query string (`handlers/admin_temp.go:140-145`). The GET spends the token on read and renders the credential once (`:18-25`).

Three properties fall out of `handlers/temp_creds.go` and each is worth asserting:

- **Display-once** survives, because `take` deletes the entry before it even checks the expiry (`:49-63`), so a refresh finds nothing whether or not the TTL has run out.
- **Post/redirect/get** is preserved, so refreshing the result page no longer creates a second temporary user.
- **Expired entries are evicted on every access** (`evictExpired`, `:67-73`), so an uncollected credential cannot accumulate in memory.

The store is deliberately process-local and unpersisted. Losing an uncollected credential to a restart is the right direction to fail.

*What the suite does.* E2E-TEMP-01 asserts the token flow and the absence of the cookie.

### 6.8 V8 Low, fixed: a password change did not invalidate other sessions

Same root cause as V3.

*Fixed, 2026-08-10,* by the session epoch. All three password-write paths bump it: the user's own change (`handlers/users.go:272`), an administrator's reset (`handlers/users.go:142`), and a completed reset-token flow (`handlers/password_reset.go:200`). The first re-stamps the acting session so the user is not logged out of the page they just used. Asserted by E2E-AUTH-15.

### 6.9 V9 Low, fixed: `UsersPost action=create` did not validate `role`

`UsersPost`'s `create` branch passed `r.FormValue("role")` straight to `appdb.CreateUser`. A typo produced an account whose role matched no key in `roleLevel`, so every role-gated route returned `403`, the account could still log in, and the administrator who created it saw no error.

*Fixed, 2026-08-10,* by making the allowlist an invariant of the data layer rather than a habit of one handler. `ValidRole` and `ErrInvalidRole` live in `db/users.go:21-28` and are enforced in `CreateUser` (`:100`), `UpdateUserRole` (`:112`) and `UpsertOIDCUser` (`:270`). The `create` and `change_role` handlers check first so the operator gets `Role must be one of: viewer, manager, admin` rather than a database error (`handlers/users.go:52,92`).

`OIDC_DEFAULT_ROLE` was a second route around the same invariant, since it is operator-supplied and lands in a role field without passing through either handler. The process now refuses to boot on a bad value when OIDC is enabled (`main.go:151-153`).

### 6.10 V10 Low, fixed: `GET /logout` carried no CSRF token

*Evidence.* Logout was `r.Get("/logout", h.Logout)`, outside the `RequireLogin` group and with no token check, so any page that could make the browser issue that GET could log the user out. The weakness was pre-existing, but its blast radius grew with the V3 fix: once logout bumps `session_epoch` it terminates every session that user holds on every device, rather than dropping one browser's cookie.

*Fixed, 2026-08-10.* Logout is now `r.Post("/logout", h.Logout)` behind `requireCSRF` (`main.go:225`, `handlers/auth.go:215`), offered as an inline form in both `base.html` and `admin_base.html`. `r.Get("/logout", h.LogoutGet)` is kept as a safe redirect to `/login` that logs nobody out (`main.go:224`, `handlers/auth.go:209-211`), so an old bookmark degrades rather than breaking.

*What the suite does.* E2E-AUTH-11 asserts both halves, and `/logout` is now one of the twenty-three routes in E2E-CSRF-01's sweep.

### 6.11 V11 High, correctness, fixed: flash messages were never delivered

*Evidence.* `models.FlashMsg` was never registered with `gob`. `gorilla/sessions` gob-encodes `sess.Values`, so `sess.Save` failed with `gob: type not registered for interface` and wrote **no cookie at all**. Every flash message in the application had been silently discarded for as long as the code has existed, and because `h.flash` ignores the save error there was nothing in the logs to show for it.

*Impact on this specification.* Around thirty assertions in Section 3 check flash text. Until this was fixed, every one of them would have failed against otherwise-correct code. That makes V11 the single most consequential finding for the suite, and it is the reason a flash assertion is worth writing at all rather than being noise.

*Fixed, 2026-08-10.* `gob.Register(FlashMsg{})` in `models`' own `init` (`models/models.go:79`) as well as in `main` (`main.go:126`). Registering only in `main` would have left the type unregistered under `go test ./models/...` and every other package test, which is precisely the configuration in which the defect would keep hiding.

*What the suite does.* Every flash assertion in Section 3 is now a live check. The three that changed shape as a result are named in V12.

### 6.12 V12 Medium, correctness, fixed: three flash-rendering defects, masked by V11

Registering the flash type exposed three rendering defects that were unreachable while the delivery mechanism was dead.

| Defect | Consequence | Fix |
|---|---|---|
| `admin_base.html` had no flash region | every message raised from an admin page, including V9's new invalid-role error, surfaced later on whatever non-admin page the administrator happened to visit next | flash region added (`templates/admin_base.html:288-290`) |
| `admin_users_temp` popped the flash twice | three of its error paths showed nothing at all | single pop |
| the login page rendered the lockout message twice, once from `.Error` and once from `.Msgs` | duplicate error boxes on the fifth failed attempt | the fifth-attempt flash removed (`handlers/auth.go:98-101`). `LoginGet`'s `Blocked` branch is now the only channel for that text |

The duplicate flash blocks in `admin_notifications.html` and `admin_console.html` were removed at the same time, so the new base region does not double up.

*What the suite does.* E2E-AUTH-02 asserts the lockout text arrives on the following `GET /login` as `.Error` and **not** as a flash. E2E-ADM-08 asserts the invalid-role error renders on `/admin/users` itself. E2E-TEMP-01 asserts the temp-user error paths render their messages.

### 6.13 Reviewed, no action

Reviewed against source with no action required: the path-containment helpers (`containedPath`, `containedAbsPath`, `safeName`), `csrfOK`'s constant-time comparison and empty-token guard, the OIDC state, nonce and PKCE handling, `resetLink`'s refusal to derive an origin from the request, password-reset token storage, single use and TTL, the provisioner password being read per call, the admin console's argv being wholly server-side, `pg_dump` receiving its password through `PGPASSFILE`, uniformly parameterised SQL, the static handler's embedded sub-FS boundary, the TOTP replay guard, and the Let's Encrypt domain validation.
## Appendix A: test index

All 78 tests, sorted by ID. "Stack" is the CI job or nightly leg that runs it.

| ID | Property | Tier | Stack | Section |
|---|---|---|---|---|
| E2E-ADM-01 | `app.version` pins the certificates library | PR | `e2e-main` | 3.7 |
| E2E-ADM-02 | `ca.health` with the CA up | PR | `e2e-main` | 3.7 |
| E2E-ADM-03 | `ca.health` with the CA down | PR | `e2e-main` | 3.7 |
| E2E-ADM-04 | the OS diagnostic commands still run | PR | `e2e-main` | 3.7 |
| E2E-ADM-05 | an unknown `command_id` is rejected and audited | PR | `e2e-main` | 3.7 |
| E2E-ADM-07 | the `/admin/about` preflight | PR | `e2e-main` | 3.7 |
| E2E-ADM-08 | the non-create user actions | PR | `e2e-main` | 3.7 |
| E2E-AUTH-01 | successful local login rotates session content | PR | `e2e-main` | 3.2 |
| E2E-AUTH-02 | failed logins count down and then lock out | PR | `e2e-main` | 3.2 |
| E2E-AUTH-03 | a correct password is rejected while the IP is blocked | PR | `e2e-main` | 3.2 |
| E2E-AUTH-04 | TOTP enrollment | PR | `e2e-main` | 3.2 |
| E2E-AUTH-05 | login with 2FA, including replay rejection | PR | `e2e-main` | 3.2 |
| E2E-AUTH-06 | login with a recovery code | PR | `e2e-main` | 3.2 |
| E2E-AUTH-07 | disabling 2FA requires the password and a fresh code | PR | `e2e-main` | 3.2 |
| E2E-AUTH-08 | OIDC login against a mock IdP | nightly | nightly `oidc-mail` | 3.2 |
| E2E-AUTH-09 | password reset request and completion | nightly | nightly `oidc-mail` | 3.2 |
| E2E-AUTH-11 | logout is a POST, and a GET to the same path logs nobody out | PR | `e2e-main` | 3.2 |
| E2E-AUTH-12 | logout revokes a cookie captured before it | PR | `e2e-main` | 3.2 |
| E2E-AUTH-13 | the viewer-to-admin escalation chain is refused at both gates | nightly | nightly `oidc-mail` | 3.2 |
| E2E-AUTH-14 | deactivation, demotion and deletion take effect on the next request | PR | `e2e-main` | 3.2 |
| E2E-AUTH-15 | a password change evicts other sessions but not the acting one | PR | `e2e-main` | 3.2 |
| E2E-AUTH-16 | an unmapped OIDC subject gets the configured default role, and a disabled sync does not revert a changed role | nightly | nightly `oidc-mail` | 3.2 |
| E2E-BAK-01 | the bundle is valid, complete and self-verifying | PR | `e2e-main` | 3.8 |
| E2E-BAK-03 | a backup bundle actually restores | nightly | nightly `bootstrap-extra` | 3.8 |
| E2E-BOOT-01 | `stepca` mode happy path from empty volumes via `CA_FINGERPRINT` | PR | `e2e-bootstrap` / `fingerprint` | 3.1 |
| E2E-BOOT-02 | CA down at boot, `UI_TLS_MODE=stepca` exhausts the retry loop and falls back | PR | `e2e-bootstrap` / `ca-down` | 3.1 |
| E2E-BOOT-03 | `UI_TLS_MODE=provided` leaves an operator certificate untouched | PR | `e2e-bootstrap` / `provided` | 3.1 |
| E2E-BOOT-04 | self-signed default when `UI_TLS_MODE` is unset | PR | `e2e-bootstrap` / `selfsigned` | 3.1 |
| E2E-BOOT-05 | wrong `CA_FINGERPRINT` exhausts the root fetch and is reported distinctly | PR | `e2e-bootstrap` / `fingerprint` | 3.1 |
| E2E-BOOT-06 | `CA_ROOT_CERT_PEM` inline root provisioning | PR | `e2e-bootstrap` / `fingerprint` | 3.1 |
| E2E-BOOT-07 | the deliberate startup fatals | PR | `e2e-bootstrap` / `fatals` | 3.1 |
| E2E-BOOT-08 | SIGTERM drains in-flight requests | nightly | nightly `bootstrap-extra` | 3.1 |
| E2E-BOOT-09 | nil CA client short-circuits, and the failure is not cached | PR | `e2e-bootstrap` / `ca-down` | 3.1 |
| E2E-CERT-01 | issuance matrix | PR | `e2e-main`, full matrix nightly `bootstrap-extra` | 3.4 |
| E2E-CERT-02 | the wildcard template rejects a non-wildcard domain | PR | `e2e-main` | 3.4 |
| E2E-CERT-03 | an invalid domain is rejected before it reaches the CA | PR | `e2e-main` | 3.4 |
| E2E-CERT-04 | renew | PR | `e2e-main` | 3.4 |
| E2E-CERT-05 | revocation is enforced CA-side on reuse | PR | `e2e-main` | 3.4 |
| E2E-CERT-06 | import by upload | PR | `e2e-main` | 3.4 |
| E2E-CERT-07 | import by scan | PR | `e2e-main` | 3.4 |
| E2E-CERT-08 | import by manual path, with traversal rejected | PR | `e2e-main` | 3.4 |
| E2E-CERT-09 | downloads | PR | `e2e-main` | 3.4 |
| E2E-CERT-10 | the certificate-detail page's own validations | PR | `e2e-main` | 3.4 |
| E2E-CERT-11 | duration normalisation and the CA's maximum | PR | `e2e-main` | 3.4 |
| E2E-CERT-12 | the domain-suffix policy is unrestricted by default and binds both issue and renew | PR | `e2e-main` | 3.4 |
| E2E-CERT-13 | an import name collision destroys the existing certificate | PR | `e2e-main` | 3.4 |
| E2E-CFG-01 | the response-header and config-switch matrix | PR | `e2e-main`, two rows nightly `oidc-mail` | 3.12 |
| E2E-CFG-02 | a forwarding header is believed only from a trusted peer | nightly | nightly `oidc-mail` | 3.12 |
| E2E-CSRF-01 | every POST route rejects a missing and a wrong token | PR | `e2e-main` | 3.11 |
| E2E-CSRF-05 | a token from a different session is rejected | PR | `e2e-main` | 3.11 |
| E2E-HIST-01 | history pagination | PR | `e2e-main` | 3.6 |
| E2E-HIST-02 | the history action filter | PR | `e2e-main` | 3.6 |
| E2E-HIST-03 | the history certificate-name filter | PR | `e2e-main` | 3.6 |
| E2E-HLTH-01 | `/health` is unconditional | PR | `e2e-main` | 3.9 |
| E2E-HLTH-02 | `/ready` with everything healthy | PR | `e2e-main` | 3.9 |
| E2E-HLTH-03 | `/ready` reports the CA down | PR | `e2e-main` | 3.9 |
| E2E-HLTH-04 | `/ready` recovers when step-ca comes back | PR | `e2e-main` | 3.9 |
| E2E-HLTH-05 | `/ready` reports the database down | PR | `e2e-main` | 3.9 |
| E2E-HLTH-06 | `/admin/integrity` tracks live CA availability and nothing else | PR | `e2e-main` | 3.9 |
| E2E-LE-01 | the LE dashboard and settings round-trip | nightly | nightly `le` | 3.14 |
| E2E-LE-02 | LE issuance against a local ACME server | nightly | nightly `le` | 3.14 |
| E2E-LE-03 | auto-renew toggle and delete | nightly | nightly `le` | 3.14 |
| E2E-LE-04 | DNS-provider credentials are never echoed to the client | nightly | nightly `le` | 3.14 |
| E2E-NOTIF-01 | notification settings round-trip and test send | nightly | nightly `oidc-mail` | 3.15 |
| E2E-PROV-01 | the provisioner list matches the CA's own configuration | PR | `e2e-main` | 3.5 |
| E2E-PROV-02 | the page degrades rather than failing when the CA is unreachable | PR | `e2e-main` | 3.5 |
| E2E-RBAC-01 | the route-by-role matrix, driven as data | PR | `e2e-main` | 3.3 |
| E2E-RBAC-02 | unauthenticated access to an authed route redirects, it does not 403 | PR | `e2e-main` | 3.3 |
| E2E-RENEW-01 | the background renewer re-issues before expiry, with no downtime | nightly | nightly `renew` | 3.10 |
| E2E-SEC-01 | security-log pagination, search and filter | PR | `e2e-main` | 3.6 |
| E2E-SEC-02 | audited privileged actions carry the `Audit:` prefix and the right payload | PR | `e2e-main` | 3.6 |
| E2E-SEC-03 | the provisioner password never appears on any UI surface | PR | `e2e-main` | 3.6 |
| E2E-SEC-04 | the log artifact is safe to publish | PR | `e2e-main` | 3.6 |
| E2E-SEC-05 | the backup bundle is CA-key-equivalent and gated accordingly | PR | `e2e-main` | 3.6 |
| E2E-SEC-06 | sensitive pages are not cacheable | PR | `e2e-main` | 3.6 |
| E2E-STATIC-01 | static assets are served with correct MIME types and resist traversal | PR | `e2e-main` | 3.12 |
| E2E-TEMP-01 | a temporary user is handed over by one-shot token and expires on the ticker | PR | `e2e-main` | 3.13 |
| E2E-TEMP-02 | an expired temporary admin loses access immediately | PR | `e2e-main` | 3.13 |

## Appendix B: workflow file

`.github/workflows/e2e.yml`. Action SHAs are the ones already pinned in the repository's existing workflows.

```yaml
name: E2E

on:
  pull_request:
  push:
    branches: [main]

concurrency:
  group: e2e-${{ github.ref }}
  cancel-in-progress: true

permissions:
  contents: read

env:
  # Pinned by tag and digest: an unpinned image changes browser version between
  # runs, which is exactly the kind of silent drift this suite exists to catch.
  # <digest> is a placeholder: fill it in from the tag's current manifest
  # before this workflow's first run, and again on any deliberate bump. It
  # must also match the @playwright/test version pinned in test/e2e/package.json.
  PLAYWRIGHT_IMAGE: mcr.microsoft.com/playwright:v1.49.1-noble@sha256:<digest>

jobs:
  image:
    name: build e2e images
    runs-on: ubuntu-latest
    steps:
      - name: Checkout
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
      - name: Set up Buildx
        uses: docker/setup-buildx-action@d7f5e7f509e45cec5c76c4d5afdd7de93d0b3df5 # v4.1.0
      - name: Build the application image and warm its layer cache
        uses: docker/build-push-action@f9f3042f7e2789586610d6e8b85c8f03e5195baf # v7.2.0
        with:
          context: ./step-ui-go
          file: ./step-ui-go/Dockerfile
          tags: step-ca-ui:e2e
          push: false
          load: true
          cache-from: type=gha,scope=e2e
          cache-to: type=gha,scope=e2e,mode=max
      - name: Build the harness image (Playwright base plus docker CLI)
        uses: docker/build-push-action@f9f3042f7e2789586610d6e8b85c8f03e5195baf # v7.2.0
        with:
          context: ./test/e2e
          file: ./test/e2e/Dockerfile
          build-args: |
            PLAYWRIGHT_IMAGE=${{ env.PLAYWRIGHT_IMAGE }}
          tags: step-ca-ui-harness:e2e
          push: false
          load: true
          cache-from: type=gha,scope=e2e-harness
          cache-to: type=gha,scope=e2e-harness,mode=max

  e2e-main:
    name: e2e main suite
    needs: image
    runs-on: ubuntu-latest
    timeout-minutes: 25
    steps:
      - name: Checkout
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
      - name: Set up Buildx
        uses: docker/setup-buildx-action@d7f5e7f509e45cec5c76c4d5afdd7de93d0b3df5 # v4.1.0
      - name: Materialise the application image from the layer cache
        uses: docker/build-push-action@f9f3042f7e2789586610d6e8b85c8f03e5195baf # v7.2.0
        with:
          context: ./step-ui-go
          file: ./step-ui-go/Dockerfile
          tags: step-ca-ui:e2e
          push: false
          load: true
          cache-from: type=gha,scope=e2e
      - name: Materialise the harness image from the layer cache
        uses: docker/build-push-action@f9f3042f7e2789586610d6e8b85c8f03e5195baf # v7.2.0
        with:
          context: ./test/e2e
          file: ./test/e2e/Dockerfile
          build-args: |
            PLAYWRIGHT_IMAGE=${{ env.PLAYWRIGHT_IMAGE }}
          tags: step-ca-ui-harness:e2e
          push: false
          load: true
          cache-from: type=gha,scope=e2e-harness
      - name: Generate secrets in-job
        run: |
          make setup FORCE=1
          ADMIN_PW="$(openssl rand -base64 24)"
          echo "::add-mask::${ADMIN_PW}"
          cp .env.example .env
          {
            echo "STEPUI_ADMIN_PASSWORD=${ADMIN_PW}"
            echo "PUBLIC_BASE_URL=https://localhost"
            echo "SESSION_SECURE=true"
          } >> .env
      - name: Bring up the stack
        run: docker compose -f docker-compose.yml -f compose.e2e-image.yml up -d --wait
      # The three steps below run separate containers of the same harness
      # image, all attached to step-network. Each mounts the repo root
      # (compose file, .env and secrets/ live there, so STEPUI_ADMIN_PASSWORD
      # and the ca_password/secret_key/postgres_password canaries are read
      # off the filesystem rather than threaded through as extra masked
      # env vars) and /var/run/docker.sock (so tests can run
      # `docker compose exec`/`logs`/`stop`/`start` and `docker compose exec
      # -T postgres psql ...` from inside the container, which is the one
      # mechanism this suite uses for every docker- or database-touching
      # assertion). Three separate invocations, not one `--project=api
      # --project=ui` command: Playwright does not order across projects
      # within a single invocation, and the ui project depends on state the
      # api project establishes; E2E-AUTH-02/03 additionally need their own
      # source address, which only a second container gives them.
      - name: Run the api project, excluding the lockout pair
        run: |
          docker run --rm \
            --network step-network \
            -v "$PWD:/repo" -v /var/run/docker.sock:/var/run/docker.sock \
            -w /repo/test/e2e \
            -e BASE_URL=https://step-ui:8443 \
            -e CI=true \
            step-ca-ui-harness:e2e \
            sh -c 'npm ci && npx playwright test --project=api --grep-invert "E2E-AUTH-02|E2E-AUTH-03"'
      - name: Run the lockout pair on its own harness address
        run: |
          docker run --rm \
            --network step-network \
            -v "$PWD:/repo" -v /var/run/docker.sock:/var/run/docker.sock \
            -w /repo/test/e2e \
            -e BASE_URL=https://step-ui:8443 \
            -e CI=true \
            step-ca-ui-harness:e2e \
            sh -c 'npm ci && npx playwright test --project=api --grep "E2E-AUTH-02|E2E-AUTH-03"'
      - name: Restart step-ui before the ui project
        # Unconditional, not just on failure: the lockout pair's container
        # address is freed when that container exits, and the freed address
        # can be reassigned to the ui project's own container. Without this
        # restart the ui project could inherit the lockout pair's blocked
        # rate-limit state for an address it never poisoned itself.
        run: make e2e-restart-ui
      - name: Run the ui project
        run: |
          docker run --rm \
            --network step-network \
            -v "$PWD:/repo" -v /var/run/docker.sock:/var/run/docker.sock \
            -w /repo/test/e2e \
            -e BASE_URL=https://step-ui:8443 \
            -e CI=true \
            step-ca-ui-harness:e2e \
            sh -c 'npm ci && npx playwright test --project=ui'
      - name: Collect artifacts
        if: always()
        run: ./test/e2e/collect.sh artifacts/
      - name: Assert the artifact carries no secrets
        if: always()
        run: ./test/e2e/assert-redacted.sh artifacts/
      - name: Upload artifacts
        if: always()
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: e2e-main-${{ github.run_attempt }}
          path: |
            artifacts/
            test/e2e/playwright-report/
            test/e2e/test-results/
          if-no-files-found: error

  e2e-bootstrap:
    name: e2e bootstrap (${{ matrix.scenario }})
    needs: image
    runs-on: ubuntu-latest
    timeout-minutes: 15
    strategy:
      fail-fast: false
      matrix:
        scenario:
          - selfsigned
          - provided
          - ca-down
          - fingerprint
          - fatals
    steps:
      - name: Checkout
        uses: actions/checkout@de0fac2e4500dabe0009e67214ff5f5447ce83dd # v6.0.2
      - name: Set up Node
        uses: actions/setup-node@49933ea5288caeca8642d1e84afbd3f7d6820020 # v4.4.0
        with:
          node-version: "20"
      - name: Set up Buildx
        uses: docker/setup-buildx-action@d7f5e7f509e45cec5c76c4d5afdd7de93d0b3df5 # v4.1.0
      - name: Materialise the application image from the layer cache
        uses: docker/build-push-action@f9f3042f7e2789586610d6e8b85c8f03e5195baf # v7.2.0
        with:
          context: ./step-ui-go
          file: ./step-ui-go/Dockerfile
          tags: step-ca-ui:e2e
          push: false
          load: true
          cache-from: type=gha,scope=e2e
      - name: Install the harness without browsers
        # infra launches no browser, so skipping the download is what keeps
        # each leg inside e2e-main's shadow.
        run: npm ci
        working-directory: test/e2e
        env:
          PLAYWRIGHT_SKIP_BROWSER_DOWNLOAD: "1"
      - name: Generate secrets in-job
        run: |
          make setup FORCE=1
          cp .env.example .env
      - name: Run the scenario
        run: ./test/e2e/scenario.sh "${{ matrix.scenario }}"
      - name: Collect artifacts
        if: always()
        run: ./test/e2e/collect.sh artifacts/
      - name: Assert the artifact carries no secrets
        if: always()
        run: ./test/e2e/assert-redacted.sh artifacts/
      - name: Upload artifacts
        if: always()
        uses: actions/upload-artifact@ea165f8d65b6e75b540449e92b4886f43607fa02 # v4.6.2
        with:
          name: e2e-bootstrap-${{ matrix.scenario }}-${{ github.run_attempt }}
          path: |
            artifacts/
            test/e2e/playwright-report/
          if-no-files-found: error


  e2e-gate:
    name: e2e
    needs:
      - e2e-main
      - e2e-bootstrap
    if: always()
    runs-on: ubuntu-latest
    steps:
      - name: Fail unless every e2e job succeeded
        run: |
          test "${{ needs.e2e-main.result }}" = success
          test "${{ needs.e2e-bootstrap.result }}" = success
```

`scenario.sh` generates and exports `STEPUI_ADMIN_PASSWORD` for every scenario except `fatals` case (b), which requires it absent. That is why the bootstrap job's secret step does not append it to `.env` the way `e2e-main`'s does.

The nightly workflow is the same shape with `on: schedule`, a four-value `leg` matrix (`renew`, `oidc-mail`, `le`, `bootstrap-extra`), and a final `report` job with `if: failure()` that opens or updates a single tracking issue.
