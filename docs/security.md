# Security

Application-level controls, then the supply-chain tooling that gates changes to this repository.

## Application security controls

- **CSRF protection.** A per-session token, checked server-side on every mutating route: the JSON API requires it echoed back in an `X-CSRF-Token` header, server-rendered forms carry it as a `csrf_token` form field. The token is also exposed through a readable, non-`HttpOnly` cookie (`step-ui-csrf`, served as `__Host-step-ui-csrf` when `SESSION_SECURE=true`) so client-side code can read and echo it.
- **Rate limiting.** `backend/security/security.go`: 5 login attempts from the same IP within a 5-minute sliding window blocks that IP from logging in. The block lasts only until attempts age out of the window, not for a fixed duration, a `BlockTime` constant of 15 minutes exists but is used solely to set the API's `Retry-After: 900` header on a 429, not to hold the block open. Password reset has its own, separate limiter: 3 attempts per 15 minutes per IP (see [docs/authentication.md](authentication.md#local-login) for the full lockout writeup and the same correction there).
- **Security headers.** `backend/middleware/middleware.go`'s `SecurityHeaders`: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, a `Content-Security-Policy` with no `unsafe-inline` anywhere (`default-src 'self'`, plus explicit `script-src`, `style-src`, `font-src`, `img-src 'self' data:`, `object-src 'none'`, `base-uri 'self'`, `form-action 'self'`, `frame-ancestors 'none'`), `Cache-Control: no-store`, a stripped `Server` header, and `Strict-Transport-Security` set on every response: `max-age=31536000; includeSubDomains` when `ENABLE_HSTS=true`, `max-age=0` when false.
- **Real-IP trust.** `backend/middleware/realip.go` only rewrites the client IP from `X-Forwarded-For` / `X-Real-IP` / `True-Client-IP` when the immediate TCP peer itself falls inside a CIDR block named in `TRUSTED_PROXY_CIDRS`. This is stricter than chi's own `RealIP` middleware, which trusts any forwarding header unconditionally. When `TRUST_PROXY=false` (default), the socket peer is always used and these headers are ignored outright.
- **Certificate name policy.** `ALLOWED_DOMAIN_SUFFIXES` constrains which names the UI will request a signature for (issuance, renewal, and the Let's Encrypt path), matched on label boundaries. Empty (the default) means unrestricted, any manager can request a signature for any name, and the app logs a warning at startup saying so. This is a UI-side constraint only, not an X.509 `nameConstraints` policy: a caller talking to step-ca directly bypasses it entirely.
- **Session epoch.** Every user row carries a `session_epoch` counter. Bumping it invalidates every session cookie already issued to that user immediately, not just at its next natural expiry. The confirmed bump sites: logout (`backend/handlers/auth.go:224`), an admin resetting another user's password (`backend/handlers/users.go:142`), a user changing their own password (`backend/handlers/users.go:272`), completing an emailed password reset (`backend/handlers/password_reset.go:200`), an admin changing a user's role (`backend/db/users.go:115`), an admin deactivating a user (`backend/db/users.go:122`), and a temporary user's automatic expiry (`backend/db/users.go:371`). It is **not** bumped on a 2FA change. See [docs/authentication.md](authentication.md#session-and-csrf-cookies) for the full writeup.
- **Session lifetime.** 8-hour idle timeout, 24-hour absolute cap, whichever comes first.
- **Cookie hardening.** `__Host-` prefix (requires `Secure`, `Path=/`, no `Domain`) when `SESSION_SECURE=true`, `SameSite=Strict` on the session cookie.
- **Login audit log.** Every login attempt is recorded with IP and User-Agent.
- **Admin audit log.** Every admin action (user management, backups, key downloads, notification changes) is appended to the same security log with a structured event badge.
- **Diagnostics console.** `/admin/console` runs one of 10 allowlisted commands (`backend/handlers/admin_console.go`): `system.date`, `system.hostname`, `system.identity`, `system.disk`, `system.processes`, `app.files`, `app.version` (native Go, no subprocess), `ca.health` (native Go, no subprocess), `openssl.version`, `postgres.ready`. Every run is bounded to an 8-second timeout, capped at 16 KB of combined output, and audit-logged. No shell access, and no arbitrary command execution.
- **Self-signed TLS.** Auto-generated on first boot when `UI_TLS_MODE=self-signed` (the default). Replace with a trusted cert for production, see [docs/configuration.md](configuration.md#tls-certificate-modes).
- **Password hashing.** bcrypt for new and updated passwords, with transparent migration from legacy SHA-256 hashes on next successful login.
- **Non-root container.** The app process runs as uid/gid `10001`.
- **Secrets from files.** `POSTGRES_PASSWORD_FILE`, `PROVISIONER_PASSWORD_FILE` (`/run/secrets/ca_password`) and `SECRET_KEY_FILE` (`docker-compose.yml:80,99,100`) point step-ui at its secrets on disk, step-ca gets its own via `DOCKER_STEPCA_INIT_PASSWORD_FILE` (`docker-compose.yml:41`). None of these secrets appear as plain environment values in the shipped compose file, so none show up in `docker inspect` or `/proc/<pid>/environ`. There is no `CA_PASSWORD` variable actually read anywhere, the provisioner password env var is `PROVISIONER_PASSWORD`.
- **Path containment.** `backend/handlers/pathsafe.go`'s `containedPath`, `containedAbsPath` and `safeName` helpers keep file operations (backups, uploads, cert file access) from resolving outside their intended directory.
- **One-shot temporary credentials.** A newly created temporary user's generated password is handed to the admin exactly once, through a short-lived, unguessable token (`backend/handlers/temp_creds.go:47-59`) rather than a cookie or a database row, the token is deleted the moment it is read and expires after 2 minutes regardless.
- **Let's Encrypt secret fields render blank.** The Cloudflare/Route53 credential inputs on `/admin/le` never echo a stored value back into the form (`backend/templates/le_settings.html:52,88`), a page source view can't leak them.
- **GitHub secret scanning and push protection** are enabled on the repository, on top of the `gitleaks` CI job below.

**`SECRET_KEY` is mandatory.** The app refuses to start if it is the built-in placeholder or shorter than 32 characters. It signs session cookies and CSRF tokens, leaking it lets an attacker forge any session.

```bash
openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c 48
```

## Supply-chain security

Six GitHub Actions workflows. Every third-party action is pinned to a version tag (e.g. `actions/checkout@v6.0.2`), not a commit SHA.

### Security Scanning (`.github/workflows/security.yml`)

| Job | What it does | Blocking? |
|---|---|---|
| `gosec` | Scans Go source for security anti-patterns, uploads SARIF | No (`-no-fail`) |
| `govulncheck` | Checks Go module dependencies for known CVEs | Yes, any run failure fails the job |
| `gitleaks` | Scans for secrets committed to the repository, against `.gitleaks.toml`'s allowlist | Yes (`GITLEAKS_EXIT_CODE: "1"`) |
| `trivy-fs` | Filesystem/dependency vulnerability scan, HIGH/CRITICAL | Yes (`exit-code: "1"`) |
| `trivy-image` | Vulnerability scan of the built container image | **No.** The job has `continue-on-error: true` and `exit-code: "0"`, so an image finding is uploaded to the Security tab but never fails CI. Unlike `trivy-fs`, it also applies no severity filter, so every severity, not just HIGH/CRITICAL, lands in the Security tab |

`.gitleaks.toml` is a small allowlist of one verified false positive, the regex `Certificate\.KeyType\s*=\s*certcrypto\.` matches lego's `legoConfig.Certificate.KeyType = certcrypto.*` key-type assignment, which the generic-api-key rule misreads as a secret, not a baseline of accepted pre-existing findings.

This workflow itself runs on push to `main`, on pull requests, and on its own weekly cron (Sunday 04:23 UTC).

### CodeQL (`.github/workflows/codeql.yml`)

GitHub's own Go code scanning. Runs on push to `main`, on pull requests, and weekly (Sunday 03:17 UTC).

### Meta Lint (`.github/workflows/lint-meta.yml`)

Four jobs: `hadolint` on `backend/Dockerfile`, `actionlint` on the workflow files, `yamllint` on `.github/` (config `.github/.yamllint.yml`), and a `style` job running stylelint, eslint and djlint against the server-rendered UI's CSS/JS/templates, plus a typecheck and eslint pass over the e2e harness.

### Dependabot (`.github/dependabot.yml`)

Weekly (Monday 06:00 UTC), grouped by minor/patch, for: Go modules (`/backend`), Docker base images (`/backend`), npm in `/frontend`, npm in `/clients/ts`, npm in `/test/e2e`, and GitHub Actions.

### Branch protection

The `main` branch carries one active ruleset, `protect-main`: blocks deletion and non-fast-forward pushes. It does not itself require status checks, that gate comes from the CI/e2e/security workflows above, all of which trigger on a push to `main` and on every pull request (`security.yml` and `codeql.yml` additionally run on their own weekly cron).

See also: [docs/authentication.md](authentication.md), [docs/development.md](development.md), [docs/configuration.md](configuration.md).
