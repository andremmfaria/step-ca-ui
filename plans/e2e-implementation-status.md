# e2e implementation status

Tracks `plans/e2e-tests.md` as it lands. Sections refer to that document.

## Done

**Phase 0, application prerequisites (Section 2.7.4).** All three landed.

- `UI_CERT_DURATION`: `config.UICertDuration` via a new `getEnvDuration` (default `8760h`, invalid or non-positive falls back with a warning). `tlsbootstrap.go` dropped the `uiIssueDuration` constant; `nextRenewalSleep` gained a one-minute floor so a short duration cannot busy-spin. `main.go` warns under 10 minutes. Compose passthrough is `compose.e2e-config.yml`; both halves are required or the key is a silent no-op.
- `LE_ACME_DIRECTORY_URL`: `config.LEACMEDirectoryURL` (default the production directory), `le.LEConfig.DirectoryURL` overriding the `LEProductionCA`/`LEStagingCA` selection, passed at all three construction sites. `AddLELog` records the directory on issuance start. `main.go` warns when non-default.
- `Cache-Control: no-store` globally in `mw.SecurityHeaders`. Verified live: `curl -sk -D- https://localhost:8443/login` shows the header. **E2E-SEC-06's skip-until-fixed contract can be lifted.**

**Phase 1, compose overrides and Makefile.** All seven overrides from Section 2.7.1 plus `compose.e2e-fatals.yml` (`restart: "no"`, E2E-BOOT-07's precondition). Every merge verified with `docker compose config`; `!override` and `!reset` both behave as the plan assumes on compose v5.1.4. Makefile targets: `e2e-install`, `e2e-fresh`, `e2e-main`, `e2e-quick`, `e2e-bootstrap`, `e2e-restart-ui`, `e2e-reset-ssl`, `e2e-seed-history`, `e2e-le-certs`.

**Phase 2, harness.** `test/e2e` as its own Node project: `@playwright/test` 1.62.1, three projects (`api`/`ui`/`infra`) selected by `testMatch`, retries 0, `ignoreHTTPSErrors`, junit pinned into the collected artifact path. Harness `Dockerfile` on `mcr.microsoft.com/playwright:v1.62.1-noble` pinned by digest, plus `docker-ce-cli`, the compose plugin, `openssl` and `postgresql-client`. Helpers: compose/exec/psql/logs with a cumulative capture across recreates, bounded polling, openssl probe, TOTP with a boundary guard, QR decode, env-file editing, router-derived POST route list. Fixtures: worker-scoped `admin`/`manager`/`viewer` with lazy re-auth, `jarB`, disposable users. Scripts: `scenario.sh`, `collect.sh`, `assert-redacted.sh`.

**Phase 3, specs, so far.** 24 spec files under `test/e2e/specs/`, covering 40 of the 78 indexed test IDs (verified 2026-09-02 by listing the directory and grepping its `E2E-*` IDs): E2E-AUTH-01, E2E-AUTH-04, E2E-AUTH-07, E2E-AUTH-08, E2E-AUTH-11 to -16, E2E-RBAC-01, E2E-RBAC-02, E2E-CSRF-01, E2E-CSRF-05, E2E-CERT-01 to -04 and -09, E2E-PROV-01/02, E2E-HIST-01 to -03, E2E-SEC-01/02, E2E-ADM-01 to -05, E2E-BAK-01, E2E-HLTH-01 to -06, E2E-STATIC-01, E2E-CFG-01. Four of those files are `ui`-project specs (`cert-01.ui`, `auth-04.ui`, `auth-11.ui`, `cfg-01.ui`, five IDs: CERT-01, AUTH-04, AUTH-07, AUTH-11, CFG-01), landed alongside the three OIDC `api` specs (AUTH-08, -13, -16) in `c7a96d6` on 2026-08-15. The earlier **138 passing, 8 skipped (LE-gated cells) from a cold stack**, run the way CI runs them (the harness image on `step-network`), was recorded against the 34-ID state and was not re-measured for this count: doing so needs the full compose stack described under "Local run" below, out of scope for this pass.

**Phase 4, CI.** `.github/workflows/e2e.yml` is live and green on `main`: an `image` job that builds and caches both images, an `e2e-main` job that brings the stack up and runs the `api` project from the harness container, artifact collection with the redaction sweep, and the `e2e` gate. `lint-meta.yml` gained the harness `tsc --noEmit` and `eslint`. The bootstrap matrix and the `ui` project join the same file when their specs land.

Two things the workflow had to learn the hard way, both now encoded in it:

- The harness runs from `/e2e`, the image's own copy, because the mounted checkout carries no `node_modules`. `E2E_REPO_ROOT=/repo` points the helpers back at the mount.
- `COMPOSE_PROJECT_NAME` is pinned. Compose derives the project from the working directory, which is `/repo` inside the container and the checkout name outside it; left to default, every `docker compose exec` from a test reports "service not running".

## Found by the suite

**`/admin` served 200 with an empty body.** `admin.html` had two `<a>` tags missing their closing `>` (`class="…"Manage →</a>`), so `html/template` refused to parse the admin set and `h.render` logged `template render failed` while writing nothing. Every admin landing page visit was blank. Fixed in `step-ui-go/templates/admin.html`; a sweep found no other instance.

## Corrections to the plan

- **E2E-AUTH-11's `Max-Age=-1`.** That is the Go-side `http.Cookie.MaxAge` value. `net/http` serialises any negative `MaxAge` as `Max-Age=0` with a 1970 `Expires`, so the assertion is against the wire form.
- **The issuance form field is `name`, not `cert_name`** (`handlers/certs.go`). Nothing in the plan states it, and a wrong field name reaches the "Please fill in all fields" branch, which renders inline at 200 rather than redirecting.
- **Flash messages are one-shot.** Any test that checks status and then re-fetches to read the flash consumes it on the first fetch.
- **E2E-HLTH-05's session claim is half right.** The plan says sessions "come back on the next request once postgres is healthy, without a re-login". `rejectSession` clears `s.Values` and Saves, which overwrites the client's cookie, so a session *used* during the outage is dead for good. A session left untouched through the outage does come back. The test now pins both halves.
- **E2E-HLTH-06's row is named `CA URL` on the page**, not `Step-CA API`. The latter is the check's name in `handlers/health.go` and the name E2E-ADM-07's preflight list uses. `/admin/integrity` also renders two tables, and only the second carries status badges.
- **Error text is HTML-escaped in rendered pages.** `validateIdentifier`'s `%q` quotes arrive as `&#34;`, so E2E-CERT-03 compares the escaped form.
- **E2E-BAK-01's `step-ca-data.tgz` keeps a bare `config/` entry.** Section 2.7.5's calibration says `config/` is absent; the directory header is written before the read that fails, so the entry is there with nothing under it. `secrets/`, which sorts after it, is never reached. The test asserts contents, not entries.
- **`manifest.json`'s `components` is an array** of `{name, path, size, sha256, status}`, not an object keyed by name.
- **A stopped container's compose DNS name stops resolving**, so E2E-ADM-03's "dial-level error" arrives as `lookup step-ca ... server misbehaving` rather than `connection refused`.
- **OpenSSL 3 prints DNs as `CN = x`**, older builds as `CN=x`. The host and the harness image disagree, so `helpers/openssl.ts` normalises before any assertion.

## Local run

`.env` uses `UI_HTTPS_PORT=8443` and `HOST_IP=127.0.0.1`. The `secrets/*` files needed `chmod 644`: they were mode 600 owned by uid 1002, while step-ca runs as uid 1000 and step-ui as uid 10001, so both were denied. Run the api project from the host with `BASE_URL=https://localhost:8443`; per-test rate-limit isolation needs the container on `step-network` instead.

## The React and OpenAPI-client question, and a deviation it caused

The React/OpenAPI-client replacement question raised on 2026-08-13 was answered (see `plans/frontend-backend-split.md`): the split is going ahead, Phases 0 and 1 are merged, and Phase 2 began 2026-09-02. This track was never actually paused on that decision: `c7a96d6` landed three more OIDC `api` specs and, more consequentially, all four `ui` companions on 2026-08-15, two days after the pause note above was written.

**Survives unchanged.** Everything the `api` project asserts about status codes, headers, redirects, database rows, the CA's log, issued certificate material and the backup bundle. That is the bulk of what is written so far: RBAC, CSRF, certificates, history, security log, admin console, backup, health, static MIME and traversal, and the session-revocation family.

**Needs rewriting.** Every assertion that reads rendered HTML: the content substrings in E2E-RBAC-01's 200 cells, flash-message text, `extractCSRF`'s hidden-input scrape, the row parsers in the history, security-log, provisioner and integrity specs. A React client would move these to JSON responses, which is a smaller and more stable oracle, but the assertions themselves would be rewritten.

**Changes shape entirely.** CSRF as a hidden form field (E2E-CSRF-01, -05) and the inline logout form (E2E-AUTH-11) are properties of server-rendered forms. An OpenAPI client would carry the token differently, or use a different mechanism altogether.

**Unaffected either way.** The whole Section 3.1 bootstrap matrix, the TLS and renewal work, and the compose, workflow and harness infrastructure. None of it touches the presentation layer.

**Deviation: the four `ui` companions were written, and at the wrong time.** `plans/frontend-backend-split.md` Section 10 says `ui` specs are written last, after Phase 8, once the React markup is stable, on the reasoning that they would otherwise be the first thing thrown away. They were written anyway, in `c7a96d6`, against the server-rendered markup the split plan's Phase 9 deletes: `cert-01.ui.spec.ts`, `auth-04.ui.spec.ts` (covering both E2E-AUTH-04 and E2E-AUTH-07), `auth-11.ui.spec.ts` and `cfg-01.ui.spec.ts`. Consequence: those four files must be rewritten, or retired, in whichever of Phases 4 to 8 replaces the page each one asserts against. Until then they guard the legacy server-rendered UI only, not anything the split is building toward.

## Remaining

- PR tier, not yet written: E2E-AUTH-02, -03, -05, -06, E2E-CERT-05 to -08, -10 to -13, E2E-SEC-03 to -06, E2E-ADM-07, -08, E2E-TEMP-01/02.
- `infra` project: the five bootstrap scenarios (E2E-BOOT-01 to -09), plus the `e2e-bootstrap` matrix job that drives them.
- `ui` project: the four spec files are written (see the deviation above), but `.github/workflows/e2e.yml` has no `ui`-project job step yet, nor the `make e2e-restart-ui` step that must precede it.
- Nightly legs: `renew`, `oidc-mail`, `le`, `bootstrap-extra`. Note E2E-AUTH-08, -13 and -16 are written but gated behind `test.skip` for the `oidc-mail` leg, per their PR-tier-versus-nightly split in `plans/e2e-tests.md` Section 1.3.
- Section 4.4's remaining change: `security.yml`'s `trivy-image` job should build through `docker/build-push-action` with `cache-from: type=gha,scope=e2e` rather than calling `docker build` directly.
- Branch protection: make `e2e` a required check.
