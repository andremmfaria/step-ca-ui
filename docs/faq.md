# FAQ

Only entries still true against the current codebase.

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

Use the admin UI: **Admin > Backup > Download backup bundle**, or the CLI:

```bash
make backup
```

Both produce a SHA-256-checksummed manifest, through two separate implementations that cover different ground. The admin UI (`backend/handlers/backup.go`) archives a `pg_dump`, `/home/step` (the step-ca-data mount), `/opt/step-ui/data`, certificates and uploads. `make backup` archives all six named Docker volumes, including `postgres-data` and `step-ui-ssl` (the UI's own TLS material), which the admin-UI export does not cover, alongside its own `pg_dump`. Restore is manual by design: unpack the archive, restore the PostgreSQL dump, and restore the Docker volumes.
</details>

<details>
<summary><b>How do I reset the admin password?</b></summary>

**Self-service (requires SMTP and `PUBLIC_BASE_URL` configured):** use the **Forgot password?** link on the login page. SMTP is configured as a database setting from the admin UI's notifications page (host, from address, and so on, `backend/handlers/password_reset.go:94`), not an environment variable, only `PUBLIC_BASE_URL` is env. A single-use reset link is emailed and expires after 30 minutes.

**Database reset (no email required):**

```bash
docker compose exec postgres psql -U stepui -d stepui -c \
  "UPDATE users SET password_hash = encode(sha256('newpass'::bytea), 'hex'), \
     totp_enabled = false, totp_secret = '', is_active = true \
   WHERE username='admin';"
```

Log in with `admin` / `newpass` and change it from the UI. `backend/security/security.go`'s `VerifyPassword` still recognizes a 64-hex-character SHA-256 value as a legacy hash and accepts it, then transparently rehashes it to bcrypt after a successful login.

The `totp_enabled`/`totp_secret` reset and `is_active = true` matter even if you only meant to reset the password: login checks `is_active` before the password at all (`backend/handlers/auth.go:102`), and a stale `totp_enabled=true` sends a successful password check straight to the 2FA step (`backend/handlers/auth.go:114`) which you may no longer be able to complete.
</details>

<details>
<summary><b>The browser warns about a self-signed certificate. How do I use my own?</b></summary>

Three approaches, depending on how you obtain the certificate:

**Option 1, operator-provided cert (static):** set `UI_TLS_MODE=provided` and mount your certificate and key into the `step-ui-ssl` volume at `/opt/step-ui/ssl/server.crt` and `server.key`. `SSL_CERT` and `SSL_KEY` are not environment variables the app reads, despite appearing in `.env.example`, these paths are hardcoded in `backend/config/config.go`, so setting them in `.env` has no effect. The entrypoint does not touch those files. Recreate the container to pick up the change.

**Option 2, manual replacement (hot-swap, no restart required):** replace the cert and key files inside the `step-ui-ssl` volume with your own. The TLS hot-reloader (`backend/tlsreload.go`) re-stats both files on every handshake and reloads them automatically when their modification time changes.

**Option 3, cert from step-ca itself:** set `UI_TLS_MODE=stepca`. The Go app requests a signed leaf from step-ca on startup and renews it in a background goroutine, the hot-reloader picks up each renewal with zero downtime. Requires `CA_URL`, `CA_FINGERPRINT` (or a pre-populated `ROOT_CERT`), and the provisioner password.
</details>

<details>
<summary><b>Can I run this behind Cloudflare / Caddy / nginx?</b></summary>

Yes. Point your reverse proxy at `step-ui:8443` (HTTPS upstream).

Set `TRUST_PROXY=true` and `TRUSTED_PROXY_CIDRS` to the proxy's own address (a `/32` or `/128` unless it's a pool) in `.env`. Both reach the container through `docker-compose.yml`'s `environment:` block already. `TRUST_PROXY=true` with an empty or unusable `TRUSTED_PROXY_CIDRS` is a startup fatal, by design, see [docs/security.md](security.md#application-security-controls).

Set `PUBLIC_BASE_URL` to the origin your users actually reach, e.g. `https://ca.example.com`. Emailed password-reset links are built from it, deliberately never from `Host` or `X-Forwarded-Proto`, so forwarding those headers does not affect the links.
</details>

<details>
<summary><b>How do I update?</b></summary>

```bash
make backup  # snapshot first
make update  # docker compose pull + up -d --build
```

Database migrations run automatically on startup.
</details>

<details>
<summary><b>Why doesn't setting OIDC_ENABLED in .env do anything?</b></summary>

The stock `docker-compose.yml` does not pass any `OIDC_*` variable, or `LOCAL_LOGIN_ENABLED`, through to the `step-ui` container. You need to add those `environment:` lines to the `step-ui` service yourself, see [docs/configuration.md](configuration.md#oidc-sso) for the full list and [docs/authentication.md](authentication.md#oidc-sso) for the setup checklist.
</details>

<details>
<summary><b>The container won't start: "No admin user exists and STEPUI_ADMIN_PASSWORD is not set". What now?</b></summary>

This is a first-boot fatal, not a bug: `backend/db/schema.go` refuses to seed an admin user with a made-up password (`schema.go:187-193`). Set `STEPUI_ADMIN_PASSWORD` in `.env` to a strong password and start the stack again. Once the admin user exists, remove the variable from `.env`, it is only consulted while the `users` table is empty.
</details>

See also: [docs/configuration.md](configuration.md), [docs/authentication.md](authentication.md), [docs/deployment.md](deployment.md).
