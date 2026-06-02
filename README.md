<div align="center">

# Step-CA UI

**Self-hosted web interface for [Smallstep step-ca](https://smallstep.com/docs/step-ca/) — manage your private PKI from a browser.**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Made with Go](https://img.shields.io/badge/Made%20with-Go%201.26-00ADD8.svg)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED.svg)](https://docs.docker.com/compose/)
[![Current version](https://img.shields.io/badge/version-v1.6.0-success.svg)](https://github.com/UncleFi1/step-ca-ui/releases/tag/v1.6.0)
[![Latest release](https://img.shields.io/badge/release-v1.6.0-success.svg)](https://github.com/UncleFi1/step-ca-ui/releases/latest)


</div>

---

> A small-team-friendly web UI on top of `smallstep/step-ca`. No SaaS, no telemetry, no vendor lock-in — runs entirely on your own server in three Docker containers.

## Current Release

**Latest stable:** [v1.6.0](https://github.com/UncleFi1/step-ca-ui/releases/tag/v1.6.0)

Highlights:
- TOTP 2FA with authenticator app enrollment, QR code and recovery codes
- login 2FA challenge after password verification
- polished main page, 2FA page and certificate list layout
- responsive certificate list for desktop, laptop and narrow browser windows

## Features

- 📋 **Certificate management** — issue, renew, revoke and import X.509 certificates
- 👥 **Role-based access** — `admin` / `manager` / `viewer`
- ⏱️ **Temporary users** — short-lived guest accounts with automatic expiration *(new in v1.4.0)*
- 📅 **Custom date picker** — site-themed, no native browser widget *(new in v1.4.0)*
- 🌍 **Timezone-aware** — configurable with the `TZ` environment variable
- 🎨 **4 themes** — dark, light, blue, auto (follows OS)
- 🧭 **Admin workspace** — polished admin UI with matching dark, light and blue themes *(new in v1.4.11)*
- 🛡️ **Built-in security** — CSRF tokens, rate limiting, IP blocking, security log
- 🌐 **Provisioner inspection** — list and edit step-ca provisioners
- 💾 **Backup export** — admin UI and CLI backup bundles with manifest checksums *(new in v1.4.9)*
- 🔎 **CA integrity checks** — root/intermediate chain, provisioner claims, password sync and pinned step-ca image *(new in v1.5.0)*
- 🔬 **Certificate details** — SANs, fingerprints, key usage, cert/key pair and chain validation *(new in v1.5.1)*
- 🧩 **Certificate templates** — server, internal service, wildcard and client identity presets *(new in v1.5.2)*
- 🔔 **Webhook notifications** — test webhook, failed issue/renew alerts, login burst alerts and expiry watcher *(new in v1.5.3)*
- 🔐 **TOTP 2FA** — authenticator app enrollment, QR code login challenge and recovery codes *(new in v1.6.0)*
- **OIDC SSO** — group-to-role mapping via any OIDC provider (JumpCloud reference); feature-flagged, off by default

## Quick Start

```bash
git clone https://github.com/UncleFi1/step-ca-ui.git
cd step-ca-ui
sudo ./install.sh
```

The installer can run in Russian or English and supports both clean installs and
safe updates:

```bash
sudo ./install.sh --mode install --lang en
sudo ./install.sh --mode update --lang en
```

Update mode creates a backup first, preserves `.env` and Docker volumes, then
runs `docker compose up -d --build`. It does not run `docker compose down -v`.

That's it. The installer:
1. Detects your OS and installs Docker if needed
2. Auto-detects your server IP (with confirmation)
3. Generates strong passwords for everything
4. Writes `.env` and `credentials.txt` (chmod 600)
5. Builds and starts the containers
6. Prints the URL and admin password

The whole thing takes 2–4 minutes on a fresh VM.

## Requirements

|                | Minimum | Recommended | High-load |
|----------------|---------|-------------|-----------|
| **CPU**        | 1 vCPU  | 2 vCPU      | 4+ vCPU   |
| **RAM**        | 1 GB    | 2 GB        | 4+ GB     |
| **Disk**       | 5 GB    | 20 GB SSD   | 50+ GB NVMe |
| **Network**    | 10 Mbit/s | 100 Mbit/s | 1 Gbit/s  |
| **Users**      | up to 50 | up to 500  | 500+      |
| **Certificates**| up to 500 | up to 10k | 10k+     |

**Software:**
- Linux kernel 4.4+ (Ubuntu 20.04+, Debian 11+, CentOS Stream 9+, Rocky 9+, Alma 9+)
- Docker Engine 20.10+ with Compose plugin v2+
- Open ports: `443/tcp` (HTTPS UI), optionally `9000/tcp` (step-ca API)

> Untested but should work: macOS / Windows via Docker Desktop (development only). \
> **Not supported:** shared hosting without Docker, Raspberry Pi Zero (insufficient RAM).

## Stack

| Layer        | Technology                  |
|--------------|-----------------------------|
| Backend      | Go 1.26, [chi](https://github.com/go-chi/chi) router |
| Frontend     | Server-rendered HTML + vanilla JS, no build step |
| Database     | PostgreSQL 16 |
| CA           | [smallstep/step-ca](https://hub.docker.com/r/smallstep/step-ca) |
| Deploy       | Docker Compose |
| Container OS | Alpine 3.23 + tzdata        |

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
                          │ pg  │ │ step-ca   │  port 9000
                          │ 16  │ │ (PKI)     │
                          └─────┘ └───────────┘

   step-ui exposes :443  →  internally redirects to :8443
   step-ca exposes :9000 →  internal-only by default
```

## Roles

| Role    | View | Issue/Import | Revoke | Manage Users |
|---------|------|--------------|--------|--------------|
| viewer  | ✅   | ❌           | ❌     | ❌           |
| manager | ✅   | ✅           | ❌     | ❌           |
| admin   | ✅   | ✅           | ✅     | ✅           |

**Temporary users** can have any role; they're auto-blocked when `expires_at` passes (a goroutine checks every minute).

## Authentication / SSO

Step-CA UI supports local password login and OIDC SSO. Both can be active simultaneously; the installer leaves OIDC off so existing deployments are unaffected.

### Local login

Username/password login is on by default (`LOCAL_LOGIN_ENABLED=true`). Keep it enabled while you set up OIDC — disabling it before SSO is verified will lock you out. It acts as a break-glass path if your IdP is unreachable.

TOTP/2FA applies to local accounts only. SSO users rely on the IdP's MFA.

### OIDC SSO

Set `OIDC_ENABLED=true` to activate SSO. The implementation uses the authorization code flow with PKCE and validates state, nonce, and the ID token signature against the issuer's JWKS. JumpCloud is the reference IdP, but any standards-compliant OIDC provider works.

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

If none of the three group names match and `OIDC_DEFAULT_ROLE` is empty, access is denied. Set `OIDC_DEFAULT_ROLE` to `viewer` only if every authenticated user in your IdP should have read access.

When `OIDC_SYNC_ROLE=true` (default), the role is updated on every login so IdP group membership stays authoritative. Set `OIDC_SYNC_ROLE=false` to preserve roles assigned manually inside the app.

**Environment variables**

| Variable | Default | Description |
|----------|---------|-------------|
| `OIDC_ENABLED` | `false` | Set `true` to enable SSO |
| `OIDC_ISSUER_URL` | — | Provider issuer URL (e.g. `https://oauth.id.jumpcloud.com/`) |
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

## Security

- **CSRF protection** — tokens on every form and server-side checks on POST routes
- **Rate limiting** — 5 failed login attempts → 15-minute IP block
- **Security headers** — CSP, X-Frame-Options, X-Content-Type-Options, Referrer-Policy, optional HSTS
- **Session timeout** — 8 hours, sliding
- **Login audit log** — every login attempt is recorded with IP and User-Agent
- **Self-signed TLS** — auto-generated on first boot, 10-year validity
- **Password hashing** — bcrypt for new/updated passwords, with transparent migration from legacy SHA-256 hashes on next successful login

**`SECRET_KEY` is mandatory.** The app refuses to start if `SECRET_KEY` is the default placeholder or shorter than 32 characters. This key signs session cookies and CSRF tokens; leaking it allows forging any user's session.

```bash
# Generate a suitable value:
openssl rand -base64 48 | tr -dc 'A-Za-z0-9' | head -c 48
```

**`SESSION_SECURE`** (default `true`) sets the `Secure` flag on session cookies. Set `false` only for local HTTP development; the app logs a warning at startup when it is false.

**`TRUST_PROXY`** controls how the app determines the client IP:

| Value | Behaviour |
|-------|-----------|
| `false` (default) | Uses the real TCP socket peer. Rate limiter and auth log cannot be spoofed by crafted `X-Forwarded-For` headers. Use when the app is directly internet-facing or behind a proxy you do not control. |
| `true` | Rewrites `RemoteAddr` from `X-Forwarded-For` / `X-Real-IP` (via chi's `RealIP` middleware). Use only when a trusted reverse proxy (nginx, Traefik, Caddy) strips or rewrites those headers before forwarding. |

> **Production tip:** put step-ui behind a reverse proxy with a real TLS certificate, restrict access via VPN/Tailscale, and back up the `step-ca-data` volume regularly.

## Configuration

All configuration lives in `.env`. The installer creates this file for you, but you can edit it manually. See `.env.example` for the full annotated list.

**Core variables:**

```env
HOST_IP=192.168.1.100              # SAN in self-signed cert; step-ca DNS
UI_HTTPS_PORT=443                  # external HTTPS port
PROVISIONER=admin                  # step-ca provisioner identifier
CA_PASSWORD=<generated>            # step-ca provisioner password
STEP_CA_IMAGE=smallstep/step-ca:0.30.2 # pinned step-ca image
SECRET_KEY=<generated>             # session/CSRF signing key — MANDATORY, min 32 chars
SESSION_SECURE=true                # secure session cookie over HTTPS (warn if false)
ENABLE_HSTS=false                  # enable only when using a trusted TLS certificate
POSTGRES_PASSWORD=<generated>      # database password
TZ=UTC                             # container timezone
STEPCA_DEFAULT_TLS_CERT_DURATION=8760h
STEPCA_MAX_TLS_CERT_DURATION=87600h
```

**Reverse-proxy and OIDC variables** (off by default — set only what you need):

```env
TRUST_PROXY=false                  # true only behind a trusted proxy that strips XFF
LOCAL_LOGIN_ENABLED=true           # false to hide password form (keep true until OIDC is verified)
OIDC_ENABLED=false
OIDC_ISSUER_URL=https://oauth.id.jumpcloud.com/
OIDC_CLIENT_ID=
OIDC_CLIENT_SECRET=
OIDC_REDIRECT_URL=https://<your-host>/auth/oidc/callback
OIDC_GROUP_CLAIM=groups
OIDC_GROUP_ADMIN=step-ca-admins
OIDC_GROUP_MANAGER=step-ca-managers
OIDC_GROUP_VIEWER=step-ca-viewers
OIDC_DEFAULT_ROLE=                 # empty = deny when no group matches (recommended)
OIDC_SYNC_ROLE=true
```

After changing `.env`, recreate the containers:

```bash
sudo docker compose up -d --force-recreate
```

## FAQ

<details>
<summary><b>How do I change the HTTPS port from 443?</b></summary>

Edit `docker-compose.yml`:
```yaml
services:
  step-ui:
    ports:
      - "8443:8443"   # was "443:8443"
```
Then restart: `sudo docker compose up -d --force-recreate step-ui`.
</details>

<details>
<summary><b>How do I back up and restore the data?</b></summary>

Use the admin UI: `Admin -> Backup -> Download backup bundle`.

CLI export is also supported:

```bash
sudo ./install.sh --mode backup --lang en
```

Backups include PostgreSQL, `step-ca-data`, Step-CA UI data/certs/uploads and
`manifest.json` with SHA-256 checksums. Restore is manual by design; follow
[BACKUP_RESTORE.md](BACKUP_RESTORE.md).
</details>

<details>
<summary><b>How do I reset the admin password?</b></summary>

```bash
sudo docker compose exec postgres psql -U stepui -d stepui -c \
  "UPDATE users SET password_hash = encode(sha256('newpass'::bytea), 'hex') WHERE username='admin';"
```
Then log in with `admin` / `newpass` and change it from the UI.
The legacy SHA-256 reset value is accepted for recovery and is rehashed to bcrypt after login.
</details>

<details>
<summary><b>The browser warns about a self-signed certificate. How do I use my own?</b></summary>

Replace `step-ui-go/ssl/server.crt` and `server.key` with your own cert + key (e.g. from Let's Encrypt or your internal CA), then restart `step-ui`. Make sure the cert covers your `HOST_IP` or hostname.
</details>

<details>
<summary><b>Can I run this behind Cloudflare / Caddy / nginx?</b></summary>

Yes. Point your reverse proxy at `step-ui:8443` (HTTPS upstream) or change step-ui to plain HTTP and put TLS termination on the proxy. Set `X-Forwarded-Proto: https` so step-ui generates correct URLs.
</details>

<details>
<summary><b>How do I update to a new version?</b></summary>

```bash
sudo ./install.sh --mode update --lang en
```
The update mode creates a backup first, keeps existing Docker volumes, optionally checks out a selected tag, and runs migrations automatically on startup. Always check the [release notes](https://github.com/UncleFi1/step-ca-ui/releases) first — major versions may have breaking changes.
</details>

## Container image

The published image is `ghcr.io/andremmfaria/step-ca-ui`.

```bash
docker pull ghcr.io/andremmfaria/step-ca-ui:main
```

The image is built on Alpine 3.23 with the smallstep CLI v0.30.2 bundled. It exposes port 8443; map it to your desired host port.

Running standalone (without Compose) is possible but requires a reachable PostgreSQL instance and a running step-ca. For most deployments, use the provided `docker-compose.yml`:

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui
cp .env.example .env   # edit values
sudo docker compose up -d
```

To build locally without pushing:

```bash
docker build -f step-ui-go/Dockerfile step-ui-go
```

**Publish behaviour:**

| Trigger | Image pushed? | Tags applied |
|---------|---------------|-------------|
| Push to `main` | Yes | `main`, short SHA |
| Version tag (`v*`) | Yes | semver (e.g. `1.7.0`, `1.7`) |
| Pull request | No (build only) | PR ref |

## CI/CD and supply-chain security

Five GitHub Actions workflows run on every push and pull request.

### CI

Runs `gofumpt` formatting check, `go vet`, `go build`, `go test -race`, golangci-lint v2 (expanded ruleset on a ratcheted `new-from-rev` baseline), and a coverage gate. All checks must pass before merge.

### Testing and coverage

Unit tests run under the race detector on every push and PR, and a coverage gate
(`scripts/coverage-gate.sh`) enforces a minimum total that ratchets upward — it can
only rise, never regress.

**Known limitation — overall coverage is currently low (~15%).** This is
architectural, not an oversight:

- Well-isolated packages are thoroughly tested and **exceed** their targets:
  `middleware` 100%, `config` 100%, `security` 97%.
- The `handlers` (~13%) and `le`/ACME (~28%) packages talk to the database and the
  CA directly, so they are exercised **end-to-end by integration tests** rather than
  in isolation. The unit profile only sees their non-I/O logic.
- The `db` layer is covered by an **integration** suite (behind the `integration`
  build tag) that runs against a real Postgres service in CI; those numbers are not
  reflected in the default `go test ./...` profile.

**The remaining coverage comes from live/integration tests, not mocks.** A follow-up
wave runs `handlers`, `le`, and `db` against real services (Postgres + step-ca + an
ACME test server such as Pebble), measured with `-coverpkg` and merged into the unit
profile so the gate reflects real coverage and ratchets toward the per-module targets.
Until then the gate sits at the honest unit-only baseline. See `REMEDIATION_SPEC.md`
(P3-0) and `IMPLEMENTATION_PLAN.md` (Wave 4) for the plan.

### Meta Lint

Lints the Dockerfile with hadolint, workflow files with actionlint, and YAML files under `.github/` with yamllint.

### Security Scanning

Runs four scanners and uploads SARIF results to the GitHub Security tab:

| Scanner | What it checks |
|---------|---------------|
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

Pull requests are welcome. For major changes, please open an issue first to discuss what you'd like to change.

```bash
git clone https://github.com/UncleFi1/step-ca-ui.git
cd step-ca-ui/step-ui-go
go mod download
go run .  # requires running postgres + step-ca
```

When submitting:
- Run `gofmt -w .` and `go vet ./...`
- Update relevant tests
- Keep commits focused and descriptive

## Project structure

```
.
├── docker-compose.yml         # 3 services: postgres, step-ca, step-ui
├── .env.example               # configuration template
├── install.sh                 # one-shot installer
├── LICENSE                    # GPL-3.0
├── README.md                  # this file
└── step-ui-go/
    ├── main.go                # entry point, router setup
    ├── config/                # env-based config loader
    ├── db/                    # all SQL queries
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
