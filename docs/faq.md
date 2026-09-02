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

Both produce a SHA-256-checksummed manifest, through two separate implementations (`backend/handlers/backup.go` for the admin UI, the `backup` Makefile target for the CLI). Restore is manual by design: unpack the archive, restore the PostgreSQL dump, and restore the Docker volumes.
</details>

<details>
<summary><b>How do I reset the admin password?</b></summary>

**Self-service (requires SMTP and `PUBLIC_BASE_URL` configured):** use the **Forgot password?** link on the login page. A single-use reset link is emailed and expires after 30 minutes.

**Database reset (no email required):**

```bash
docker compose exec postgres psql -U stepui -d stepui -c \
  "UPDATE users SET password_hash = encode(sha256('newpass'::bytea), 'hex') WHERE username='admin';"
```

Log in with `admin` / `newpass` and change it from the UI. `backend/security/security.go`'s `VerifyPassword` still recognizes a 64-hex-character SHA-256 value as a legacy hash and accepts it, then transparently rehashes it to bcrypt after a successful login.
</details>

<details>
<summary><b>The browser warns about a self-signed certificate. How do I use my own?</b></summary>

Three approaches, depending on how you obtain the certificate:

**Option 1, operator-provided cert (static):** set `UI_TLS_MODE=provided` and mount your certificate and key at `SSL_CERT` / `SSL_KEY` (default paths `/opt/step-ui/ssl/server.crt` and `server.key`). The entrypoint does not touch those files. Recreate the container to pick up the change.

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

See also: [docs/configuration.md](configuration.md), [docs/authentication.md](authentication.md), [docs/deployment.md](deployment.md).
