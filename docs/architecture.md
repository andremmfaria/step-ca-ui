# Architecture

What runs in production today, the request path, and the state of the SPA migration.

## Components today

Three containers, wired by `docker-compose.yml`:

| Container | Image / build | Role |
|---|---|---|
| `step-ui` | built from `backend/Dockerfile` | Go binary. Server-rendered HTML UI (chi router, `html/template`) plus a small versioned JSON API. Owns sessions, CSRF, rate limiting, and all step-ca/database access |
| `postgres` | `postgres:16-alpine` | Users, certificate history, Let's Encrypt state, notification settings, password-reset tokens |
| `step-ca` | `smallstep/step-ca:0.30.2` (pinned via `STEP_CA_IMAGE`) | The actual PKI. step-ui never runs the `step` CLI against it: `backend/stepca/` wraps `github.com/smallstep/certificates/ca` and `github.com/smallstep/certificates/api` directly (pinned to the same v0.30.2), so certificate issuance, renewal and revocation are Go library calls, not shelled-out subprocesses |

`backend/Dockerfile` ships `openssl` only because the admin diagnostic console has an "OpenSSL version" entry (`openssl.version`, see [docs/security.md](security.md)). No `step` binary is installed in the image.

## Request flow

```
                          ┌────────────┐
   Browser  ─── HTTPS ───►│  step-ui   │  Go binary, chi router, port 8443
                          │  (chi)     │
                          └──┬─────┬───┘
                             │     │
                  SQL ◄──────┘     └──────► HTTPS API (ca library)
                             │     │
                          ┌──▼──┐ ┌▼──────────┐
                          │ pg  │ │ step-ca   │  port 9443
                          │ 16  │ │ (PKI)     │
                          └─────┘ └───────────┘

   step-ui exposes :443 (UI_HTTPS_PORT) → internally listens on :8443
   step-ca exposes :9443 → internal-only unless you publish it yourself
```

Inside `step-ui`, one `chi.Router` serves two things on the same port:

- The server-rendered UI: `/login`, `/dashboard`, `/certificates`, `/admin/*`, `/le/*`, `/profile/*`, and so on, rendered from `backend/templates/` with `html/template`.
- A versioned JSON API mounted at `/api/v1` by `humaapi.Mount` (`backend/api/`), described by an OpenAPI 3.1 document at `backend/openapi/openapi.json`.

`/health` (liveness, always 200 while the process is up) and `/ready` (readiness, checks a database ping and step-ca reachability, answers 503 with a body naming which one failed) sit outside both, unauthenticated.

## Roles

| Role | View | Issue / Import | Revoke | Manage users |
|---|---|---|---|---|
| `viewer` | Yes | No | No | No |
| `manager` | Yes | Yes | No | No |
| `admin` | Yes | Yes | Yes | Yes |

Temporary users (`backend/handlers`, expiry checked by a background goroutine every 60 seconds) can hold any of the three roles and are auto-deactivated once their `expires_at` timestamp passes.

## The `/api/v1` surface today

This is genuinely small. `backend/api/register.go` registers exactly:

| Method | Path | Auth | Purpose |
|---|---|---|---|
| GET | `/api/v1/session` | optional | Current session state, exempt from idle-timeout renewal so an open tab cannot keep a session alive forever |
| DELETE | `/api/v1/session` | public (CSRF-checked only when a session is present) | Log out, expiring the session and CSRF cookies |
| GET | `/api/v1/config` | public | Public runtime flags: `oidcEnabled`, `oidcButtonLabel`, `acmeEnabled`, `appVersion`, `contractSha`, `roleLevels`, `sessionIdleTimeoutSeconds`, `expiringSoonDays`. Note `oidcButtonLabel` and `acmeEnabled` are spike placeholders with no backing config field yet |
| GET | `/api/v1/status` | `viewer` | Active/expiring certificate counts |
| GET | `/api/v1/_spike/blob` | `viewer` | Proves the binary-download response mechanic a later phase reuses |
| POST | `/api/v1/_spike/upload` | `manager` | Proves the multipart-upload mechanic a later phase reuses |

An unmatched path under `/api/v1` gets an RFC 9457 problem document instead of a 404 page.

## Migration state

The repository is partway through [plans/frontend-backend-split.md](../plans/frontend-backend-split.md) (revision 10 at time of writing), which replaces the server-rendered UI above with a React SPA served by its own container, talking to `step-ui` only through the versioned JSON API. Phases 0, 1 and 2 are complete and merged. **The server-rendered UI described above is what actually serves users today**, nothing in `frontend/` is deployed.

`frontend/` is a Phase 0 throwaway spike: it boots, calls `getSession` through the generated client, and renders the session-state discriminator, nothing more. It uses React 19.2.0, Vite 8.2.2 and `@vitejs/plugin-react` 6.1.1.

### The generated TypeScript client

`clients/ts` (`@andremmfaria/step-ca-ui-client`) is generated from `backend/openapi/openapi.json` by `@hey-api/openapi-ts` on every push and pull request, in the `client` job of `.github/workflows/ci.yml`. It is published to GitHub Packages only on a `client-v*` tag.

The version is never hand-edited. `scripts/client-version.sh` derives `0.<MINOR>.<PATCH>-sha.<short-sha>`: `MINOR` comes from `backend/openapi/package-version.txt` (bumped by hand on a deliberate break), `PATCH` is `git rev-list --count origin/main`, so two pull requests can never collide on the same number, and the short commit SHA is appended so a consumer can prove which commit it was generated from. The `frontend` CI job asserts the installed client's version string ends in the current commit's short SHA before it will typecheck or build (this is what `docs/contract-proof.md` demonstrates end to end with a deliberately regressed commit).

`frontend` never declares the client as a package.json dependency (D8 of the plan): CI installs the freshly packed tarball with `npm install --no-save` so the client under test is always the one generated from the commit being built, never a stale published version from an npm cache.

### Contract documentation

- [docs/contract-changes.md](contract-changes.md) is an append-only log of every breaking API change, gated by `scripts/contract-gate.sh` (backed by `oasdiff`) so a breaking change without a matching row fails CI.
- [docs/contract-proof.md](contract-proof.md) is a two-commit worked proof that the provenance check above actually rejects a client that doesn't match the commit under test, not just that the check exists.

See also: [docs/configuration.md](configuration.md), [docs/deployment.md](deployment.md), [docs/development.md](development.md).
