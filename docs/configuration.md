# Configuration

Startup sequence, TLS bootstrap, and the full environment variable reference, all sourced from `backend/config/config.go`, `backend/entrypoint.sh`, `backend/main.go` and `docker-compose.yml`.

All configuration lives in `.env`. `make setup` creates it from `.env.example` and generates `secrets/` (see [docs/deployment.md](deployment.md)). `.env.example` carries the same reference this page does, inline as comments.

## Startup sequence

`backend/entrypoint.sh` runs before the Go binary starts:

1. **Secret files.** Read `POSTGRES_PASSWORD`, `SECRET_KEY`, `PROVISIONER_PASSWORD` from `secrets/` (`*_FILE` path, default `/run/secrets/<name>`), falling back to a plain-env value if already set.
2. **`DATABASE_URL` construction.** Assembled from the `POSTGRES_*` parts if `DATABASE_URL` is not already set. Aborts if neither `DATABASE_URL` nor a postgres password is available.
3. **PostgreSQL wait.** Host and port are parsed out of `DATABASE_URL` (works with any backend, not just the compose service name) and probed with `nc` up to 60 times, 1 second apart. Bounded and non-fatal: if postgres is not yet reachable, startup continues and the Go app's own connection retry takes over.
4. **Provisioner password file.** Writes `PROVISIONER_PASSWORD` to `$PASSWORD_FILE` if that file does not already exist.
5. **`exec /opt/step-ui/step-ui`.**

Everything past that point, root CA trust and the UI's own TLS certificate, is handled **in-process** by the Go binary (`backend/main.go`, `backend/tlsbootstrap.go`), not by the entrypoint script. It runs synchronously before the HTTPS listener starts.

`main.go` also runs a set of startup checks before any of this: it refuses to start if `SECRET_KEY` is weak, if `TRUST_PROXY=true` without a usable `TRUSTED_PROXY_CIDRS`, or if `OIDC_DEFAULT_ROLE` names something other than `viewer`, `manager`, `admin` or empty. It logs (but does not fail on) an unset `ALLOWED_DOMAIN_SUFFIXES`, a `UI_CERT_DURATION` under 10 minutes, and an `LE_ACME_DIRECTORY_URL` that isn't Let's Encrypt's production directory.

## Root CA trust

`main.go` establishes the root certificate the Go app uses to verify step-ca, through `github.com/smallstep/certificates/ca`. No `step` binary is involved. Three methods, applied in this precedence order:

1. **`CA_ROOT_CERT_PEM` set.** The inline PEM is written to `$ROOT_CERT`. Use this for ECS/Kubernetes deployments where a Docker volume mount is impractical.
2. **`CA_FINGERPRINT` set and `$ROOT_CERT` absent or empty.** An unauthenticated, fingerprint-verified fetch against step-ca, retried up to 30 times, 1 second apart. On failure a warning is logged and startup continues.
3. **Neither set.** The file at `$ROOT_CERT` is assumed to already exist (for example, mounted read-only from the step-ca container, which is what `docker-compose.yml` does).

`CA_FINGERPRINT` is the SHA-256 hex digest of the root certificate's DER encoding. It is public and non-secret, it only authenticates which certificate to trust on download.

## TLS certificate modes

`UI_TLS_MODE` controls how the UI's own serving certificate is obtained:

| `UI_TLS_MODE` | Behaviour |
|---|---|
| `self-signed` (default) | Generates a 10-year self-signed EC P-256 cert on first boot via `crypto/x509`, if `$SSL_CERT` is absent. SANs: `IP:$HOST_IP`, `DNS:localhost`, and `DNS:$UI_HOSTNAME` when set |
| `provided` | Does nothing. Expects a cert and key to already exist at `$SSL_CERT` / `$SSL_KEY` |
| `stepca` | Requests a signed leaf from the step-ca this UI manages, via the smallstep library. Retried up to 30 times, 1 second apart, falls back to self-signed on failure. After a successful issue, a background renewal goroutine re-issues at roughly two thirds of the certificate's validity window |

`UI_HOSTNAME` sets the certificate subject (CN) and an extra DNS SAN. For `self-signed` it is added to the SAN list. For `stepca` it is the requested CN/SAN, falling back to the OS-reported hostname when empty.

## TLS hot-reload

`backend/tlsreload.go` re-stats both `SSL_CERT` and `SSL_KEY` on every TLS handshake. When either file's modification time changes, it reloads the pair in place via `tls.LoadX509KeyPair`. If the reload fails, the last successfully loaded certificate keeps serving, no handshake is ever dropped. This works for the `stepca` renewal goroutine, a manual file replacement in the `step-ui-ssl` volume, or any external cert-manager writing to the mounted path.

## Environment variable reference

Every variable below is read in `backend/config/config.go` unless noted otherwise. "In compose" says whether the stock `docker-compose.yml` actually passes the variable through to the `step-ui` container, since setting a key only in `.env` has no effect unless `docker-compose.yml`'s `environment:` block references it.

### Core deployment

| Variable | Default | In compose | Description |
|---|---|---|---|
| `HOST_IP` | `127.0.0.1` (config default, `.env.example` ships `192.168.1.100` instead) | Yes | SAN in the self-signed cert and DNS name for step-ca |
| `UI_HTTPS_PORT` | `443` | Yes (host port mapping only) | Host port mapped to the container's `8443` |
| `PORT` | `8443` | Yes (hardcoded, not from `.env`) | Internal port the Go app listens on |
| `TZ` | `UTC` | Yes | Container timezone, applied to all three containers |
| `STEPUI_ADMIN_PASSWORD` | none, **required on first boot** | Yes | Seeds the initial admin user when no user rows exist yet. `backend/db/schema.go`'s `resolveAdminPassword` calls `log.Fatal` if no admin exists and this is empty. There is no `Admin123!`-style implicit default in the code, despite what `.env.example`'s comment says (see the note below). Remove from `.env` after first login |

`.env.example`'s comment claims an empty value defaults to a placeholder password. That does not match `backend/db/schema.go`: if no admin user exists yet and `STEPUI_ADMIN_PASSWORD` is empty, the database migration step calls `log.Fatal` and the container will not come up. Set `STEPUI_ADMIN_PASSWORD` before the first `make up`.

### step-ca connection

| Variable | Default | In compose | Description |
|---|---|---|---|
| `CA_URL` | `https://step-ca:9443` | Yes (hardcoded) | Base URL of the step-ca API |
| `ROOT_CERT` | `/home/step/certs/root_ca.crt` | Yes (hardcoded) | Path to the step-ca root certificate inside the container |
| `CA_FINGERPRINT` | empty | Yes | SHA-256 hex fingerprint of the root cert DER. Public, non-secret. See [Root CA trust](#root-ca-trust) |
| `CA_ROOT_CERT_PEM` | empty | Yes | Inline PEM of the root CA certificate, highest-precedence root-trust method |
| `PROVISIONER` | `admin` | Yes | step-ca provisioner identifier used for certificate requests |
| `PASSWORD_FILE` | `/opt/step-ui/data/provisioner_password` | Yes (hardcoded) | Path to the provisioner password file inside the container |
| `PROVISIONER_PASSWORD` | empty | entrypoint.sh only | Plain-env fallback, written to `$PASSWORD_FILE` by the entrypoint when the file does not exist. Prefer `secrets/ca_password` |
| `PROVISIONER_PASSWORD_FILE` | `/run/secrets/ca_password` | Yes (as `PROVISIONER_PASSWORD_FILE`, entrypoint.sh only) | Docker secret path for the provisioner password |
| `STEP_CA_IMAGE` | `smallstep/step-ca:0.30.2` | Yes | step-ca image tag, also used for CA integrity verification |
| `STEPCA_DEFAULT_TLS_CERT_DURATION` | `8760h` | Yes | Read by `scripts/step-ca-bootstrap.sh` and the step-ca container directly, not by the Go app. Default cert lifetime applied during step-ca initialisation |
| `STEPCA_MAX_TLS_CERT_DURATION` | `87600h` | Yes | Same script/container. Maximum cert lifetime applied during step-ca initialisation |

### TLS: UI serving certificate

| Variable | Default | In compose | Description |
|---|---|---|---|
| `UI_TLS_MODE` | `self-signed` | Yes | `self-signed`, `provided`, or `stepca`. See [TLS certificate modes](#tls-certificate-modes) |
| `UI_HOSTNAME` | empty | Yes | Hostname added as DNS SAN (self-signed) or used as CN (stepca) |
| `SSL_CERT` | `/opt/step-ui/ssl/server.crt` | n/a, hardcoded in config.go | Path to the serving TLS certificate |
| `SSL_KEY` | `/opt/step-ui/ssl/server.key` | n/a, hardcoded in config.go | Path to the serving TLS private key |
| `USE_HTTPS` | empty (auto-detect) | n/a, not read by entrypoint or compose | Force TLS on (`true`) or off (`false`). Empty probes whether `$SSL_CERT` exists |
| `UI_CERT_DURATION` | `8760h` | **No**, only in `compose.e2e-config.yml` | Validity requested under `UI_TLS_MODE=stepca`. Unparseable or non-positive values fall back to the default with a warning |

### Database

| Variable | Default | In compose | Description |
|---|---|---|---|
| `DATABASE_URL` | constructed from parts | No (constructed by entrypoint.sh) | Full PostgreSQL DSN. Overrides the `POSTGRES_*` parts when set |
| `POSTGRES_HOST` | `postgres` | Yes | Used only when `DATABASE_URL` is not set |
| `POSTGRES_PORT` | `5432` | Yes | Used only when `DATABASE_URL` is not set |
| `POSTGRES_USER` | `stepui` | Yes | Used only when `DATABASE_URL` is not set |
| `POSTGRES_DB` | `stepui` | Yes | Used only when `DATABASE_URL` is not set |
| `POSTGRES_PASSWORD` | empty | entrypoint.sh only | Plain-env fallback for the database password. Prefer `secrets/postgres_password` |
| `POSTGRES_PASSWORD_FILE` | `/run/secrets/postgres_password` | Yes, entrypoint.sh only | Docker secret path for the database password |

### Application security

| Variable | Default | In compose | Description |
|---|---|---|---|
| `SECRET_KEY` | none, **required** | No plain fallback in compose (file-based only) | Signs session cookies and CSRF tokens. Minimum 32 characters and must not equal the built-in placeholder, or the app refuses to start |
| `SECRET_KEY_FILE` | `/run/secrets/secret_key` | Yes | Docker secret path for `SECRET_KEY`, read by both `entrypoint.sh` and, redundantly, `config.go`'s own `*_FILE` fallback |
| `SESSION_SECURE` | `true` | Yes | Sets the `Secure` flag (and the `__Host-` cookie name prefix, see [docs/authentication.md](authentication.md)) on session and CSRF cookies |
| `ENABLE_HSTS` | `false` | Yes | Sends `Strict-Transport-Security`. Enable only with a trusted, non-self-signed certificate |
| `TRUST_PROXY` | `false` | Yes | Rewrites the client IP from `X-Forwarded-For` / `X-Real-IP` / `True-Client-IP`, but only when the immediate TCP peer itself falls inside `TRUSTED_PROXY_CIDRS` |
| `TRUSTED_PROXY_CIDRS` | empty | Yes | Comma-separated CIDR blocks of trusted proxies. **Required** when `TRUST_PROXY=true`: the app refuses to start otherwise |
| `ALLOWED_DOMAIN_SUFFIXES` | empty (unrestricted) | **No**, only in `compose.e2e-config.yml` | Comma-separated domain suffixes the UI will request a signature for (internal issuance, renewal and the Let's Encrypt path). Matched on label boundaries. This is not an X.509 name constraint: a caller talking to step-ca directly bypasses it |
| `PUBLIC_BASE_URL` | unset | Yes | Externally reachable origin, e.g. `https://ca.example.com`. **Required for password recovery**, emailed links are built from this and never from the request. Unset means reset emails are not sent |

### Let's Encrypt

| Variable | Default | In compose | Description |
|---|---|---|---|
| `LE_ACME_DIRECTORY_URL` | `https://acme-v02.api.letsencrypt.org/directory` | **No**, only in `compose.e2e-le.yml` | ACME directory issuance dials. Env-only on purpose, a manager-editable database setting must not be able to repoint where issuance actually goes |

### Local password login

| Variable | Default | In compose | Description |
|---|---|---|---|
| `LOCAL_LOGIN_ENABLED` | `true` | **No** | Shows the username/password form. See below |

### OIDC SSO

| Variable | Default | In compose | Description |
|---|---|---|---|
| `OIDC_ENABLED` | `false` | **No** | Enable OIDC SSO |
| `OIDC_ISSUER_URL` | empty | **No** | Provider issuer URL |
| `OIDC_CLIENT_ID` | empty | **No** | OAuth2 client ID from the IdP |
| `OIDC_CLIENT_SECRET` | empty | **No** | OAuth2 client secret from the IdP |
| `OIDC_REDIRECT_URL` | empty | **No** | Must resolve to `https://<your-host>/auth/oidc/callback` |
| `OIDC_GROUP_CLAIM` | `groups` | **No** | ID token claim that carries group membership |
| `OIDC_GROUP_ADMIN` | empty | **No** | IdP group name mapped to `admin` |
| `OIDC_GROUP_MANAGER` | empty | **No** | IdP group name mapped to `manager` |
| `OIDC_GROUP_VIEWER` | empty | **No** | IdP group name mapped to `viewer` |
| `OIDC_DEFAULT_ROLE` | empty (deny) | **No** | Role assigned when no group matches. Must be `viewer`, `manager`, `admin` or empty, or startup fails |
| `OIDC_SYNC_ROLE` | `true` | **No** | Re-syncs role from IdP groups on every login |

**None of the OIDC variables, nor `LOCAL_LOGIN_ENABLED`, appear in the stock `docker-compose.yml`'s `step-ui` service.** Setting them in `.env` alone does nothing: `docker compose config` confirms none of these keys reach the container. To enable OIDC against the stock compose file you need to add the corresponding `environment:` lines to the `step-ui` service yourself. `compose.e2e-oidc.yml` in the repository root shows the exact key list this is exercised against in CI (against a mock IdP), and is a reasonable template to copy from. See [docs/authentication.md](authentication.md) for the login and group-mapping behaviour these variables configure.

After changing `.env` (for a variable the compose file does pass through), recreate the containers:

```bash
docker compose up -d --force-recreate
```

See also: [docs/authentication.md](authentication.md), [docs/security.md](security.md), [docs/deployment.md](deployment.md).
