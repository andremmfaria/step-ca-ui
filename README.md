<div align="center">

<img src="assets/logo.svg" alt="Step-CA UI logo" width="140" height="140" />

# Step-CA UI

**Self-hosted web interface for [Smallstep step-ca](https://smallstep.com/docs/step-ca/), manage your private PKI from a browser.**

[![License: GPL v3](https://img.shields.io/badge/License-GPLv3-blue.svg)](https://www.gnu.org/licenses/gpl-3.0)
[![Made with Go](https://img.shields.io/badge/Made%20with-Go-00ADD8.svg)](https://go.dev)
[![Docker](https://img.shields.io/badge/Docker-Compose-2496ED.svg)](https://docs.docker.com/compose/)

</div>

---

A small-team-friendly web UI on top of `smallstep/step-ca`. No SaaS, no telemetry, no vendor lock-in, runs on your own server in three Docker containers (step-ui, PostgreSQL, step-ca). The UI talks to step-ca through the Go `smallstep/certificates` library, not the `step` CLI.

## Features

- Certificate management: issue, renew, revoke and import X.509 certificates, with server/internal-service/wildcard/client-identity templates
- Role-based access (`admin` / `manager` / `viewer`) plus short-lived temporary users with automatic expiry
- TOTP 2FA, OIDC SSO with group-to-role mapping, and self-service password recovery
- Let's Encrypt / ACME issuance and renewal from within the UI
- Built-in security: CSRF tokens, per-IP rate limiting, security and admin audit logs, a read-only diagnostics console
- Backup export (admin UI and CLI) with SHA-256 manifest checksums, CA integrity checks, webhook and SMTP notifications
- 4 themes, a custom date picker, and a mobile-first responsive layout

## Quick Start

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui
make setup   # copies .env.example → .env and generates secrets/
# edit .env: set HOST_IP, UI_HTTPS_PORT, PROVISIONER, TZ, and STEPUI_ADMIN_PASSWORD
make up      # docker compose up -d --build
```

`STEPUI_ADMIN_PASSWORD` is not optional on a first boot, see [docs/deployment.md](docs/deployment.md). Run `make help` for every other target.

## Documentation

- [docs/architecture.md](docs/architecture.md): components today, request flow, roles, the SPA migration's state, and the generated TypeScript client
- [docs/configuration.md](docs/configuration.md): startup sequence, TLS bootstrap, and the full environment variable reference
- [docs/authentication.md](docs/authentication.md): local login, OIDC SSO, session/CSRF cookies, 2FA, and password reset
- [docs/deployment.md](docs/deployment.md): requirements, compose services, secrets, the container image, and Makefile targets
- [docs/security.md](docs/security.md): application security controls and the supply-chain gates in CI
- [docs/development.md](docs/development.md): local dev loop, tests, lint gates, regenerating the API contract, and project structure
- [docs/faq.md](docs/faq.md): frequently asked questions
- [plans/frontend-backend-split.md](plans/frontend-backend-split.md): the in-progress React SPA / Go backend-for-frontend migration plan

## License

GNU General Public License v3.0, see [LICENSE](LICENSE). You can use, modify, and distribute this software, but any derivative work must also be released under GPLv3.
