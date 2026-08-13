# e2e implementation status

Tracks `plans/e2e-tests.md` as it lands. Sections refer to that document.

## Done

**Phase 0, application prerequisites (Section 2.7.4).** All three landed.

- `UI_CERT_DURATION`: `config.UICertDuration` via a new `getEnvDuration` (default `8760h`, invalid or non-positive falls back with a warning). `tlsbootstrap.go` dropped the `uiIssueDuration` constant; `nextRenewalSleep` gained a one-minute floor so a short duration cannot busy-spin. `main.go` warns under 10 minutes. Compose passthrough is `compose.e2e-config.yml`; both halves are required or the key is a silent no-op.
- `LE_ACME_DIRECTORY_URL`: `config.LEACMEDirectoryURL` (default the production directory), `le.LEConfig.DirectoryURL` overriding the `LEProductionCA`/`LEStagingCA` selection, passed at all three construction sites. `AddLELog` records the directory on issuance start. `main.go` warns when non-default.
- `Cache-Control: no-store` globally in `mw.SecurityHeaders`. Verified live: `curl -sk -D- https://localhost:8443/login` shows the header. **E2E-SEC-06's skip-until-fixed contract can be lifted.**

**Phase 1, compose overrides and Makefile.** All seven overrides from Section 2.7.1 plus `compose.e2e-fatals.yml` (`restart: "no"`, E2E-BOOT-07's precondition). Every merge verified with `docker compose config`; `!override` and `!reset` both behave as the plan assumes on compose v5.1.4. Makefile targets: `e2e-install`, `e2e-fresh`, `e2e-main`, `e2e-quick`, `e2e-bootstrap`, `e2e-restart-ui`, `e2e-reset-ssl`, `e2e-seed-history`, `e2e-le-certs`.

**Phase 2, harness.** `test/e2e` as its own Node project: `@playwright/test` 1.62.1, three projects (`api`/`ui`/`infra`) selected by `testMatch`, retries 0, `ignoreHTTPSErrors`, junit pinned into the collected artifact path. Harness `Dockerfile` on `mcr.microsoft.com/playwright:v1.62.1-noble` pinned by digest, plus `docker-ce-cli`, the compose plugin, `openssl` and `postgresql-client`. Helpers: compose/exec/psql/logs with a cumulative capture across recreates, bounded polling, openssl probe, TOTP with a boundary guard, QR decode, env-file editing, router-derived POST route list. Fixtures: worker-scoped `admin`/`manager`/`viewer` with lazy re-auth, `jarB`, disposable users. Scripts: `scenario.sh`, `collect.sh`, `assert-redacted.sh`.

**Phase 3, specs, first tranche.** E2E-AUTH-01, E2E-AUTH-11, E2E-RBAC-01, E2E-RBAC-02, E2E-CSRF-01, E2E-CSRF-05. 95 passing, 8 skipped (LE-gated cells) against a live local stack.

## Found by the suite

**`/admin` served 200 with an empty body.** `admin.html` had two `<a>` tags missing their closing `>` (`class="…"Manage →</a>`), so `html/template` refused to parse the admin set and `h.render` logged `template render failed` while writing nothing. Every admin landing page visit was blank. Fixed in `step-ui-go/templates/admin.html`; a sweep found no other instance.

## Corrections to the plan

- **E2E-AUTH-11's `Max-Age=-1`.** That is the Go-side `http.Cookie.MaxAge` value. `net/http` serialises any negative `MaxAge` as `Max-Age=0` with a 1970 `Expires`, so the assertion is against the wire form.
- **The issuance form field is `name`, not `cert_name`** (`handlers/certs.go`). Nothing in the plan states it, and a wrong field name reaches the "Please fill in all fields" branch, which renders inline at 200 rather than redirecting.
- **Flash messages are one-shot.** Any test that checks status and then re-fetches to read the flash consumes it on the first fetch.

## Local run

`.env` uses `UI_HTTPS_PORT=8443` and `HOST_IP=127.0.0.1`. The `secrets/*` files needed `chmod 644`: they were mode 600 owned by uid 1002, while step-ca runs as uid 1000 and step-ui as uid 10001, so both were denied. Run the api project from the host with `BASE_URL=https://localhost:8443`; per-test rate-limit isolation needs the container on `step-network` instead.

## Remaining

- PR tier: E2E-CERT-01 to -13, E2E-PROV-01/02, E2E-HIST-01 to -03, E2E-SEC-01 to -06, E2E-ADM-01 to -08, E2E-BAK-01, E2E-HLTH-01 to -06, E2E-CFG-01, E2E-STATIC-01, E2E-TEMP-01/02, E2E-AUTH-02 to -07, -12, -14, -15.
- `infra` project: the five bootstrap scenarios (E2E-BOOT-01 to -09).
- `ui` project: the four browser companions (E2E-CERT-01, E2E-AUTH-04, E2E-AUTH-11, E2E-CFG-01).
- Nightly legs: `renew`, `oidc-mail`, `le`, `bootstrap-extra`.
- Phase 4: the workflow in Appendix B, plus the Section 4.4 changes to `security.yml` and `lint-meta.yml`.
