# Backup and Restore

This document describes the supported backup format for Step-CA UI and the
manual restore procedure. Restore from the web UI is intentionally not provided:
it can overwrite CA private keys and the application database.

## What Must Be Backed Up

A complete backup must contain:

- PostgreSQL database dump: users, certificate metadata, history and logs.
- `step-ca-data`: Smallstep CA configuration, certificates, keys and secrets.
- `step-ui-data`: provisioner password and application data.
- `step-ui-certs`: issued certificate/key files stored by Step-CA UI.
- `step-ui-uploads`: imported certificate files.
- `manifest.json`: timestamp, version, component sizes and SHA-256 checksums.

The backup contains CA private keys. Store it as a secret.

## Export From UI

1. Log in as an admin.
2. Open `Admin -> Backup`.
3. Click `Download backup bundle`.
4. Store the downloaded `.tgz` outside the server.

The UI bundle contains:

```text
manifest.json
postgres-stepui.sql
step-ca-data.tgz
step-ui-data.tgz
step-ui-certs.tgz
step-ui-uploads.tgz
```

## Export From CLI

From the project directory:

```bash
sudo ./install.sh --mode backup --lang en
```

The CLI backup is written to:

```text
./backups/manual-YYYYMMDD_HHMMSS/
```

`./backups/LATEST` points to the latest backup directory.

## Verify A Backup

For a UI bundle:

```bash
mkdir -p /tmp/step-ca-ui-restore-check
tar -xzf step-ca-ui-backup-*.tgz -C /tmp/step-ca-ui-restore-check
cd /tmp/step-ca-ui-restore-check
sha256sum -c <(jq -r '.components[] | select(.status=="ok") | "\\(.sha256)  \\(.path)"' manifest.json)
```

If `jq` is not installed, inspect `manifest.json` manually and compare:

```bash
sha256sum postgres-stepui.sql step-ca-data.tgz step-ui-data.tgz step-ui-certs.tgz step-ui-uploads.tgz
```

## Restore On A Clean Server

Assumptions:

- Docker and Docker Compose are installed.
- The project is present in `/opt/step-ca-ui`.
- The UI backup bundle is copied to `/root/step-ca-ui-backup.tgz`.

Stop the current stack and remove only this project's volumes:

```bash
cd /opt/step-ca-ui
sudo docker compose down
sudo docker volume rm \
  step-ca-ui_postgres-data \
  step-ca-ui_step-ca-data \
  step-ca-ui_step-ui-data \
  step-ca-ui_step-ui-certs \
  step-ca-ui_step-ui-uploads \
  step-ca-ui_step-ui-ssl 2>/dev/null || true
```

Unpack the backup:

```bash
mkdir -p /root/step-ca-ui-restore
tar -xzf /root/step-ca-ui-backup.tgz -C /root/step-ca-ui-restore
cd /root/step-ca-ui-restore
```

Recreate volumes and restore file data:

```bash
sudo docker volume create step-ca-ui_step-ca-data
sudo docker volume create step-ca-ui_step-ui-data
sudo docker volume create step-ca-ui_step-ui-certs
sudo docker volume create step-ca-ui_step-ui-uploads

restore_volume() {
  volume="$1"
  archive="$2"
  mountpoint="$(sudo docker volume inspect "$volume" --format '{{.Mountpoint}}')"
  sudo tar -xzf "$archive" -C "$mountpoint"
}

restore_volume step-ca-ui_step-ca-data step-ca-data.tgz
restore_volume step-ca-ui_step-ui-data step-ui-data.tgz
restore_volume step-ca-ui_step-ui-certs step-ui-certs.tgz
restore_volume step-ca-ui_step-ui-uploads step-ui-uploads.tgz
```

Start PostgreSQL first and restore the database:

```bash
cd /opt/step-ca-ui
sudo docker compose up -d postgres
until sudo docker compose exec -T postgres pg_isready -U stepui >/dev/null 2>&1; do sleep 2; done
sudo docker compose exec -T postgres psql -U stepui -d stepui < /root/step-ca-ui-restore/postgres-stepui.sql
```

Start the full stack:

```bash
sudo docker compose up -d --build
sudo docker compose ps
```

Verify:

```bash
curl -k https://localhost/login
sudo docker compose logs --tail=100 step-ca
sudo docker compose logs --tail=100 step-ui
```

Then log in and open `Admin -> About`. Preflight should not report critical
errors.

## Restore From A CLI Backup Directory

CLI backups created by `install.sh --mode backup` are directories, not a single
download bundle:

```text
backups/manual-YYYYMMDD_HHMMSS/
├── .env
├── manifest.json
├── postgres-stepui.sql
├── project-files.tgz
└── volumes/
    ├── step-ca-ui_postgres-data.tgz
    ├── step-ca-ui_step-ca-data.tgz
    ├── step-ca-ui_step-ui-certs.tgz
    ├── step-ca-ui_step-ui-data.tgz
    ├── step-ca-ui_step-ui-ssl.tgz
    └── step-ca-ui_step-ui-uploads.tgz
```

For CLI restore, copy `.env` back to `/opt/step-ca-ui/.env`, then restore each
archive from `volumes/` into the Docker volume with the matching name. The exact
volume prefix can differ if `COMPOSE_PROJECT_NAME` or the project directory name
is different.

## Restore Notes

- Restore must be done with the same `PROVISIONER` and compatible `.env`.
- If `.env` is lost, recreate it before starting the stack. The most critical
  values are `POSTGRES_PASSWORD`, `CA_PASSWORD`, `SECRET_KEY`, `HOST_IP`,
  `PROVISIONER`, and `UI_HTTPS_PORT`.
- Do not restore `step-ca-data` from an untrusted source.
- Do not mix CA data from one installation with a PostgreSQL dump from another
  unless you understand the resulting certificate metadata mismatch.
