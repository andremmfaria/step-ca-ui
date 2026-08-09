# End-to-End Test Specification: step-ca-ui

Status: Draft — seeded from `plans/step-cli-to-ca-lib-swap.md` (branch `feat/stepca-lib-swap`)

## 1. Purpose and scope

This suite validates the deployed `step-ca-ui` stack (postgres + step-ca + step-ui, `docker-compose.yml`) as a black box: real HTTP requests against a running container set, real TLS handshakes, real files on the compose volumes. It exists because the `feat/stepca-lib-swap` migration replaced every `step`/`step ca` subprocess call with the in-process `github.com/smallstep/certificates/ca` library (`step-ui-go/stepca/`) for both the request-handling path (`handlers/`) and the container's own TLS bootstrap (`step-ui-go/main.go`, `step-ui-go/tlsbootstrap.go`), and none of that is exercised by the unit-test suite alone.

**What e2e covers that unit tests don't:**
- The three-way `UI_TLS_MODE` bootstrap switch (`self-signed` / `provided` / `stepca`) actually running against a real `step-ca` container, including retry-then-fallback timing (`caBootstrapRetries=30`, `caBootstrapInterval=1s`, `stepca/bootstrap.go`, `step-ui-go/tlsbootstrap.go`).
- The background UI-cert renewal goroutine (`startUICertRenewer`) picking up a renewed cert with zero downtime via `certReloader` (`tlsreload.go`).
- Full request/response round-trips through chi middleware — CSRF, session cookies, RBAC — that unit tests exercise handler-by-handler but not as an integrated stack.
- Certificate issuance producing material a real `openssl`/`tls.LoadX509KeyPair` can parse, with SANs/duration/key type matching what was requested (plan Risk R4's "what would have to be true for this plan to fail silently" — CSR shape must match `step ca certificate`'s, not just "issuance succeeded").
- Revocation actually rejected CA-side on reuse (Risk R7 — `Revoke()` returning `nil` is not proof; must observe rejection on subsequent use).
- Fresh-volume `docker-compose up` behavior, which unit tests structurally cannot exercise (plan's "what would have to be true for this plan to fail silently" explicitly calls out `CA_FINGERPRINT`-from-empty-volume and `UI_TLS_MODE=stepca` as untested-until-e2e).

**What e2e does NOT cover** (already gated elsewhere, do not duplicate):
- `go build`/`go vet`/`golangci-lint`/`go test ./...`/`coverage-gate.sh` — CI, not this suite.
- Unit-level CA-client behavior (timeout wrapping, error-string parity, `FakeCA`-based handler tests) — `step-ui-go/stepca/*_test.go`, `handlers/*_test.go`.
- The `grep -rn 'exec.Command'` / `grep -rn '"step"'` code-hygiene sweeps (plan tasks 6.5/7.4) — static checks, not runtime behavior.
- Let's Encrypt (`le/`, `handlers/le_renewer.go`) — untouched by this migration, out of scope per the plan.

## 2. Test environment

### 2.1 Compose stack

Standard stack: `docker-compose.yml` — `postgres` (16-alpine), `step-ca` (`smallstep/step-ca:0.30.2`), `step-ui` (built from `step-ui-go/Dockerfile`). Bring up with:

```
cp .env.example .env
make setup                    # generates secrets/postgres_password, secrets/secret_key, secrets/ca_password
docker compose up -d --build
```

`make setup` only creates `secrets/*` files if absent (`FORCE=1` to regenerate). `secrets/ca_password` is read by `step-ca` at first init (`DOCKER_STEPCA_INIT_PASSWORD_FILE`) and by `step-ui` (`PROVISIONER_PASSWORD_FILE`) to write `PASSWORD_FILE` (`entrypoint.sh:86-102`).

### 2.2 Fresh-volume vs reused-volume scenarios

Two distinct starting states, and most bootstrap tests (Section 3.1) require the **fresh** one:

- **Fresh volumes** (`docker compose down -v` first, or a never-started stack): `step-ca-data`, `step-ui-certs`, `step-ui-ssl`, `step-ui-data`, `postgres-data` all empty. `step-ca`'s `DOCKER_STEPCA_INIT_*` env vars (name, DNS names, provisioner, **and `STEPCA_DEFAULT_TLS_CERT_DURATION`/`STEPCA_MAX_TLS_CERT_DURATION`**) only take effect on this path — they are baked into `ca.json` at first `step ca init` and are silently ignored on a reused volume. This matters directly for E2E-RENEW-01 (Section 3.10), which needs a short `STEPCA_MAX_TLS_CERT_DURATION`.
- **Reused volumes**: `step-ca-data` already has `ca.json` + root/intermediate certs; `postgres-data` already has the `users` table populated (so `STEPUI_ADMIN_PASSWORD` seeding, Section 2.3, is skipped). Use this for tests that don't care about first-boot behavior (most of Sections 3.2–3.9, 3.11).

Tear down fresh-volume tests with `docker compose down -v` so the next test starts clean; reused-volume tests only need `docker compose down` (volumes persist) or targeted cleanup (delete a specific cert row/file).

### 2.3 Seed credentials flow

`step-ui-go/db/schema.go` seeds the initial admin **only when the `users` table is empty**, and **fails startup (`log.Fatal`) if `STEPUI_ADMIN_PASSWORD` is unset at that point** (`schema.go:133-144`, `resolveAdminPassword`, `schema.go:166-178`):

```go
if pw == "" {
    return "", fmt.Errorf("[FATAL] No admin user exists and STEPUI_ADMIN_PASSWORD is not set. ...")
}
```

Note: `.env.example`'s comment ("If left empty, defaults to `Admin123!`") is **stale relative to the current code** — there is no default; a fresh volume with `STEPUI_ADMIN_PASSWORD` unset will crash-loop `step-ui` (visible as the container never passing its healthcheck, `docker compose ps` showing `step-ui` restarting). Every fresh-volume test in this suite must set `STEPUI_ADMIN_PASSWORD` in `.env` before `docker compose up`. Seeded user: `username=admin`, `role=admin`, password = the env value. Remove `STEPUI_ADMIN_PASSWORD` from `.env` after the first successful login (it is not re-read once a user row exists).

### 2.4 Env matrix: `UI_TLS_MODE` × `CA_FINGERPRINT` presence

| `UI_TLS_MODE` | `CA_FINGERPRINT` | `ROOT_CERT` present | Expected UI cert source |
|---|---|---|---|
| unset / `self-signed` | — | — | `generateSelfSignedCert` (EC P-256, 10y, `tlsbootstrap.go`) if `SSL_CERT` absent |
| `provided` | — | — | No-op; operator must mount `SSL_CERT`/`SSL_KEY` beforehand |
| `stepca` | unset, `ROOT_CERT` volume-mounted | yes (via `step-ca-data:ro` mount) | Real leaf cert issued by step-ca, `Provisioner`/`PASSWORD_FILE` |
| `stepca` | set (correct), no pre-existing root | fetched via `Client.Root(sha256Sum)` | Root fetched + verified, then leaf issued |
| `stepca` | set (wrong/mismatched) | fetch fails every retry | Root fetch exhausts 30×1s, warns, continues without root; leaf issuance then also fails (no trusted root client) → self-signed fallback |
| `stepca` | any | step-ca unreachable at boot | Leaf issuance exhausts 30×1s → self-signed fallback (`ensureUICert`'s `"stepca"` branch) |

All four `UI_TLS_MODE` values and both fingerprint states are exercised in Section 3.1.

## 3. Test suites

### 3.1 Startup / bootstrap matrix

All tests in this section start from **fresh volumes** (`docker compose down -v`) unless noted.

---

**E2E-BOOT-01 — `stepca` mode happy path, fresh volumes, `CA_FINGERPRINT` set**

*Preconditions:* `.env`: `UI_TLS_MODE=stepca`, `STEPUI_ADMIN_PASSWORD=<strong pw>`. Comment out `ROOT_CERT` (so the `CA_FINGERPRINT` path is exercised, not the volume-mount path). Set `CA_FINGERPRINT` — obtain it by starting `step-ca` alone first (`docker compose up -d step-ca`, wait for healthy, then `docker compose exec step-ca step certificate fingerprint /home/step/certs/root_ca.crt`), then stop everything (`docker compose down`) before the real fresh-volume run (do **not** `-v` here, since `step-ca-data` must retain the just-computed fingerprint's cert).

*Steps:*
1. `docker compose up -d --build`.
2. Poll `docker compose ps step-ui` until healthy (`healthcheck` hits `GET /login` with `--max-time 3`, `start_period: 20s`).
3. `docker compose logs step-ui | grep "root CA certificate fetched and verified"` — confirms `ensureRootCert` succeeded via `stepca.FetchRootByFingerprint`.
4. `docker compose logs step-ui | grep "UI leaf certificate obtained"` — confirms `ensureUICert`'s `"stepca"` branch succeeded (not the self-signed fallback).
5. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} -showcerts </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject -dates`.
6. `docker compose exec step-ui which step` — must fail (`step` binary not in image).

*Expected:* Container healthy within `start_period` + a few retries. Issuer CN matches the step-ca intermediate (not self-signed, which would have `Issuer == Subject`). `which step` exits non-zero.

*Teardown:* `docker compose down -v`.

---

**E2E-BOOT-02 — CA down at boot, `UI_TLS_MODE=stepca` falls back to self-signed**

*Preconditions:* Fresh volumes. `.env`: `UI_TLS_MODE=stepca`, `STEPUI_ADMIN_PASSWORD` set, `ROOT_CERT` unset with no `CA_FINGERPRINT` either (so root-fetch is skipped entirely and only leaf-issuance retry applies) — or, for a stricter variant, pre-populate `ROOT_CERT` via a one-shot `step-ca` boot then remove it before the real run.

*Steps:*
1. Start only `postgres` + `step-ui` — **do not** start `step-ca` (`docker compose up -d --build postgres step-ui`, and if `step-ui`'s `depends_on: step-ca: condition: service_healthy` blocks this, temporarily comment out that `depends_on` entry for this test run and restore it after).
2. Wait ~30s (bounded by `caBootstrapRetries=30 × caBootstrapInterval=1s` inside `ensureUICert`'s `"stepca"` branch, `tlsbootstrap.go:220-237`).
3. `docker compose logs step-ui | grep "falling back to self-signed"`.
4. `curl -sk -o /dev/null -w "%{http_code}\n" https://localhost:${UI_HTTPS_PORT:-443}/login` — must be `200` (server still starts and serves; TLS bootstrap failure is non-fatal, R2).
5. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject` — issuer must equal subject (self-signed).

*Expected:* Server starts, serves HTTPS with a self-signed EC P-256 cert, logs the fallback explicitly. Total added startup delay ≈ 30s (documented, not a bug).

*Teardown:* Restore `depends_on` if edited; `docker compose down -v`.

---

**E2E-BOOT-03 — `UI_TLS_MODE=provided`**

*Preconditions:* Fresh volumes. `.env`: `UI_TLS_MODE=provided`, `STEPUI_ADMIN_PASSWORD` set. Before `docker compose up`, generate a cert/key pair and place them where `SSL_CERT`/`SSL_KEY` resolve inside the `step-ui-ssl` volume — e.g. `docker compose run --rm --entrypoint sh step-ui -c "apk add --no-cache openssl >/dev/null; openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -days 1 -nodes -subj /CN=test-provided -keyout /opt/step-ui/ssl/server.key -out /opt/step-ui/ssl/server.crt"` (or mount a pre-built pair via a bind mount for the test).

*Steps:*
1. `docker compose up -d --build`.
2. Wait for healthy.
3. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -subject` — must show `CN=test-provided`, proving `ensureUICert`'s `"provided"` branch (no-op) left the operator-supplied cert untouched.

*Expected:* No bootstrap log lines about issuance or self-signed generation for the UI cert (`ensureUICert` returns immediately for `"provided"`).

*Teardown:* `docker compose down -v`.

---

**E2E-BOOT-04 — self-signed default (`UI_TLS_MODE` unset)**

*Preconditions:* Fresh volumes. `.env`: comment out `UI_TLS_MODE` entirely (defaults to `"self-signed"` per `config.go:103`, `getEnv("UI_TLS_MODE", "self-signed")`). `STEPUI_ADMIN_PASSWORD` set.

*Steps:*
1. `docker compose up -d --build`.
2. Wait for healthy.
3. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -issuer -subject -ext subjectAltName`.

*Expected:* Self-signed EC P-256 cert (issuer == subject). SAN list contains `IP Address:<HOST_IP>` and `DNS:localhost` at minimum (`generateSelfSignedCert`, `tlsbootstrap.go:115-184`); `DNS:<UI_HOSTNAME>` additionally if `UI_HOSTNAME` is set. `NotAfter` ≈ 10 years out.

*Teardown:* `docker compose down -v`.

---

**E2E-BOOT-05 — bad `CA_FINGERPRINT`**

*Preconditions:* Fresh volumes. `.env`: `UI_TLS_MODE=stepca`, `CA_FINGERPRINT=0000000000000000000000000000000000000000000000000000000000000000` (64 hex zeros — well-formed but wrong), `ROOT_CERT` unset, `STEPUI_ADMIN_PASSWORD` set.

*Steps:*
1. `docker compose up -d --build`.
2. Wait ~30s (`ensureRootCert`'s retry budget) then a further ~30s (`ensureUICert`'s `"stepca"` retry budget also runs, since with no trusted root the leaf-issuance attempt via `caClient` — if constructed at all — will itself fail on every attempt) — total bootstrap ceiling is `2*30*1s + 30s = 90s` (`main.go:330-333`).
3. `docker compose logs step-ui | grep "could not fetch root CA certificate after retries"`.
4. `docker compose logs step-ui | grep "falling back to self-signed"`.
5. `curl -sk https://localhost:${UI_HTTPS_PORT:-443}/ready` — check `ca` field.

*Expected:* Both retry loops exhaust (not truncated mid-retry — this is why the outer context is derived as `2×retries×interval + 30s`, not a smaller hardcoded number). UI cert falls back to self-signed. `/ready` reports `"ca":"unreachable"` (checkCAReachability falls back to the system trust pool since `ROOT_CERT` was never written, and step-ca's self-signed intermediate is not in the system pool) even though step-ca itself is healthy — a legitimate, expected consequence of the fingerprint mismatch, not a step-ca outage.

*Teardown:* `docker compose down -v`.

### 3.2 Auth flows

Preconditions shared by this whole section unless noted: reused-or-fresh stack up, admin user seeded (`admin` / `STEPUI_ADMIN_PASSWORD` value), a second non-admin test user created via `POST /admin/users` (Section 3.3 covers the endpoint) for role-boundary tests later.

---

**E2E-AUTH-01 — successful local login**

*Steps:* `GET /login` → capture `csrf_token` hidden input and the `step-ui` session cookie. `POST /login` with form fields `csrf_token`, `username=admin`, `password=<pw>`, same cookie jar.

*Expected:* `302` redirect to `/`. New session cookie value (rotated: `completeLogin` resets `s.Values` and sets a new `csrf_token`, `auth.go:190-200`). `auth_log` row inserted via `appdb.LogAuth(..., success=true, reason="")`.

*Teardown:* `GET /logout`.

---

**E2E-AUTH-02 — wrong password shows remaining-attempts count**

*Steps:* `POST /login` with a valid `username` and wrong `password`, valid `csrf_token`.

*Expected:* `302` to `/login`; flash message `"Invalid username or password. Attempts remaining: 4"` (first failure, `LimitCount=5` in `security/security.go:111`). Repeat 4 more times from the same IP.

*Expected on the 5th failure:* Message becomes `"Too many attempts. Please wait 15 minutes."` and a `notifyAsync("auth-burst:...", "auth.failed_burst", "warn", ...)` fires (check `/admin/notifications` log or the notification channel configured).

---

**E2E-AUTH-03 — lockout and actual unblock timing (QA note: verify against code, not the UI copy)**

*Preconditions:* IP already at 5 failed attempts (from E2E-AUTH-02).

*Steps:* Immediately `POST /login` again with correct credentials from the same IP.

*Expected:* Still blocked (`security.RL.IsBlocked` true) — `LoginPost` checks `IsBlocked` before credential verification (`auth.go:63-69`), so even a correct password is rejected while blocked.

*Steps (continued):* Wait and retry at the 4-minute, 5-minute, and 6-minute marks (or fast-forward by having the test truncate the rate limiter's window — this requires either waiting in realtime or, for CI, driving `security.RL` directly in a Go integration test rather than through HTTP).

*Expected — and this is the point of the test:* The block clears once the failed attempts age out of `LimitWindow = 5 * time.Minute` (`security.go:112`, the only window `IsBlocked`'s `clean()` actually consults), **not** at the 15-minute mark the UI copy states (`"Please wait 15 minutes"`, `auth.go:38,65,98`). `security.BlockTime = 15 * time.Minute` (`security.go:113`) is declared but not referenced by any blocking logic found in `security/security.go` — flag this to the team as either a UI-copy bug (should say "5 minutes") or a missing enforcement of `BlockTime` (the intended design), rather than assuming the 15-minute figure is correct. Do not accept "the message said 15 minutes" as passing evidence; assert the actual unblock time.

*Teardown:* `security.RL.Clear(ip)` is normally called only on successful login — since correct credentials are still rejected while blocked, the record ages out naturally; no manual teardown beyond waiting.

---

**E2E-AUTH-04 — TOTP 2FA enrollment**

*Steps:*
1. Log in as a test user.
2. `POST /profile/2fa/start` with `csrf_token` from `GET /profile/2fa`.
3. `GET /profile/2fa/qr` — decode the PNG (a QR library, e.g. `zbarimg`, or parse the `otpauth://` URI directly from `key.Image`'s source — simpler: read `TOTPPendingSecret` from the DB in a test harness, or extract the secret from `GET /profile/2fa`'s rendered `<input readonly>` at `profile_2fa.html:104`).
4. Compute a valid 6-digit code from the pending secret (`github.com/pquerna/otp/totp` in the test harness, or `oathtool --totp -b <secret>`).
5. `POST /profile/2fa/confirm` with `csrf_token`, `totp_code=<computed>`.

*Expected:* Response renders `profile_2fa` with `RecoveryCodes` (8 codes, format `XXXXXX-XXXXXX-XXXXXX`, `totp.go:114,168-180`) shown exactly once. `appdb.EnableUserTOTP` persisted; subsequent `GET /profile/2fa` no longer offers "start" (already enabled).

---

**E2E-AUTH-05 — login with 2FA enabled**

*Preconditions:* Test user has TOTP enabled (E2E-AUTH-04).

*Steps:* `POST /login` with correct `username`/`password` → expect `302 /login` (not `/`) since `user.TOTPEnabled` routes through the pending-2FA branch (`auth.go:115-122`), setting `pending_2fa_user_id` in session. `GET /login` again → `data["NeedTOTP"] = true` should render the TOTP/recovery-code fields (`login.html:32-49`). `POST /login` again with `totp_code=<fresh code>` and the same session cookie.

*Expected:* `302 /` — full login completes (`loginPost2FA`, `auth.go:128-163`). A **replayed** code (same code submitted twice within its 30s window) must be rejected on the second submission (`validateTOTPWithReplayCtx`, `totp.go:206-230`) — test this explicitly as a separate assertion, it is a real anti-replay guarantee worth verifying live.

---

**E2E-AUTH-06 — login with a recovery code**

*Preconditions:* Test user has TOTP enabled; one recovery code from E2E-AUTH-04 saved.

*Steps:* Trigger pending-2FA state as in E2E-AUTH-05, then `POST /login` with `recovery_code=<one of the 8 codes>` (field name `recovery_code`, `login.html:49`; matched case-insensitively, `totp.go:183`) instead of `totp_code`.

*Expected:* `302 /`; `auth_log` reason `"Login with recovery code"` (`auth.go:159`). Reusing the **same** recovery code a second time must fail (`appdb.UseRecoveryCode` marks it used, `totp.go:193`).

---

**E2E-AUTH-07 — 2FA disable requires password + fresh TOTP code**

*Steps:* `POST /profile/2fa/disable` with `csrf_token`, `current_password`, `totp_code`.

*Expected:* Wrong `current_password` → flash `"Current password is incorrect"`, 2FA stays enabled. Correct password + stale/replayed `totp_code` → flash `"Invalid TOTP code"` (replay guard applies here too, `totp.go:154`). Correct password + fresh code → 2FA disabled, `auth_log` entry `"2FA disabled"`.

---

**E2E-AUTH-08 — OIDC login (conditional on `OIDC_ENABLED=true`)**

*Preconditions:* `.env`: `OIDC_ENABLED=true` with a real or test-double OIDC provider (JumpCloud in production; for e2e without a live IdP, stand up a minimal OIDC provider stub — e.g. `oauth2-proxy`'s test IdP or a purpose-built `httptest`-based OIDC server — since `h.initOIDC()` calls `gooidc.NewProvider` at `Handler` construction time and `log.Fatalf`s on discovery failure, `handler.go:81-96`, so a broken/unreachable issuer prevents the whole app from starting, not just OIDC).

*Steps:* `GET /auth/oidc/login` (only registered when `cfg.OIDCEnabled`, `main.go:203-206`) → redirect to IdP with PKCE (`S256ChallengeOption`) and `state`/`nonce` stored in session. Complete IdP auth. IdP redirects to `/auth/oidc/callback?code=...&state=...`.

*Expected:* State mismatch → flash `"OIDC state mismatch — possible CSRF attack"`, no login. Group claim (`OIDC_GROUP_CLAIM`, default `"groups"`) mapped via `OIDC_GROUP_ADMIN`/`MANAGER`/`VIEWER` env vars, admin-precedence (`oidc.go:26-47`). No matching group + empty `OIDC_DEFAULT_ROLE` → flash `"Access denied: your account is not in an authorised group"`, no user created. Matching group → `appdb.UpsertOIDCUser` creates/updates the user with the mapped role; `OIDC_SYNC_ROLE=true` (default) re-syncs role on every login even if manually changed via `/admin/users` since.

*Note:* If no test IdP is available, mark this test **skip-with-reason** rather than omit it — OIDC is a real auth path shipped in `main.go`'s route table.

---

**E2E-AUTH-09 — password reset request and completion**

*Preconditions:* `.env`: `PUBLIC_BASE_URL` set (required — `resetLink` refuses to build a link without it, `password_reset.go:256-266`), SMTP configured via `/admin/notifications` (`GetNotificationSettings`, must have `SMTPEnabled=true`, `SMTPHost`, `SMTPFrom` non-empty) or a local SMTP catcher (e.g. `MailHog`/`smtp4dev`) reachable at the configured host/port. Test user has a non-empty `email`.

*Steps:*
1. `POST /forgot-password` with `csrf_token`, `identifier=<username or email>`.
2. Expect the generic response (`genericResetInfo`, always identical regardless of outcome — no-enumeration by design) — assert the response text is **identical** whether the identifier exists or not (submit a second request with a nonexistent identifier and diff the two responses byte-for-byte).
3. Retrieve the emailed link from the SMTP catcher; extract `token`.
4. `GET /reset-password?token=<token>` → expect the form (not the "invalid or expired" error).
5. `POST /reset-password` with `csrf_token`, `token`, `new_password`, `confirm_password` (mismatched passwords first, to assert `"Passwords do not match."`; then matching, weak per `security.ValidatePassword` policy, to assert the policy-error message; then matching + policy-valid).

*Expected:* `302 /login` with flash `"Password updated. Please sign in with your new password."`. Reusing the same `token` a second time → `"This reset link is invalid or has expired."` (`MarkPasswordResetTokenUsed` + `InvalidatePasswordResetTokens`, `password_reset.go:198-200`).

*Also test:* rate limit — 4th `/forgot-password` request from the same IP within 15 minutes (`passwordResetLimitCount=3`, `passwordResetLimitWindow=15m`, `password_reset.go:25-26`) must still return the generic response (no distinguishable "rate limited" signal to the client) but the audit log should show `"Password reset rate limited"`.

---

**E2E-AUTH-10 — session idle timeout / absolute lifetime**

*Note on automation:* `SessionTimeout=8h` and `SessionMaxLifetime=24h` (`middleware/middleware.go:15,20`) are Go constants, not env-configurable — a real-time e2e wait is impractical. Two options: (a) accept this as a **unit/integration test responsibility** (`middleware/middleware_test.go` should already assert the logic; verify it does), or (b) for a true e2e check, write a small Go test-harness script that uses the known `SECRET_KEY` to mint a `gorilla/sessions` `CookieStore`-encoded cookie with `session_created_at`/`last_activity` values already beyond the thresholds, then send it as the `Cookie` header on a real HTTP request to a running stack and assert redirect-to-`/login`. Document which option was taken; do not silently skip.

### 3.3 RBAC boundaries

Three test users needed: `viewer_user` (role `viewer`), `manager_user` (role `manager`), plus the seeded `admin` (role `admin`). Create via `POST /admin/users` as admin: `csrf_token`, `action=create`, `username`, `password`, `role`.

| Route | Method | viewer | manager | admin |
|---|---|---|---|---|
| `/` , `/dashboard`, `/api/status` | GET | 200 | 200 | 200 |
| `/certificates`, `/certificates/{id}`, `/history`, `/provisioners` | GET | 200 | 200 | 200 |
| `/profile`, `/profile/2fa*` | GET/POST | 200 | 200 | 200 |
| `/issue` (GET/POST) | — | 403 | 200 | 200 |
| `/renew/{id}` | POST | 403 | 200 | 200 |
| `/import` (GET/POST) | — | 403 | 200 | 200 |
| `/download/cert/{id}`, `/download/key/{id}` | GET | 403 | 200 | 200 |
| `/revoke/{id}` | POST | 403 | 403 | 200 |
| `/download/ca`, `/download/intermediate-ca`, `/download/full-chain` | GET | 403 | 403 | 200 |
| `/admin`, `/admin/users*`, `/admin/activity`, `/admin/security`, `/admin/console*`, `/admin/about`, `/admin/integrity`, `/admin/backup*`, `/admin/notifications*` | — | 403 | 403 | 200 |
| `/le*` | — | 403 | 200 | 200 |

**E2E-RBAC-01 through E2E-RBAC-N** — one test per non-`200` cell above (or one parameterized test iterating the table): authenticate as the role in question, hit the route, assert `403 Forbidden` (`middleware.RequireRole`, `middleware.go:111-114`) with body `"403 Forbidden"`. Assert the `200` cells too (regression guard — a route silently becoming role-gated is as much a bug as the reverse).

**E2E-RBAC-N+1 — unauthenticated access to any authed route redirects to `/login`, not 403.** Distinct failure mode from RBAC: `RequireLogin` (`middleware.go:50-95`) redirects (`302`), it does not `403`. Verify at least one route from each tier (`/`, `/issue`, `/admin`) with no session cookie at all.

**E2E-RBAC-N+2 — a viewer can see a certificate exists but cannot obtain its key material.** `GET /certificates/{id}` (viewer, 200, cert metadata rendered) followed by `GET /download/key/{id}` (viewer, 403) — asserts the download-gated-by-role boundary specifically, since `CertificateDetails` itself has no role gate beyond `RequireLogin` while `DownloadKey`/`DownloadCert` require `manager`.

### 3.4 Certificate lifecycle

---

**E2E-CERT-01 — issuance matrix: 4 templates × 4 key types**

*Steps (repeat for each of the 16 combinations):* `POST /issue` with `csrf_token`, `name=e2e-<template>-<keytype>`, `domain=<per-table>`, `template`, `key_type`, `duration` (hidden fields normally set by JS in `issue.html`'s template picker — a form POST can set them directly without a browser).

| Template | Domain | Duration | Key types |
|---|---|---|---|
| `server` | `e2e-server.example.com` | `8760h` | `EC:P-256`, `EC:P-384`, `RSA:2048`, `RSA:4096` |
| `internal` | `e2e-internal.example.com` | `87600h` | `EC:P-256`, `EC:P-384`, `RSA:2048`, `RSA:4096` |
| `wildcard` | `*.e2e-wildcard.example.com` | `8760h` | `EC:P-256`, `EC:P-384`, `RSA:2048`, `RSA:4096` |
| `client` | `e2e-client.example.com` | `8760h` | `EC:P-256`, `EC:P-384`, `RSA:2048`, `RSA:4096` |

*Expected per combination:* `302 /issue` with flash `"Certificate <name> for <domain> issued (<key_type>)!"`. `appdb.InsertCert` row with matching `KeyType`/`IssueDuration`. Download both files (`GET /download/cert/{id}`, `GET /download/key/{id}`, manager+) and verify:
- `openssl x509 -in certificate.crt -noout -text` parses without error.
- `-noout -subject` / `-ext subjectAltName` shows `CN=<domain>, DNS:<domain>` (single-SAN, matching `stepca/issue.go:68-71`'s `Subject.CommonName = domain, DNSNames = []string{domain}` — **this is exactly the Risk R4/R7 "fail silently" scenario the plan calls out**: assert the actual SAN list, not just that issuance returned 200).
- `-noout -pubkey` algorithm/curve/bits matches the requested `key_type` (EC P-256/P-384, RSA 2048/4096).
- `openssl rsa -in private.key -check -noout` (RSA) or `openssl ec -in private.key -check -noout` (EC) succeeds.
- `tls.LoadX509KeyPair(certificate.crt, private.key)` (Go one-liner in the test harness) succeeds — the cert and key are a matched pair.
- `NotAfter - NotBefore` ≈ the requested duration (`8760h` ≈ 365 days, `87600h` ≈ 3650 days) — allow small clock skew, but the two durations must be clearly distinguishable from each other (catches a hardcoded-duration regression).

*Teardown:* `POST /revoke/{id}` for each issued cert (admin), or leave for E2E-CERT-04/05 to consume.

---

**E2E-CERT-02 — wildcard template rejects non-wildcard domain**

*Steps:* `POST /issue` with `template=wildcard`, `domain=not-a-wildcard.example.com` (no `*.` prefix).

*Expected:* `302 /issue` with flash `"Policy error: wildcard template requires domain like *.example.com"` (`normalizeIssuePolicy`, `certs.go:92-94`). No cert created.

---

**E2E-CERT-03 — invalid domain rejected before reaching the CA**

*Steps:* `POST /issue` with `domain=--foo` (or `; rm -rf /`, or any value starting with `-`).

*Expected:* `302 /issue` with an error flash containing `"possible flag injection"` or `"contains disallowed characters"` (`validateIdentifier`, `identifiers.go:20-31`). Confirm via `docker compose logs step-ca` that **no** `/sign` request was ever received for this domain — `validateIdentifier` runs before `caClient.IssueCertificate` is called (`cert_ops.go:101-103`), so a malicious domain must never reach the library, let alone the wire.

---

**E2E-CERT-04 — renew**

*Preconditions:* An active cert from E2E-CERT-01 (any combination), current `expires_at` noted.

*Steps:* `POST /renew/{id}` with `csrf_token` (manager+).

*Expected:* `302 /certificates` with flash `"Certificate renewed"`. New `expires_at` > old value; same `key_type` and `issue_duration` reused (`Renew` reads `c.KeyType`/`c.IssueDuration` and falls back to `"EC:P-256"`/`"8760h"` only for pre-migration rows without those columns, `certs.go:235-244`). Serial number changes (new cert, not the same one).

---

**E2E-CERT-05 — revoke + CA-side rejection (Risk R7 mitigation, live)**

*Preconditions:* An active cert from E2E-CERT-01, its `certificate.crt`/`private.key` on disk (via `docker compose exec step-ui cat /opt/step-ui/certs/<name>/certificate.crt`, or downloaded via E2E-CERT-01's download step).

*Steps:*
1. `POST /revoke/{id}` with `csrf_token` (admin only).
2. Expect `302 /certificates`, flash `"Certificate revoked"`, DB `status='revoked'`.
3. **CA-side verification (the actual point of this test, per plan Risk R7):** attempt to renew the same cert again — `POST /renew/{id}` — which calls `issueCert` with the **same** `certPath`/`keyPath` the just-revoked mTLS identity would authenticate with if `Renew` used revocation-style auth. (Note: `Renew` actually re-issues via a fresh provisioner OTT, not the leaf cert's own mTLS identity, so this specific path does not directly test revocation rejection — see the next step instead.)
4. **Correct verification:** directly exercise `stepca.Client.Revoke`'s mTLS transport a second time against the now-revoked cert (a small Go or `curl --cert --key` test-harness script hitting `step-ca`'s revoke endpoint directly, or re-run `POST /revoke/{id}` for the same already-revoked cert through the UI a second time) and confirm the CA rejects it (expect a `403`/error from step-ca, not a silent success) — a revoked cert's serial must be rejected on any subsequent mTLS-authenticated use, not merely "the UI's `Revoke()` call returned no error."
5. Cross-check via `docker compose logs step-ca | grep -i revoke` for the corresponding log line.

*Expected:* The revoked serial is rejected by step-ca on reuse. This is explicitly **not** satisfied by "the HTTP response to step 1 was 200" — that is exactly the false-positive Risk R7 warns about.

---

**E2E-CERT-06 — import: upload**

*Steps:* Generate a standalone cert/key pair out-of-band (`openssl req -x509 -newkey ec ...`). `POST /import` multipart form: `csrf_token`, `action=upload`, `name`, `domain`, `cert_file=@certificate.crt`, `key_file=@private.key` (both `<input type=file>`, `import.html:95,146`).

*Expected:* `302 /import?tab=upload`, flash `"Certificate <name> uploaded!"`. `KeyType` correctly detected from the uploaded cert (`getCertKeyType`, `cert_ops.go:162-184`) — test with both an EC and an RSA cert to cover both branches.

---

**E2E-CERT-07 — import: scan**

*Preconditions:* A cert/key pair placed directly under `h.cfg.CertsDir` (`/opt/step-ui/certs/<name>/certificate.crt`) outside the DB (e.g. copied in via `docker compose cp` or generated by a prior manual-path test), not yet in the `certificates` table.

*Steps:* `POST /import` with `csrf_token`, `action=scan`.

*Expected:* `302 /import?tab=scan`, flash `"Found and imported: N"`. Re-running immediately after → `"No new certificates found"` (already in DB, `scanExistingCerts` filters by serial via `appdb.GetCertBySerial`, `cert_ops.go:186-215`).

---

**E2E-CERT-08 — import: manual path, path traversal rejected**

*Steps:* `POST /import` with `csrf_token`, `action=manual`, `name`, `domain`, `cert_path=../../../../etc/passwd`.

*Expected:* `302 /import?tab=manual`, flash `"Invalid certificate path: ..."` (`containedPath`, restricts to `h.cfg.CertsDir`, `certs.go:513-518`). Repeat with a legitimate path under `CertsDir` for the success case.

---

**E2E-CERT-09 — download: cert, key, full chain, intermediate, root**

*Steps:* `GET /download/cert/{id}`, `GET /download/key/{id}` (manager+); `GET /download/ca`, `GET /download/intermediate-ca`, `GET /download/full-chain` (admin only).

*Expected:* Correct `Content-Disposition: attachment; filename=...` per handler (`home-ca-root.crt`, `home-ca-intermediate.crt`, `home-ca-full-chain.crt`, or `<safename>.crt`/`.key`). `full-chain` = intermediate PEM directly followed by root PEM (`certs.go:312-337`) — verify `openssl crl2pkcs7 -nocrl -certfile full-chain.crt | openssl pkcs7 -print_certs -noout` lists exactly 2 certs in that order. Key download must trigger an audit-log entry (`h.auditSecurity(..., "certificate.key_download id=... name=... domain=...")`, `certs.go:385`) — verify via `/admin/security` (Section 3.6).

### 3.5 Provisioners page

**E2E-PROV-01 — provisioners list matches CA config**

*Steps:* `GET /provisioners` (viewer+). Cross-check against `docker compose exec step-ca step-ca provisioner list` or the raw `ca.json` (`docker compose exec step-ca cat /home/step/config/ca.json | jq '.authority.provisioners'`).

*Expected:* Every provisioner name/type from `ca.json` appears in the rendered table (`provisioners.html:82-83`, `index . "name"`/`index . "type"`). With the default compose setup, expect exactly one entry: name matching `PROVISIONER` env (default `admin`), type `JWK`. Page also shows `CAURL`, `RootCert`, `Provisioner` config values (`provisioners.go:19-21`) — verify they match `.env`.

**E2E-PROV-02 — provisioners page degrades gracefully when CA is unreachable**

*Steps:* Stop `step-ca` (`docker compose stop step-ca`), then `GET /provisioners`.

*Expected:* `200` (not an error page) with an empty provisioners table (`provs` stays `nil` when `h.caClient()` or `caClient.Provisioners()` errors, `provisioners.go:12-16`) — confirms the CA-unreachable path degrades rather than 500s.

### 3.6 History and security-log pagination

**E2E-HIST-01 — history pagination**

*Preconditions:* At least 35 history entries (issue/renew/revoke/import actions from Section 3.4 tests, `pageSize=30`, `history.go:10`).

*Steps:* `GET /history` (page 1 default), `GET /history?page=2`.

*Expected:* Page 1 shows 30 entries, `TotalPages` = `ceil(total/30)`. Page 2 shows the remainder, no overlap with page 1 (compare entry IDs/timestamps).

**E2E-HIST-02 — history action filter (multi-select)**

*Steps:* `GET /history?action=issue&action=revoke`.

*Expected:* Only `issue` and `revoke` entries returned (multi-value query param, `history.go:15-23`); `renew`/`import` entries excluded.

**E2E-HIST-03 — history cert-name filter**

*Steps:* `GET /history?cert=e2e-server-EC-P-256` (or whatever exact name was used in E2E-CERT-01).

*Expected:* Only entries for that cert name.

**E2E-SEC-01 — security log pagination and search**

*Steps:* `GET /admin/security` (admin), `GET /admin/security?page=2`, `GET /admin/security?q=<username>`, `GET /admin/security?filter=<value handled by GetAuthLogs' filter param>`.

*Expected:* Same pagination contract as history (`pageSize=30`). `TotalOK`/`TotalFail` counts (`appdb.GetAuthStats`) match a manual count from `GET /admin/security?filter=` unfiltered. Entries from E2E-AUTH-01 through E2E-AUTH-09 (logins, 2FA events, password resets) all appear with correct labels (`securityEventLabel`: `"Login"`, `"2FA"`, `"Reset"`, `"Logout"`, `"Denied"`, `"Audit"` — `audit.go:25-43`).

**E2E-SEC-02 — audited privileged actions appear with the `Audit:` prefix**

*Steps:* Perform a key download (E2E-CERT-09) and an admin-console command run (E2E-ADM-01). `GET /admin/security`.

*Expected:* Both actions appear with label `"Audit"` and badge `warn` (`auditPrefix = "Audit: "`, `audit.go:10,30-31,50-51`), reason text containing `certificate.key_download id=... name=... domain=...` and `console.run id=... command=... exit=... timeout=... duration=...` respectively.

### 3.7 Admin console commands

Preconditions: logged in as admin.

**E2E-ADM-01 — `app.version` native command**

*Steps:* `GET /admin/console` → confirm the `<select id="command_id">` includes `app.version` and `ca.health` as `Native` entries (rendered without a `Name`/`Args` shell string, `admin_console.html:38,47`). `POST /admin/console` with `csrf_token`, `command_id=app.version`.

*Expected:* `Result.Success=true`, `Result.ExitCode=0`, output matching `step-ui <Version> (build <BuildDate>, commit <GitCommit>)\nsmallstep/certificates <pinned version>` (`appVersionNativeFn`, `admin_console.go:168-182`) — the pinned library version line **must be present and non-`"unknown"`**, proving `runtime/debug.ReadBuildInfo()` actually resolved `github.com/smallstep/certificates` from the compiled binary's module info (this only works in a real built binary, not `go run` — another reason this belongs in e2e against the built Docker image, not a unit test).

**E2E-ADM-02 — `ca.health` native command, CA up**

*Steps:* `POST /admin/console` with `command_id=ca.health`, step-ca running.

*Expected:* `Result.Success=true`, `Output="ok"`.

**E2E-ADM-03 — `ca.health` native command, CA down**

*Steps:* `docker compose stop step-ca`, then `POST /admin/console` with `command_id=ca.health`.

*Expected:* `Result.Success=false`, `Result.ExitCode=1`, output containing the underlying error text (connection refused / timeout), **not** a panic or 500 — confirms `caHealthNativeFn`'s nil-`ca` guard (`admin_console.go:188-196`) and the general "never crash on CA-unavailable" property (R2) hold through the full HTTP stack, not just in a unit test with a `FakeCA`.

**E2E-ADM-04 — OS-diagnostic commands still work (unaffected by this migration)**

*Steps:* Run each of `system.date`, `system.hostname`, `system.identity`, `system.disk`, `system.processes`, `app.files`, `openssl.version`, `postgres.ready` in turn.

*Expected:* All `Success=true`, sane output (`postgres.ready` specifically should say `accepting connections`; `openssl.version` should report a real OpenSSL build string — Dockerfile deliberately keeps `openssl` installed per Phase 5.6 even though TLS bootstrap no longer shells out to it).

**E2E-ADM-05 — unknown `command_id` rejected and audited**

*Steps:* `POST /admin/console` with `csrf_token`, `command_id=rm.rf` (not in the allowlist).

*Expected:* `data["ConsoleError"] = "Unknown command. Only allowlisted commands may be run."`; `/admin/security` shows an entry `"console.denied command_id=rm.rf"` (`admin_console.go:227`).

**E2E-ADM-06 — allowlist size regression guard**

*Steps:* Count `<option>` entries under `#command_id` on `GET /admin/console`.

*Expected:* Exactly 10 (`adminConsoleCommands`, `admin_console.go:94-163`). A change here (add/remove a command) should be a deliberate code review event, not a silent drift — this test exists to force that.

### 3.8 Backup

**E2E-BAK-01 — backup download produces a valid, complete bundle**

*Steps:* `POST /admin/backup/download` with `csrf_token` (admin). Save the response body as `backup.tgz`.

*Expected:* `Content-Type: application/gzip`, `Content-Disposition: attachment; filename="step-ca-ui-backup-<timestamp>.tgz"`. `tar tzf backup.tgz` lists `manifest.json`, `postgres-stepui.sql`, `step-ca-data.tgz`, `step-ui-data.tgz`, `step-ui-certs.tgz`, `step-ui-uploads.tgz`. Extract `manifest.json`: `format=="step-ca-ui-backup-v1"`, `components` array has an entry per file with matching `sha256` (recompute and compare against the actual extracted file). `postgres-stepui.sql` contains `INSERT INTO` (or `COPY`) statements for the `certificates`/`users`/`cert_history` tables — a plain-SQL dump readable by `psql`, not a custom-format dump. Backup download itself must appear in `/admin/security` as `"backup.download filename=..."` (`backup.go:67`).

**E2E-BAK-02 — backup requires admin + CSRF**

*Steps:* `POST /admin/backup/download` as `manager_user` → `403` (route-level `RequireRole("admin")`). As admin but with a missing/wrong `csrf_token` → `302 /admin/backup` with flash `"Session error. Please refresh the page."`, no bundle produced (`requireCSRF`, `backup.go:52-54`).

### 3.9 Health / readiness transitions

**E2E-HLTH-01 — `/health` is unconditional**

*Steps:* `GET /health` with step-ca stopped, with postgres stopped, with both stopped (stop postgres last / restart it carefully since step-ui itself needs a live DB connection to have started at all — this specific sub-case may only be reachable by killing postgres *after* step-ui is already running).

*Expected:* `200 {"status":"ok"}` in all cases where the step-ui process itself is alive — `Liveness` does no DB/CA check at all (`health.go:21-25`).

**E2E-HLTH-02 — `/ready` reflects both DB and CA status**

*Steps:* `GET /ready` with everything healthy.

*Expected:* `200 {"status":"ready"}`.

**E2E-HLTH-03 — `/ready` reports CA down**

*Steps:* `docker compose stop step-ca`, then `GET /ready`.

*Expected:* `503`, body `{"status":"not ready","db":"ok","ca":"unreachable"}` (`checkCAReachability` returns `"unreachable"` on a connect error, `health.go:96-98`).

**E2E-HLTH-04 — `/ready` recovers when step-ca restarts**

*Steps:* `docker compose start step-ca`, wait for its healthcheck to pass, then immediately `GET /ready`.

*Expected:* `200 {"status":"ready"}` — `checkCAReachability` does a live HTTPS GET on every `/ready` call (no caching), so recovery should be observed on the very next request once step-ca answers `/health`, with no propagation delay beyond TLS handshake + step-ca's own boot time.

**E2E-HLTH-05 — `/ready` reports DB down**

*Steps:* `docker compose stop postgres`, then `GET /ready` (step-ui itself will likely also start failing its own healthcheck around now, since `GET /login` may depend on session store reachability — capture this interaction).

*Expected:* `503`, `"db":"unreachable"` (2s `PingContext` timeout, `health.go:36-40`).

**E2E-HLTH-06 — `/admin/integrity` (`caIntegrity`) reflects a live CA correctly**

*Steps:* `GET /admin/integrity` (admin) with CA up, then with CA down.

*Expected:* `Step-CA API` check `ok`/`err` toggling with CA availability (`caIntegrity`, `health.go:216-231`), independent of the other chain-integrity checks (root/intermediate cert file checks, provisioner-password-sync check) which don't require live CA connectivity and should stay `ok` regardless.

### 3.10 UI-cert renewal goroutine (short-validity trick)

**E2E-RENEW-01 — background renewer picks up a short-lived cert and renews before expiry**

*Preconditions:* **Fresh volumes** (the `STEPCA_*_TLS_CERT_DURATION` envs below only apply at step-ca's first init, Section 2.2). `.env`: `UI_TLS_MODE=stepca`, `STEPUI_ADMIN_PASSWORD` set, `STEPCA_DEFAULT_TLS_CERT_DURATION=5m`, `STEPCA_MAX_TLS_CERT_DURATION=5m`. (`stepca.IssueRequest.Duration` for the UI's own cert is hardcoded to `uiIssueDuration = 8760h`, `tlsbootstrap.go:44`, but the CA's provisioner `maxTLSCertDuration` claim clamps the actually-issued `NotAfter` to whatever the CA allows — this is the mechanism, and it's exactly the same clamping any over-long `api.SignRequest.NotAfter` would hit in production, not a test-only shortcut layered on top of the real code path.)

*Steps:*
1. `docker compose up -d --build`.
2. Wait for healthy; confirm bootstrap succeeded via `stepca` mode (E2E-BOOT-01's log checks).
3. `openssl s_client -connect localhost:${UI_HTTPS_PORT:-443} </dev/null 2>/dev/null | openssl x509 -noout -dates` — confirm `notAfter - notBefore` ≈ 5 minutes (clamped, not the requested 1 year).
4. Note the certificate's serial number (`openssl x509 -noout -serial`).
5. Wait ~2/3 of 5 minutes ≈ 3m20s (`renewUICertOnce`'s `nextSleep = validity * 2 / 3`, `tlsbootstrap.go:296-300`) plus a safety margin — poll every 15s.
6. Re-run the `openssl s_client` probe.

*Expected:* Serial number changes before the original cert's `notAfter` is reached — the renewal goroutine fired on schedule. `docker compose logs step-ui | grep "UI cert renewed"` shows the log line with `nextRenewalIn` ≈ 3m20s. No client-visible downtime or handshake errors at any point during the transition — `certReloader` (`tlsreload.go`) picks up the new file via `GetCertificate`'s per-handshake mtime check with zero restart. If the renewal fails for any reason, `docker compose logs step-ui | grep "UI cert renewal failed"` should show it and the retry backoff (`uiCertRenewFailureBackoff = 5m`).

*Teardown:* `docker compose down -v`.

### 3.11 CSRF enforcement

All state-changing `POST` endpoints must reject a request with a missing or wrong `csrf_token` — verified in `handlers/handler.go`'s `csrfOK`/`requireCSRF` (`handler.go:316-332`) via `subtle.ConstantTimeCompare` against the session-stored token.

**E2E-CSRF-01 — `POST /login` without/with wrong `csrf_token`**

*Steps:* `GET /login` to establish a session + real token, then `POST /login` with `csrf_token=` (empty) or `csrf_token=wrong-value`, correct `username`/`password` otherwise.

*Expected:* `302 /login` with flash `"Session error. Please refresh the page."` — login does **not** succeed even with correct credentials (`auth.go:71-76`, checked after the rate-limit check but before credential verification).

**E2E-CSRF-02 — `POST /issue` without `csrf_token`**

*Steps:* Logged in as manager, `POST /issue` with all valid fields except an empty `csrf_token`.

*Expected:* `302 /issue` with the session-error flash (`requireCSRF`, `certs.go:162`); no cert created, no `/sign` request reaches step-ca (verify via `docker compose logs step-ca`, same style of check as E2E-CERT-03).

**E2E-CSRF-03 — `POST /admin/console` without `csrf_token`**

*Steps:* Logged in as admin, `POST /admin/console` with `command_id=app.version`, missing `csrf_token`.

*Expected:* `302 /admin/console` with the session-error flash (`admin_console.go:218-220`); command never runs (no `console.run`/`console.denied` entry in `/admin/security` for this attempt — confirms the CSRF check happens strictly before command lookup).

**E2E-CSRF-04 — `POST /revoke/{id}` without `csrf_token`**

*Steps:* Logged in as admin, valid cert `id`, `csrf_token` omitted.

*Expected:* `302 /certificates` with the session-error flash; DB `status` unchanged; CA never contacted for revocation.

**E2E-CSRF-05 — token from a different (stale) session is rejected**

*Steps:* Capture a valid `csrf_token` from one session (browser tab A / cookie jar A), then submit it in a `POST` using a *different* session's cookie (jar B, its own distinct token).

*Expected:* Rejected — `csrfOK` compares against `sess.Values["csrf_token"]` for *that request's* session, not a global token, so a token minted for a different session must never validate (guards against token leakage between sessions, e.g. via logs or referrer headers).

## 4. Automation notes

### 4.1 curl/openssl-scriptable (no browser needed)

Nearly everything in this suite is plain form-encoded/multipart HTTP plus TLS inspection, and does not need a real browser:

- All of Section 3.1 (bootstrap) — `docker compose` + `curl`/`openssl s_client` + log greps.
- Section 3.9 (health/readiness) — pure `curl`.
- Section 3.10 (renewal) — `openssl s_client` polling.
- Most of Section 3.2 (auth) — `curl -c cookiejar -b cookiejar` chains, with a TOTP code computed by a small script (`pyotp`, `oathtool`, or Go's `github.com/pquerna/otp/totp` in the harness) rather than scanning a QR code.
- Section 3.3 (RBAC) — a matrix-driven `curl` script iterating routes × roles.
- Section 3.4 (certificates) — `curl -F` for multipart upload (E2E-CERT-06), plain form POST for the rest, `openssl x509`/`openssl rsa`/`openssl ec` for material verification.
- Sections 3.5–3.8 (provisioners, history, admin console, backup) — plain `curl` + `tar`/`jq` for backup inspection.
- Section 3.11 (CSRF) — `curl` with a deliberately wrong/missing form field.

A single reusable test harness (Go, using `net/http` + `net/http/cookiejar`, or Python + `requests`) covers essentially the whole suite; CSRF-token and session-cookie extraction from HTML responses is the only "scraping" required, and both are plain `<input type=hidden>` values extractable with a simple regex/HTML parse — no JS execution needed.

### 4.2 Needs a real browser (Playwright suggested)

A small number of tests exercise client-side JS behavior that a raw HTTP client bypasses entirely:

- **E2E-CERT-01's form**: `issue.html`'s template/key-type/duration picker sets the hidden `template`/`key_type`/`duration` inputs via JS (clicking a template card, `issue.html:12-14`). An HTTP-only test can set those hidden fields directly and skip the JS entirely (which is fine for testing the *backend* contract), but a **separate** Playwright test should click through the actual UI picker at least once per template to catch a JS/backend field-name mismatch that a raw-form test would never surface.
- **E2E-AUTH-04's QR code**: rendering and scanning `GET /profile/2fa/qr` as a real image is a genuine browser/QR-library concern; the HTTP-only path (reading the plaintext secret from the page or DB) is a reasonable substitute for functional coverage but does not verify the QR image itself renders correctly — add one Playwright test that screenshots the QR and decodes it with an image-based QR library.
- **General session/cookie/CSP behavior under a real browser**: the CSP header (`middleware.go:39-42`, `default-src 'self'`, no `unsafe-inline` anywhere) should be smoke-tested with a real browser at least once per page template to catch a console CSP violation that curl cannot detect (a JS file failing to load due to CSP would not fail an HTTP status-code check but would break the page functionally).
- **`admin_console.html`'s command dropdown** — server-rendered (not JS-populated, confirmed by reading the template), so no browser is strictly required here; a curl-based `POST` with the right `command_id` is equivalent. Included for completeness in case the template changes to a JS-driven picker later.

Suggested split: ~85% of this suite as a fast Go/Python HTTP-client suite running against the compose stack; a much smaller Playwright suite (Section 4.2's items + one smoke pass per authenticated page to catch CSP/console errors) as a slower, separate CI job.

### 4.3 Suggested CI shape (compose-based job)

```yaml
# illustrative, not a literal file to drop in
e2e:
  runs-on: ubuntu-latest
  steps:
    - checkout
    - setup docker compose
    - cp .env.example .env; set STEPUI_ADMIN_PASSWORD, PUBLIC_BASE_URL=http://localhost, etc.
    - make setup                      # generate secrets/
    - docker compose up -d --build
    - wait-for-healthy step-ui        # poll `docker compose ps` / healthcheck
    - run HTTP-client e2e suite (Go or Python) against https://localhost:${UI_HTTPS_PORT}
    - run Playwright suite (headless) against the same stack
    - docker compose logs > artifact  # always, for postmortem
    - docker compose down -v
```

Split into separate jobs per Section 3.1 sub-scenario (each needs its own fresh-volume `docker compose up`/`down -v` cycle and shouldn't share a stack with the rest of the suite) versus one shared long-lived stack for Sections 3.2–3.9, 3.11 (which can reuse one stack sequentially, since most don't require a specific bootstrap history — watch for state bleed between tests, e.g. rate-limiter IP blocks from E2E-AUTH-02/03 affecting a later test's login from the same runner IP; namespace by using distinct source IPs/ports where the CI runner supports it, or run auth-lockout tests last and accept the runner needing a fresh `security.RL` state, i.e. a process restart, before any subsequent login-dependent test).

## 5. Traceability table

Maps each manual-QA bullet from `plans/step-cli-to-ca-lib-swap.md`'s **Acceptance Criteria** section (the "Functional correctness, verified against a live `docker-compose up` stack" sub-list) to the e2e test IDs that cover it. Non-functional acceptance criteria in that plan (code greps, `go build`/`vet`/`lint`/`test`, `go.mod` pinning) are CI/code-review concerns, not this suite, and are intentionally not mapped here.

| Plan acceptance-criteria bullet | Covering test IDs |
|---|---|
| `/health`, `/ready` report CA status correctly when step-ca is up and when it is stopped | E2E-HLTH-01, E2E-HLTH-02, E2E-HLTH-03, E2E-HLTH-04, E2E-HLTH-05, E2E-HLTH-06 |
| `/issue` issues a certificate for each of the 4 templates × 4 key types; each cert/key pair is loadable via `openssl`/`tls.LoadX509KeyPair` and matches requested domain/duration/key type | E2E-CERT-01, E2E-CERT-02, E2E-CERT-03 |
| `/certificates/{id}/renew` and `/certificates/{id}/revoke` succeed against a live CA; a revoked cert's serial is rejected on subsequent use (CA-side verification) | E2E-CERT-04, E2E-CERT-05 |
| `/provisioners` page renders the same provisioner name/type as before | E2E-PROV-01, E2E-PROV-02 |
| `/admin/console` "step version"-equivalent (`app.version`) and "step-ca health"-equivalent (`ca.health`) entries still run and report sane output | E2E-ADM-01, E2E-ADM-02, E2E-ADM-03 |
| Fresh `docker-compose up` from empty volumes with `UI_TLS_MODE=stepca` and `CA_FINGERPRINT` set completes bootstrap (root fetch + leaf issuance) without the `step` binary present in the image | E2E-BOOT-01 |

**Additional coverage beyond the plan's explicit manual-QA list** (defense-in-depth the plan's Risk section and Reasoning Transparency called out as needing live verification, folded into this suite since they're adjacent to the same bootstrap/issuance code paths):

| Risk / concern from the plan | Covering test IDs |
|---|---|
| R2 — CA client construction failure must never crash the process; must degrade to a reported status | E2E-BOOT-02, E2E-ADM-03, E2E-PROV-02, E2E-HLTH-03 |
| R4 / "fail silently" scenario — locally-built CSR must produce the *same* SAN/CN shape as `step ca certificate`, not just "issuance succeeded" | E2E-CERT-01 (explicit SAN assertion) |
| R7 / "fail silently" scenario — `Revoke()` returning `nil` is not proof of a working revoke without a subsequent-use rejection check | E2E-CERT-05 |
| "Nobody exercises the `CA_FINGERPRINT`-from-empty-volume and `UI_TLS_MODE=stepca` paths" (plan's stated blind spot) | E2E-BOOT-01, E2E-BOOT-05 |
| Background UI-cert renewal goroutine (Phase 5, `startUICertRenewer`) actually fires on schedule against a real CA | E2E-RENEW-01 |
| CSRF protection holds across the full middleware stack, not just per-handler unit tests | E2E-CSRF-01 through E2E-CSRF-05 |
| RBAC tiers (`viewer`/`manager`/`admin`) enforced correctly at the route level, including download-gated-by-role | E2E-RBAC-01 through E2E-RBAC-N+2 |
