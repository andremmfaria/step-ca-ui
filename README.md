<div align="center">

# Step-CA UI

**Self-hosted web interface for [Smallstep step-ca](https://smallstep.com/docs/step-ca/) — manage your private PKI from a browser.**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Made with Go](https://img.shields.io/badge/Made%20with-Go-00ADD8.svg)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED.svg)](https://docs.docker.com/compose/)

</div>

---

> A small-team-friendly web UI on top of `smallstep/step-ca`. No SaaS, no telemetry, no vendor lock-in — runs entirely on your own server in three Docker containers.

## Features

- **Certificate management** — issue, renew, revoke, and import X.509 certificates
- **Role-based access** — `admin` / `manager` / `viewer`
- **Temporary users** — short-lived guest accounts with automatic expiration (goroutine checks every minute)
- **Custom date picker** — site-themed, no native browser widget
- **Timezone-aware** — configurable via the `TZ` environment variable
- **4 themes** — dark, light, blue, auto (follows OS)
- **Admin workspace** — dedicated admin UI with matching themes
- **Built-in security** — CSRF tokens, rate limiting, IP blocking, security log
- **Admin audit log** — every admin action (user management, backups, key downloads, notification changes) is recorded in the security log with a per-entry event badge
- **Diagnostics console** — read-only `/admin/console` page runs one of 10 allowlisted diagnostic commands; no shell access, 8-second timeout, 16 KB output cap, all invocations audit-logged
- **Provisioner inspection** — list and inspect step-ca provisioners
- **Backup export** — admin UI and CLI backup bundles with SHA-256 manifest checksums
- **CA integrity checks** — root/intermediate chain, provisioner claims, password sync, and pinned step-ca image verification
- **Certificate details** — SANs, fingerprints, key usage, cert/key pair and chain validation
- **Certificate templates** — server, internal service, wildcard, and client identity presets
- **Webhook notifications** — test webhook, failed issue/renew alerts, login burst alerts, and expiry watcher
- **SMTP email delivery** — optional SMTP transport alongside webhooks; configured on the `/admin/notifications` page (host, port, TLS mode, credentials, from address)
- **Password recovery** — self-service forgot-password flow: a single-use, 30-minute reset token is sent by email; tokens are SHA-256 hashed before storage and invalidated on use; IP rate-limited to 3 attempts per 15 minutes; no user enumeration
- **Responsive navigation** — mobile-first top navigation bar on screens ≤ 980 px with collapsible section dropdowns; closes on outside-click or Esc
- **TOTP 2FA** — authenticator app enrollment, QR code, recovery codes, and login challenge
- **OIDC SSO** — authorization code + PKCE flow with group-to-role mapping; feature-flagged, off by default
- **Let's Encrypt / ACME** — issue, renew, and manage public TLS certificates from within the UI
- **Health endpoints** — `/health` (liveness) and `/ready` (readiness) for container orchestration

## Quick Start

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui
make setup   # copies .env.example → .env and generates secrets/
```

Edit `.env` — set `HOST_IP`, `UI_HTTPS_PORT`, `PROVISIONER`, and `TZ` — then:

```bash
make up      # docker compose up -d --build
```

The whole thing takes 2–4 minutes on a fresh VM. Run `make help` to see all available targets.

**Make targets:**

| Target | Description |
|--------|-------------|
| `setup` | Bootstrap: copy `.env.example` and generate `secrets/` |
| `up` | Build images and start all services in detached mode |
| `down` | Stop and remove containers (volumes preserved) |
| `restart` | Stop then start all services |
| `logs` | Stream logs from all services |
| `ps` | Show container status |
| `update` | Pull latest images and rebuild |
| `backup` | Dump database and volumes into `backups/<timestamp>/` |
| `test` | Run Go tests with race detector |
| `lint` | Run golangci-lint and check gofumpt formatting |
| `fmt` | Format Go source with gofumpt |
| `cover` | Run coverage gate |
| `build` | Build the Go binary |
| `clean` | Remove build artifacts and old backups |

## Requirements

|                  | Minimum   | Recommended | High-load   |
|------------------|-----------|-------------|-------------|
| **CPU**          | 1 vCPU    | 2 vCPU      | 4+ vCPU     |
| **RAM**          | 1 GB      | 2 GB        | 4+ GB       |
| **Disk**         | 5 GB      | 20 GB SSD   | 50+ GB NVMe |
| **Network**      | 10 Mbit/s | 100 Mbit/s  | 1 Gbit/s    |
| **Users**        | up to 50  | up to 500   | 500+        |
| **Certificates** | up to 500 | up to 10k   | 10k+        |

**Software:**
- Linux kernel 4.4+ (Ubuntu 20.04+, Debian 11+, CentOS Stream 9+, Rocky 9+, Alma 9+)
- Docker Engine 20.10+ with Compose plugin v2+
- Open ports: `443/tcp` (HTTPS UI), optionally `9443/tcp` (step-ca API)

> Untested but should work: macOS / Windows via Docker Desktop (development only).
> **Not supported:** shared hosting without Docker, Raspberry Pi Zero (insufficient RAM).

## Stack

| Layer        | Technology                                               |
|--------------|----------------------------------------------------------|
| Backend      | Go, [chi](https://github.com/go-chi/chi) router          |
| Frontend     | Server-rendered HTML + vanilla JS, no build step         |
| Database     | PostgreSQL 16                                            |
| CA           | [smallstep/step-ca](https://hub.docker.com/r/smallstep/step-ca) |
| Deploy       | Docker Compose                                           |
| Container OS | Alpine + tzdata                                          |

## Architecture

```
                          ┌────────────┐
   Browser  ─── HTTPS ───►│  step-ui   │  Go web app, port 8443
                          │  (chi)     │
                          └──┬─────┬───┘
                             │     │
                  SQL ◄──────┘     └──────► HTTPS API
                             │     │
                          ┌──▼──┐ ┌▼──────────┐
                          │ pg  │ │ step-ca   │  port 9443
                          │ 16  │ │ (PKI)     │
                          └─────┘ └───────────┘

   step-ui exposes :443  →  internally listens on :8443
   step-ca exposes :9443 →  internal-only by default
```

## Roles

| Role    | View | Issue / Import | Revoke | Manage Users |
|---------|------|----------------|--------|--------------|
| viewer  | Yes  | No             | No     | No           |
| manager | Yes  | Yes            | No     | No           |
| admin   | Yes  | Yes            | Yes    | Yes          |

**Temporary users** can have any role and are auto-blocked when their `expires_at` timestamp passes.

## Authentication / SSO

Step-CA UI supports local password login and OIDC SSO. Both can be active simultaneously; OIDC is off by default so existing deployments are unaffected.

### Local login

Username/password login is on by default (`LOCAL_LOGIN_ENABLED=true`). Keep it enabled while configuring OIDC — disabling it before SSO is verified will lock you out. It acts as a break-glass path if your IdP is unreachable.

TOTP 2FA applies to local accounts only. SSO users rely on the IdP's MFA.

### OIDC SSO

Set `OIDC_ENABLED=true` to activate SSO. The implementation uses the authorization code flow with PKCE and validates the state, nonce, and ID token signature against the issuer's JWKS. JumpCloud is the reference IdP, but any standards-compliant OIDC provider works.

**Routes registered when `OIDC_ENABLED=true`:**

| Route | Purpose |
|-------|---------|
| `GET /auth/oidc/login` | Initiates the authorization code + PKCE flow |
| `GET /auth/oidc/callback` | Receives the provider redirect and completes login |

**Group-to-role mapping**

The ID token claim named by `OIDC_GROUP_CLAIM` (default `groups`) carries the user's group memberships. The app maps those groups to roles with highest-privilege precedence:

```
admin > manager > viewer
```

If none of the three group names match and `OIDC_DEFAULT_ROLE` is empty, access is denied. Set `OIDC_DEFAULT_ROLE` to `viewer` only if every authenticated IdP user should have read access.

When `OIDC_SYNC_ROLE=true` (default), the role is updated on every login so IdP group membership stays authoritative. Set `OIDC_SYNC_ROLE=false` to preserve roles assigned manually inside the app.

**OIDC environment variables:**

| Variable | Default | Description |
|----------|---------|-------------|
| `OIDC_ENABLED` | `false` | Set `true` to enable SSO |
| `OIDC_ISSUER_URL` | — | Provider issuer URL |
| `OIDC_CLIENT_ID` | — | OAuth2 client ID from the IdP |
| `OIDC_CLIENT_SECRET` | — | OAuth2 client secret from the IdP |
| `OIDC_REDIRECT_URL` | — | Must be `https://<your-host>/auth/oidc/callback` |
| `OIDC_GROUP_CLAIM` | `groups` | ID token claim that carries group membership |
| `OIDC_GROUP_ADMIN` | — | IdP group name that maps to the `admin` role |
| `OIDC_GROUP_MANAGER` | — | IdP group name that maps to the `manager` role |
| `OIDC_GROUP_VIEWER` | — | IdP group name that maps to the `viewer` role |
| `OIDC_DEFAULT_ROLE` | _(empty — deny)_ | Role assigned when no group matches |
| `OIDC_SYNC_ROLE` | `true` | Re-sync role from IdP groups on every login |
| `LOCAL_LOGIN_ENABLED` | `true` | Show the username/password form (break-glass) |

**JumpCloud setup checklist:**

1. SSO > + Add New Application > Custom OIDC App
2. Set Redirect URI to `https://<your-host>/auth/oidc/callback`
3. Copy Client ID and Client Secret into `.env`
4. Add a group attribute with Attribute Name `groups`
5. Set `OIDC_GROUP_ADMIN` / `OIDC_GROUP_MANAGER` / `OIDC_GROUP_VIEWER` to the JumpCloud group names

## Security & quality

- **CSRF protection** — tokens on every form and server-side validation on all POST routes
- **Rate limiting** — 5 failed login attempts triggers a 15-minute IP block
- **Security headers** — strict CSP (no `unsafe-inline`), `X-Frame-Options`, `X-Content-Type-Options`, `Referrer-Policy`, optional HSTS
- **Session timeout** — 8 hours, sliding; `Secure` flag enforced in production
- **Login audit log** — every login attempt is recorded with IP and User-Agent
- **Admin audit log** — every admin action is appended to the same security log with a structured event badge (Audit / Login / Logout / 2FA / Reset / Denied)
- **Password-recovery rate limit** — dedicated sliding-window limiter (3 attempts / 15 min per IP); identical response on known and unknown accounts prevents user enumeration
- **Self-signed TLS** — auto-generated on first boot; replace with a trusted cert for production
- **Password hashing** — bcrypt for new/updated passwords, with transparent migration from legacy SHA-256 hashes on next successful login
- **Non-root container** — the app process runs as an unprivileged user
- **Compose secrets from files** — sensitive values (`SECRET_KEY`, `POSTGRES_PASSWORD`, `CA_PASSWORD`) are mounted from `secrets/` files; they never appear in `docker inspect` or `/proc/<pid>/environ`
- **Supply-chain** — `govulncheck` blocks on known CVEs; all third-party Actions are pinned to commit SHAs; Dependabot opens grouped weekly PRs
- **Code quality** — gofumpt + golangci-lint enforced in CI; race detector runs on every test pass

**`SECRET_KEY` is mandatory.** The app refuses to start if `SECRET_KEY` is the default placeholder or shorter than 32 characters. This key signs session cookies and CSRF tokens; leaking it allows forging any user's session.

```bash
# Generate a suitable value:
openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c 48
```

**`SESSION_SECURE`** (default `true`) sets the `Secure` flag on session cookies. Set `false` only for local HTTP development; the app logs a warning at startup when disabled.

**`TRUST_PROXY`** controls how the app determines the client IP:

| Value | Behaviour |
|-------|-----------|
| `false` (default) | Uses the real TCP socket peer. Rate limiter and auth log cannot be spoofed by crafted `X-Forwarded-For` headers. Use when the app is directly internet-facing or behind a proxy you do not control. |
| `true` | Rewrites `RemoteAddr` from `X-Forwarded-For` / `X-Real-IP` (via chi's `RealIP` middleware). Use only when a trusted reverse proxy (nginx, Traefik, Caddy) strips or rewrites those headers before forwarding. |

> **Production tip:** put step-ui behind a reverse proxy with a real TLS certificate, restrict access via VPN or Tailscale, and run `make backup` regularly.

## Configuration

All configuration lives in `.env`. `make setup` creates this file from `.env.example` and generates `secrets/` automatically. See `.env.example` for the full annotated list.

**Core variables:**

```env
HOST_IP=192.168.1.100              # SAN in self-signed cert; step-ca DNS
UI_HTTPS_PORT=443                  # external HTTPS port
PROVISIONER=admin                  # step-ca provisioner identifier
STEP_CA_IMAGE=smallstep/step-ca:0.30.2  # pinned step-ca image
SESSION_SECURE=true                # secure session cookie (warn if false)
ENABLE_HSTS=false                  # enable only with a trusted TLS certificate
TZ=UTC                             # container timezone
STEPCA_DEFAULT_TLS_CERT_DURATION=8760h
STEPCA_MAX_TLS_CERT_DURATION=87600h
```

Sensitive values are read from `secrets/` files via Docker Compose secrets. The equivalent plain-env fallbacks (`SECRET_KEY`, `POSTGRES_PASSWORD`, `CA_PASSWORD`) are supported for development but not recommended for production.

**Reverse-proxy and OIDC variables** (off by default):

```env
TRUST_PROXY=false
LOCAL_LOGIN_ENABLED=true
OIDC_ENABLED=false
OIDC_ISSUER_URL=https://oauth.id.jumpcloud.com/
OIDC_CLIENT_ID=
OIDC_CLIENT_SECRET=
OIDC_REDIRECT_URL=https://<your-host>/auth/oidc/callback
OIDC_GROUP_CLAIM=groups
OIDC_GROUP_ADMIN=step-ca-admins
OIDC_GROUP_MANAGER=step-ca-managers
OIDC_GROUP_VIEWER=step-ca-viewers
OIDC_DEFAULT_ROLE=
OIDC_SYNC_ROLE=true
```

After changing `.env`, recreate the containers:

```bash
docker compose up -d --force-recreate
```

## FAQ

<details>
<summary><b>How do I change the HTTPS port from 443?</b></summary>

Set `UI_HTTPS_PORT` in `.env`:

```env
UI_HTTPS_PORT=8443
```

Then restart: `make restart`.
</details>

<details>
<summary><b>How do I back up and restore the data?</b></summary>

Use the admin UI: **Admin > Backup > Download backup bundle**.

CLI backup:

```bash
make backup
```

Backups include the PostgreSQL dump, all named volumes, and a `manifest.json` with SHA-256 checksums. Restore is manual by design: unpack the archive, restore the PostgreSQL dump, and restore the Docker volumes.
</details>

<details>
<summary><b>How do I reset the admin password?</b></summary>

**Self-service (requires SMTP configured):** use the **Forgot password?** link on the login page. A single-use reset link is emailed and expires after 30 minutes.

**Database reset (no email required):**

```bash
docker compose exec postgres psql -U stepui -d stepui -c \
  "UPDATE users SET password_hash = encode(sha256('newpass'::bytea), 'hex') WHERE username='admin';"
```

Log in with `admin` / `newpass` and change it from the UI. The legacy SHA-256 reset value is accepted for recovery and is transparently rehashed to bcrypt after login.
</details>

<details>
<summary><b>The browser warns about a self-signed certificate. How do I use my own?</b></summary>

Replace the cert and key at the paths mounted into the `step-ui-ssl` volume (`/opt/step-ui/ssl/server.crt` and `server.key`) with your own (e.g. from Let's Encrypt or your internal CA), then restart `step-ui`. The cert must cover your `HOST_IP` or hostname.
</details>

<details>
<summary><b>Can I run this behind Cloudflare / Caddy / nginx?</b></summary>

Yes. Point your reverse proxy at `step-ui:8443` (HTTPS upstream). Set `X-Forwarded-Proto: https` so the app generates correct URLs, and set `TRUST_PROXY=true` in `.env` only if your proxy is trusted and strips incoming forwarding headers.
</details>

<details>
<summary><b>How do I update?</b></summary>

```bash
make backup  # snapshot first
make update  # docker compose pull + up -d --build
```

Database migrations run automatically on startup.
</details>

## Container image

The published image is `ghcr.io/andremmfaria/step-ca-ui`.

```bash
docker pull ghcr.io/andremmfaria/step-ca-ui:main
```

The image is built on Alpine with the Smallstep CLI bundled. It exposes port 8443; map it to your desired host port via `UI_HTTPS_PORT`.

Running standalone (without Compose) requires a reachable PostgreSQL instance and a running step-ca. For most deployments, use the provided `docker-compose.yml` via `make up`.

To build locally without pushing:

```bash
docker build -f step-ui-go/Dockerfile step-ui-go
```

**Publish behaviour:**

| Trigger | Image pushed? | Tags applied |
|---------|---------------|--------------|
| Push to `main` | Yes | `main`, short SHA |
| Pull request | No (build only) | PR ref |

## CI/CD and supply-chain security

Five GitHub Actions workflows run on every push and pull request.

### CI

Runs `gofumpt` formatting check, `go vet`, `go build`, `go test -race`, golangci-lint (expanded ruleset on a ratcheted `new-from-rev` baseline), and a coverage gate. All checks must pass before merge.

### Testing and coverage

Unit tests run under the race detector on every push and PR. A coverage gate (`scripts/coverage-gate.sh`) enforces a minimum total that ratchets upward — it can only rise, never regress.

Well-isolated packages are thoroughly tested and exceed their targets: `middleware` 100%, `config` 100%, `security` 97%. The `handlers` and `le`/ACME packages are exercised by integration tests against real services (PostgreSQL + step-ca) rather than by unit mocks; those results are tracked separately via the `integration` build tag.

### Meta Lint

Lints the Dockerfile with hadolint, workflow files with actionlint, and YAML files under `.github/` with yamllint.

### Security Scanning

Runs four scanners and uploads SARIF results to the GitHub Security tab:

| Scanner | What it checks |
|---------|----------------|
| gosec | Go source code for security anti-patterns |
| govulncheck | Known CVEs in Go module dependencies |
| gitleaks | Secrets accidentally committed to the repository |
| trivy (filesystem) | Dependency and config vulnerabilities across the repo |
| trivy (image) | Vulnerabilities in the built container image |

`govulncheck` is **blocking** — a known CVE in a dependency fails the build. `gitleaks` and trivy (HIGH/CRITICAL) block on new findings, with a committed baseline for any pre-existing items still being triaged.

### CodeQL

GitHub's own Go code scanning, running on push to `main`, pull requests, and weekly.

### Dependabot

Automated dependency PRs are grouped and opened weekly (Monday 06:00 UTC) for Go modules, Docker base images, and GitHub Actions. All third-party actions in the workflows are pinned to commit SHAs.

## Contributing

Pull requests are welcome. For major changes, open an issue first to discuss what you'd like to change.

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui/step-ui-go
go mod download
go run .  # requires a running postgres + step-ca
```

When submitting:
- Run `make fmt` and `make lint` before pushing
- Update relevant tests
- Keep commits focused and descriptive

## Project structure

```
.
├── docker-compose.yml         # 3 services: postgres, step-ca, step-ui
├── .env.example               # configuration template
├── Makefile                   # setup, up, down, backup, test, lint, ...
├── secrets/                   # generated by make setup (not committed)
├── scripts/
│   ├── step-ca-bootstrap.sh   # step-ca init script
│   └── coverage-gate.sh       # CI coverage ratchet
├── LICENSE                    # GPL-3.0
├── README.md                  # this file
└── step-ui-go/
    ├── main.go                # entry point, router setup
    ├── config/                # env-based config loader
    ├── db/                    # SQL queries and schema migrations
    ├── handlers/              # HTTP handlers (one file per area)
    ├── middleware/            # auth, security headers, CSRF
    ├── models/                # data structs
    ├── security/              # password hashing, rate limiting, CSRF
    ├── templates/             # HTML templates (Go html/template)
    ├── static/                # CSS, JS, favicon, images
    ├── Dockerfile             # multi-stage Alpine build
    └── entrypoint.sh          # waits for deps, generates SSL, starts app
```

## License

This project is licensed under the **GNU General Public License v3.0** — see the [LICENSE](LICENSE) file for details.

In short: you can use, modify, and distribute this software, but any derivative work must also be released under GPLv3.
