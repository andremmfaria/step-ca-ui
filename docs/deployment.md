# Deployment

Requirements, the compose stack, secrets, the container image, and the Makefile.

## Requirements

- Docker Engine with the Compose plugin (v2). The compose file uses top-level `secrets:` with file references and `depends_on: condition: service_healthy`, both Compose v2 features.
- Ports: `443/tcp` by default for the UI (configurable via `UI_HTTPS_PORT`), and `9443/tcp` for the step-ca API, which the stock `docker-compose.yml` publishes unconditionally (`ports: ["9443:9443"]` on the `step-ca` service). Remove that mapping yourself if you don't want step-ca reachable directly from outside the host.
- A reachable PostgreSQL 16-compatible database if you're not using the bundled `postgres` container.

This page intentionally does not give CPU/RAM/disk sizing numbers or a supported-OS list: nothing in the repository benchmarks or states them, so a table here would be invented rather than verified.

## Quick start

```bash
git clone https://github.com/andremmfaria/step-ca-ui.git
cd step-ca-ui
make setup   # copies .env.example → .env and generates secrets/
```

Edit `.env`: set `HOST_IP`, `UI_HTTPS_PORT`, `PROVISIONER`, `TZ`, and **`STEPUI_ADMIN_PASSWORD`**. That last one is not optional: if no admin user exists yet and it's empty, the container fails to start (see [docs/configuration.md](configuration.md#core-deployment)). Then:

```bash
make up      # docker compose up -d --build
```

Run `make help` to see every target.

## Docker Compose services

Three services, defined in `docker-compose.yml`:

| Service | Image | Notes |
|---|---|---|
| `postgres` | `postgres:16-alpine` | `postgres-data` volume, healthcheck via `pg_isready` |
| `step-ca` | `${STEP_CA_IMAGE:-smallstep/step-ca:0.30.2}` | `step-ca-data` volume, bootstrapped by `scripts/step-ca-bootstrap.sh`, publishes `9443` |
| `step-ui` | built from `backend/Dockerfile` | `step-ui-certs`, `step-ui-ssl`, `step-ui-data`, `step-ui-uploads` volumes, plus a read-only mount of `step-ca-data` at `/home/step`, publishes `${UI_HTTPS_PORT:-443}` mapped to the container's `8443` |

All three share a bridge network named `step-network` (compose service key `step-net`), pinned to `${STEP_NET_SUBNET:-172.28.0.0/24}`.

Several `compose.e2e-*.yml` and `compose.phase0-spike.yml` overlay files exist for CI and local e2e runs only, they are not part of a normal deployment. See [docs/development.md](development.md#end-to-end-tests-playwright) for what each one does.

## Secrets

`make setup` creates `secrets/` (mode 700) and three files, each mode 644 on the host (not 600: a plain, non-Swarm `docker compose` bind-mount preserves the host file's own owner and mode rather than remapping it the way Swarm secrets do, and `step-ui` runs as a non-root uid, so a 600 file owned by the host user would be unreadable inside the container):

| File | Used for |
|---|---|
| `secrets/postgres_password` | PostgreSQL password |
| `secrets/secret_key` | Session/CSRF signing key |
| `secrets/ca_password` | step-ca provisioner password |

Each is mounted read-only at `/run/secrets/<name>` and never appears in `docker inspect`'s environment output or `/proc/<pid>/environ`. Regenerate them with `make setup FORCE=1`.

## Container image

Published as `ghcr.io/andremmfaria/step-ca-ui`, built by `.github/workflows/docker-build.yml`:

| Trigger | Pushed? | Tags |
|---|---|---|
| Push to `main` | Yes | `main` (branch ref), short commit SHA |
| Push of a `v*` tag | Yes | full semver, `{major}.{minor}` |
| Pull request | No, build only | PR ref (computed, never pushed) |

`backend/Dockerfile` is a two-stage build: `golang:1.26.8-alpine3.23` compiles a static (`CGO_ENABLED=0`) binary, then `alpine:3.23` runs it as a non-root user (uid/gid `10001`). The runtime image installs `curl`, `openssl`, `ca-certificates`, `netcat-openbsd`, `tzdata` and `postgresql-client` (the last for `pg_dump` in the backup handler), and nothing named `step`: certificate issuance, renewal and revocation go through `github.com/smallstep/certificates/ca` and `.../api` directly. `openssl` stays only because the admin diagnostic console has an `openssl.version` entry.

```bash
docker pull ghcr.io/andremmfaria/step-ca-ui:main
```

To build locally without pushing:

```bash
docker build -f backend/Dockerfile backend
```

Running standalone (without Compose) needs a reachable PostgreSQL instance and a running step-ca. For most deployments, use `docker-compose.yml` via `make up`.

## Makefile targets

From `make help` (`.DEFAULT_GOAL := help`, self-documenting via `## ` comments):

| Target | Description |
|---|---|
| `help` | Show this help |
| `setup` | Bootstrap: copy `.env.example` and generate `secrets/` |
| `up` | Build images and start all services in detached mode |
| `down` | Stop and remove containers (volumes preserved) |
| `restart` | Stop then start all services |
| `logs` | Stream logs from all services |
| `ps` | Show container status |
| `update` | Pull latest images and rebuild |
| `backup` | Dump the database and named volumes into `backups/<timestamp>/`, with a SHA-256 manifest |
| `test` | Run Go tests with the race detector |
| `build` | Build the Go binary |
| `fmt` | Format Go source with gofumpt |
| `lint` | Run golangci-lint and check formatting |
| `cover` | Run the coverage gate |
| `openapi` | Regenerate `backend/openapi/openapi.json` from the huma-registered operations |
| `hooks` | Register the `openapi.json` merge driver in this clone's git config |
| `e2e-install` | Install the e2e harness deps (plus Chromium for a host-side run) |
| `e2e-fresh` | Destroy every e2e volume and bring the stack back up healthy |
| `e2e-main` | PR-tier suite: `api` then `ui`, against the long-lived stack |
| `e2e-quick` | Pre-push subset, `api` only, roughly 2 minutes |
| `e2e-bootstrap` | Run one bootstrap scenario: `make e2e-bootstrap SCENARIO=fingerprint` |
| `e2e-restart-ui` | Restart step-ui, clearing both process-local rate limiters |
| `e2e-reset-ssl` | Remove the `step-ui-ssl` volume only, leaving users and CA state |
| `e2e-seed-history` | Insert N synthetic `cert_history` rows: `make e2e-seed-history N=25` |
| `e2e-le-certs` | Generate the local ACME server's TLS material for the Let's Encrypt leg |
| `clean` | Remove build artifacts and old backups (`secrets/` and `.env` are untouched) |

`make backup`'s manifest format differs from the admin UI's own backup export (`backend/handlers/backup.go`), the two are separate implementations that both produce a SHA-256-checksummed manifest.

## `make update`

```bash
make backup   # snapshot first
make update   # docker compose pull + up -d --build
```

Database migrations run automatically on startup (`backend/db/schema.go`).

See also: [docs/configuration.md](configuration.md), [docs/development.md](development.md), [docs/security.md](security.md).
