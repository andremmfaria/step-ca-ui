# Security

Application-level controls, then the supply-chain tooling that gates changes to this repository.

## Application security controls

- **CSRF protection.** A per-session token, checked server-side on every mutating route. The token is also exposed through a readable, non-`HttpOnly` cookie (`step-ui-csrf`) so client-side code can echo it as a header.
- **Rate limiting.** `backend/security/security.go`: 5 login attempts within a 5-minute sliding window blocks the IP for 15 minutes. Password reset has its own, separate limiter: 3 attempts per 15 minutes per IP (see [docs/authentication.md](authentication.md#password-recovery)).
- **Security headers.** `backend/middleware/middleware.go`'s `SecurityHeaders`: `X-Frame-Options: DENY`, `X-Content-Type-Options: nosniff`, `Referrer-Policy: strict-origin-when-cross-origin`, a `Content-Security-Policy` with no `unsafe-inline` anywhere (`default-src 'self'`, plus explicit `script-src`, `style-src`, `font-src`, `img-src`, `object-src 'none'`, `base-uri 'self'`, `form-action 'self'`, `frame-ancestors 'none'`), and `Strict-Transport-Security` when `ENABLE_HSTS=true`.
- **Real-IP trust.** `backend/middleware/realip.go` only rewrites the client IP from `X-Forwarded-For` / `X-Real-IP` / `True-Client-IP` when the immediate TCP peer itself falls inside a CIDR block named in `TRUSTED_PROXY_CIDRS`. This is stricter than chi's own `RealIP` middleware, which trusts any forwarding header unconditionally. When `TRUST_PROXY=false` (default), the socket peer is always used and these headers are ignored outright.
- **Certificate name policy.** `ALLOWED_DOMAIN_SUFFIXES` constrains which names the UI will request a signature for (issuance, renewal, and the Let's Encrypt path), matched on label boundaries. Empty (the default) means unrestricted, any manager can request a signature for any name, and the app logs a warning at startup saying so. This is a UI-side constraint only, not an X.509 `nameConstraints` policy: a caller talking to step-ca directly bypasses it entirely.
- **Session epoch.** Every user row carries a `session_epoch` counter. Bumping it (password change, 2FA change, forced logout, deactivation) invalidates every session cookie already issued to that user immediately, not just at its next natural expiry. See [docs/authentication.md](authentication.md#session-and-csrf-cookies).
- **Session lifetime.** 8-hour idle timeout, 24-hour absolute cap, whichever comes first.
- **Cookie hardening.** `__Host-` prefix (requires `Secure`, `Path=/`, no `Domain`) when `SESSION_SECURE=true`, `SameSite=Strict` on the session cookie.
- **Login audit log.** Every login attempt is recorded with IP and User-Agent.
- **Admin audit log.** Every admin action (user management, backups, key downloads, notification changes) is appended to the same security log with a structured event badge.
- **Diagnostics console.** `/admin/console` runs one of 10 allowlisted commands (`backend/handlers/admin_console.go`): `system.date`, `system.hostname`, `system.identity`, `system.disk`, `system.processes`, `app.files`, `app.version` (native Go, no subprocess), `ca.health` (native Go, no subprocess), `openssl.version`, `postgres.ready`. Every run is bounded to an 8-second timeout, capped at 16 KB of combined output, and audit-logged. No shell access, and no arbitrary command execution.
- **Self-signed TLS.** Auto-generated on first boot when `UI_TLS_MODE=self-signed` (the default). Replace with a trusted cert for production, see [docs/configuration.md](configuration.md#tls-certificate-modes).
- **Password hashing.** bcrypt for new and updated passwords, with transparent migration from legacy SHA-256 hashes on next successful login.
- **Non-root container.** The app process runs as uid/gid `10001`.
- **Secrets from files.** `SECRET_KEY`, `POSTGRES_PASSWORD`, `CA_PASSWORD` are mounted from `secrets/` files, never as plain environment values in the shipped compose file, so they never appear in `docker inspect` or `/proc/<pid>/environ`.

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
| `trivy-image` | Vulnerability scan of the built container image | **No.** The job has `continue-on-error: true` and `exit-code: "0"`, so an image finding is uploaded to the Security tab but never fails CI |

`.gitleaks.toml` is a small allowlist of one verified false positive (a `certcrypto.Certificate.KeyType` assignment the generic-api-key rule misreads as a secret), not a baseline of accepted pre-existing findings.

### CodeQL (`.github/workflows/codeql.yml`)

GitHub's own Go code scanning. Runs on push to `main`, on pull requests, and weekly (Sunday 03:17 UTC).

### Meta Lint (`.github/workflows/lint-meta.yml`)

Four jobs: `hadolint` on `backend/Dockerfile`, `actionlint` on the workflow files, `yamllint` on `.github/` (config `.github/.yamllint.yml`), and a `style` job running stylelint, eslint and djlint against the server-rendered UI's CSS/JS/templates, plus a typecheck and eslint pass over the e2e harness.

### Dependabot (`.github/dependabot.yml`)

Weekly (Monday 06:00 UTC), grouped by minor/patch, for: Go modules (`/backend`), Docker base images (`/backend`), npm in `/frontend`, npm in `/clients/ts`, npm in `/test/e2e`, and GitHub Actions.

### Branch protection

The `main` branch carries one active ruleset, `protect-main`: blocks deletion and non-fast-forward pushes. It does not itself require status checks, that gate comes from the CI/e2e/security workflows above running on every push and pull request.

See also: [docs/authentication.md](authentication.md), [docs/development.md](development.md), [docs/configuration.md](configuration.md).
