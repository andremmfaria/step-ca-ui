# Plan: Split step-ca-ui into a React SPA and an OpenAPI-described Go backend-for-frontend

Status: Draft, revision 3. Written 2026-08-13 against `main` at `a549750`. Three review stages are logged in Section 13.

This plan replaces the server-rendered Go UI in `backend/` with a React single-page application, served by its own nginx container, talking to the Go process over a versioned JSON API. The Go side becomes a backend-for-frontend: it keeps sessions, authorisation, database and CA access, and publishes an OpenAPI 3.1 description that a generated TypeScript client consumes. A CI gate ties the frontend build to that exact description, so a contract change that breaks the frontend fails in the same run rather than at runtime.

Cost: a full rewrite of the user interface across 33 templates, 65 routes and 46 handler files, plus a second deployable, run as ten additive phases on `main` with no long-lived branch. Nothing is deployed today, so there is no rollout risk, only the risk of the migration stalling half-finished. Phase 9, deleting the old UI, is the only irreversible step. **Read Section 1.0 before deciding to proceed**, because the stated motive for this work does not survive contact with the repository and the cheaper alternatives are real.

**All three open questions were answered on 2026-08-13** and the answers are folded into the decisions rather than left in Section 12: the API is versioned even though no second consumer exists yet (5.1), "separate" means two containers with nginx serving the SPA (D1, D5), and there is no deadline (Section 8).

## 0. Using this document

| Section | |
|---|---|
| [1. Objective](#1-objective) | **1.0 Whether to do this at all** · 1.1 What exists today · 1.2 What changes · 1.3 Non-goals · 1.4 What it buys, what gets worse · 1.5 Alternatives to the whole approach |
| [2. Acceptance criteria](#2-acceptance-criteria) | contract · client package · backend · frontend · integration. every box mechanical or an owned judgement |
| [3. Decisions and alternatives](#3-decisions-and-alternatives) | D1 to D12. D1, D2, D4, D6 and D7 record rejected options. D3, D5, D9, D10 and D12 are consequences of those, or of an owner instruction |
| [4. Target architecture](#4-target-architecture) | 4.1 Repository layout · 4.2 Runtime topology and the image · 4.3 Request lifecycle · 4.4 Development loop |
| [5. API conventions](#5-api-conventions) | 5.1 Base path · 5.2 Error model · 5.3 Authentication · 5.4 CSRF · 5.5 Authorisation · 5.6 Lists · 5.7 Binary responses · 5.8 Naming and shapes · 5.9 Frontend conventions |
| [6. Endpoint map](#6-endpoint-map) | every current route to its replacement, with roles |
| [7. The contract pipeline](#7-the-contract-pipeline) | 7.1 Spec generation · 7.2 Drift gate · 7.3 Client generation · 7.4 Packaging · 7.5 Publication · 7.6 Consumption |
| [8. Phases](#8-phases) | Phase 0 to Phase 9, all on `main`. only Phase 9 is irreversible |
| [9. CI and CD](#9-ci-and-cd) | 9.1 Job graph · 9.2 Workflow changes · 9.3 Rollback and recovery |
| [10. Impact on the e2e suite](#10-impact-on-the-e2e-suite) | the agent was stopped 2026-08-13. what survives, and the resumption order |
| [11. Risk register](#11-risk-register) | R1 to R34, each with a trigger, an owner and a status, each with a trigger and an owner phase unless explicitly parked |
| [12. Open questions](#12-open-questions) | all answered 2026-08-13, with what each answer changed |
| [13. Review log](#13-review-log) | what the review changed |

---

## 1. Objective

### 1.0 Whether to do this at all

The stated motive for this work is that the current architecture is "not very scalable". Nothing in the repository supports a load reading of that. There are no live deployments, no operators, one tenant, no per-object ownership, and one Go process serving 65 routes off one Postgres instance. Splitting the UI out will not make the system serve more traffic, and D1 deliberately keeps one deployable, so it does not buy independent scaling of the two halves either.

What the split does buy is **development scaling**: one typed contract instead of 33 hand-maintained templates and 16 untyped scripts, one declared shape per request and response instead of three POST handlers that switch on a form field, and a machine-readable description a second consumer could be generated against. If that is what was meant, this plan delivers it, and 1.3's non-goal about a public API surface should be reopened, because a second consumer is the strongest argument the split has.

If something else was meant, the cheaper answers are real and 1.5 sets them out. Slow pages are not fixed by adding a JavaScript bundle. Painful deploys are not fixed by adding two Node toolchains to a Go repository. Painful feature work is largely fixed by splitting the three multiplexers and correcting the pagination order, without touching the rendering model at all.

**This plan proceeds on the development-scaling reading.** If that reading is wrong, stop here. See Q1.

### 1.1 What exists today

**What is being asked for is not a separation, it is a rewrite of the user interface.** The request described moving an existing TypeScript frontend off the Go backend. No such frontend exists to move. Every page is rendered by Go from `html/template`, so nothing is relocated: 33 templates, 16 scripts and every form post are deleted and written again as React components against 69 new JSON operations, and 28 of the 34 written e2e specs are rewritten with them. The backend keeps its database, session, CA and ACME code, but all 65 of its routes change shape. Ten phases of work, of which one is irreversible. Size the decision against that, not against the word "separate".

The Go side becomes a backend-for-frontend (BFF): it owns sessions, authorisation, step-ca access, the database and ACME orchestration, and it publishes a machine-readable OpenAPI 3.1 description of everything the browser is allowed to call. That description, and nothing else, is the contract. The frontend never hand-writes a request. It imports a TypeScript client package that the BFF's own CI generates from the spec, and its build fails if the package it resolves was not generated from the commit under test.

**One clause of the request could not be delivered as stated.** It asked both that "the frontend uses [the OpenAPI definition] to generate a client" and that it "consumes a package generated by BoF's ci". Those cannot both be literally true, since one has the frontend generating and the other has it consuming. This plan takes the second: generation happens once, in the BFF's CI, and the frontend consumes the result. D8 explains why that is the stronger form.

**A naming note that applies to every path in this document.** The Go directory is called `step-ui-go/` on disk today and is renamed to `backend/` by this plan (D11). Every path below, including the ones describing current state, uses the **post-rename** name. Where a path is quoted out of a file that has not been changed yet, the pre-rename name is what is actually in that file.

The evidence for the rewrite framing above:

- **33 templates** in `backend/templates/`, composed from `base.html` and `admin_base.html`.
- **65 chi routes** registered inline in `backend/main.go:222-325`, plus the `/static/*` handler at `main.go:337`. Almost all return HTML, a redirect, or a file download. The exceptions, which already return JSON, are `GET /api/status` (`h.APIStatus`), `GET /health` and `GET /ready` (`handlers/health.go:22-24`, `:45-58`).
- **16 plain-JavaScript files** in `backend/static/js/`, no build step, no modules, no types, doing progressive enhancement on rendered HTML.
- **Forms post `application/x-www-form-urlencoded`**, except the import upload (`templates/import.html:25`, `multipart/form-data`), and the server answers `302` plus a one-shot flash message stored in the session.
- **Three POST handlers are action multiplexers** switching on a form field: `UsersPost` has six cases (`handlers/users.go:36-161`), `ProfilePost` has three (`:204-260`), `ImportPost` has three (`handlers/certs.go:399-414`). Section 6 splits all three.
- The only TypeScript in the repo is the Playwright harness under `test/e2e/`, a separate Node project.

Everything else that matters:

- Sessions are `gorilla/sessions` cookie sessions (`main.go:191-200`), cookie name `step-ui`, `HttpOnly`, `SameSite=Lax`, 8 hour `MaxAge`, AES-encrypted with a key derived from `cfg.SecretKey`.
- `mw.RequireLogin` (`middleware/middleware.go:93-129`) reloads the user, checks `IsActive`, checks `session_epoch`, enforces the idle window and **slides `last_activity` on every request** (`:113,130`).
- CSRF is a random token minted by `h.csrf` (`handlers/handler.go:301`), held in the session, echoed in a hidden form field, checked by `h.csrfOK` (`:316`) with `subtle.ConstantTimeCompare` and `h.requireCSRF` (`:325`) on every mutating route.
- Authorisation is chi-group-scoped: `mw.RequireLogin` wraps the authenticated group, `mw.RequireRole("manager")` and `mw.RequireRole("admin")` wrap sub-groups (`main.go:238-325`).
- Login is two-step when TOTP is on: `POST /login` (`handlers/auth.go:48-128`) parks `pending_2fa_user_id` at `:117-124` and redirects to `/login`, whose `LoginGet` (`:32`) renders the code form. `LoginPost` checks the pending state **before** reading credentials (`:79`).
- CSP is already strict with no `unsafe-inline` (`middleware/middleware.go:56-61`). `Cache-Control: no-store` is global, and the static handler deliberately overrides it (`:62-65`, `main.go:97`).
- `models/models.go` has **no `json` tags on any type**. `models.User` carries `PasswordHash`, `TOTPSecret` and `TOTPPendingSecret`. `models.NotificationSettings` carries `SMTPPassword`. Today's protection is that templates name fields one at a time. That protection is being deleted, which is why R15 exists.
- There are **no per-object ownership checks anywhere**. `certFromURL` and `leCertFromURL` (`handlers/cert_ops.go:30,44`) fetch by id with no owner predicate, and the schema has no owner column. This is a single-tenant CA. Role-only authorisation is a faithful port, not a regression.
- **The project has no live deployments, operators, or existing volumes.** Stated in `plans/step-cli-to-ca-lib-swap.md` and still true. No backward compatibility to preserve, no staged rollout, no strangler pattern.

### 1.2 What changes

1. Every route the browser needs becomes a JSON operation under `/api/v1`, defined once as a Go type pair and registered with an OpenAPI-aware router layer.
2. The Go binary emits `openapi.json` deterministically, from a command needing no database, no CA and no environment.
3. BFF CI generates a typed TypeScript client from that spec and packs it as an npm tarball.
4. A new `frontend/` Vite + React application consumes that tarball. Its CI asserts the tarball was generated from the commit under test, so a spec change that breaks the frontend fails in the same run.
5. The SPA is served by its own nginx container, which is also the single public origin: it serves the static bundle and reverse-proxies `/api/`, `/auth/`, `/health`, `/ready` and `/openapi.json` to the Go container. Two deployables, one origin.
6. `templates/`, `static/js/` and the flash-message mechanism are deleted.
7. `step-ui-go/` is renamed to `backend/`, matching the new `frontend/`.

### 1.3 Non-goals

- Splitting into two repositories or two deployables. See D1.
- Changing the database schema, the `stepca` package, the ACME renewer, `tlsbootstrap.go` or `tlsreload.go`. **This constraint is what shapes D5's TLS decision**: the Go container keeps issuing and renewing its own leaf from step-ca exactly as today, so the `UI_TLS_MODE` bootstrap and the whole E2E-BOOT tier stay valid.
- Replacing cookie sessions with tokens. See D6.
- Redesigning the visual language. The existing CSS is ported as-is in the first pass.
- Multi-tenancy or per-object ownership. If that is ever added, every `{id}` operation needs an ownership predicate and this document is void.
- Serving the SPA from a genuinely different **origin** to the API. Two containers, yes (D1). Two origins, no: D6 and the whole CSRF design are conditional on one origin, and nginx path-routing is precisely what lets the deployment be split without splitting the origin. Adding a real second origin voids D6 and this document with it.
- Making the API a public, externally supported product surface. It is a BFF, shaped for this frontend. **See Q1: this non-goal is the one most likely to be wrong**, because an external consumer is the strongest argument the split has.

### 1.4 What it buys, and what gets worse

Every acceptance criterion in Section 2 is internal and mechanical. None of them says why the work is worth doing, so it is stated once here.

**What it buys.** A typed contract that a reviewer can diff and a compiler can enforce, replacing 33 hand-maintained templates and 16 untyped scripts. The three action multiplexers become eleven single-purpose operations, each with its own role marker and its own audit point. Authorisation stops being "it is inside the right chi group" and becomes an enumerable table a test asserts. A machine-readable description that the e2e suite and any future automation can be generated against. Deep-linkable table state. Per-item selection on certificate import, which falls out for free. Correct pagination, which is a live bug today.

**What gets worse.** First paint requires a JavaScript bundle where today it requires none. The strict CSP means toast, theming and anything else that would normally be a dependency is hand-rolled. 28 e2e specs are rewritten, and for eight phases the system carries two UIs and two authorisation implementations at once (R22). The ongoing tax, stated concretely rather than as "some overhead": two runtime images to build, scan, pin and patch on two CVE cadences; a third-party base image that must be minor-pinned and covered by dependabot; a Node build stage whose vulnerabilities no image scanner ever sees, guarded only by `npm audit`; three new npm ecosystems totalling several hundred transitive packages, which is the largest single ongoing cost in the plan; an nginx configuration that is now the security perimeter with no unit test framework of its own; and a cross-container certificate reload mechanism that does not exist today. 9.1's argument that two images is a net win is true for CI cache scoping and false for total ongoing work.

### 1.5 Alternatives to the whole approach

Section 3 compares ways to build the split. It does not compare the split against not splitting. Four options were considered and rejected, and they should be reconsidered if 1.0's reading of the motive is wrong.

**Keep server-side rendering and add htmx or Alpine.** Removes the entire contract pipeline, the client package, the two Node toolchains and Phases 1 to 3. Directly addresses "adding a page is painful" if that is what "not scalable" meant. Roughly two weeks against ten phases. **Rejected** because it delivers no machine-readable contract, which the request asked for explicitly, and leaves the untyped `static/js` layer in place.

**Hand-write an OpenAPI spec over the existing handlers and stop there.** Gives the machine-readable contract and a generated client for automation, and keeps the UI as it is. Roughly a tenth of the cost for half the value. **Rejected** because a spec no Go tool checks against handlers that were not built for it drifts immediately, which is D2's whole argument.

**Fix the concrete rough edges in place.** Split the three multiplexers, add `json` tags, fix the pagination total order, type the static JS. Every one of those is a real defect this document found, and none of them needs a rewrite. **Rejected as a complete answer** because it leaves no contract, but note that this plan does all four of them anyway, as side effects of Phases 4 to 8.

**Stop after Phase 4.** Section 8 argues the coexistence state is indefinitely stable, which makes a partial migration a legitimate end state rather than a failure. Phases 0 to 3 plus Phase 4 leave a proven contract, a typed client, one migrated domain and a working system. **This is the correct plan if the appetite is smaller than the work**, and it is preferable to compressing all ten phases.

---

## 2. Acceptance criteria

Every box below is either **mechanical** (a named command or test decides it) or explicitly marked as a judgement call with a named owner. A criterion nobody can check is worse than no criterion, because it manufactures the appearance of rigour.

**Contract**

- [ ] `backend/openapi/openapi.json` is committed and is OpenAPI 3.1. `git ls-files --error-unmatch` plus `jq -e '.openapi|startswith("3.1")'`.
- [ ] `go run ./cmd/openapi` regenerates it byte-identically, twice in a row, with `DATABASE_URL` and the CA URL pointed at `127.0.0.1:1` so any accidental dial fails loudly rather than succeeding on a runner that happens to have network.
- [ ] The spec is generated twice under two materially different environments (differing `UI_CERT_DURATION`, provisioner names, allowed-domain lists, `LE_*`) and the outputs are byte-compared. **This is the only enforcement D3 rule 1 has.** Without it the rule is a convention.
- [ ] The drift gate fails on any diff, proven once by a deliberate negative-test commit. A `yq` assertion confirms the gate and the client generation are steps of the same CI job.
- [ ] `apitypes/` imports only the standard library and other in-repo `apitypes` packages, and `api/` imports no `step-ui/models`. Enforced by `depguard` **allowlist** rules (not a denylist, which an aliasing package defeats), with a committed negative fixture the lint run must reject. Note `depguard` is not currently in `.golangci.yml`'s `enable` list and must be added.
- [ ] A test walks the spec and fails if any property name matches `(?i)(password|secret|token|hash|privateKey|certPath|keyPath|recovery|otpauth|dsn|databaseUrl|connectionString|args|env)` outside `openapi/secret-allowlist.txt`, and asserts every allowlisted property is `writeOnly: true` and absent from every response schema. The allowlist carries a CODEOWNERS entry.
- [ ] Zero `additionalProperties: true` schemas. `jq -e '[..|objects|select(.additionalProperties==true)]|length==0'`.
- [ ] Every operation has a `camelCase` `operationId`, a `summary`, and a `tags` entry from the fixed list (`session`, `certificates`, `acme`, `provisioners`, `security`, `admin`, `users`, `profile`, `system`, `dashboard`), asserted by `jq`. The golden table carries a `tag` column and the role-matrix test asserts the spec agrees with it, because tag assignment drives 5.9's invalidation map and an unreviewed tag silently breaks cache invalidation.
- [ ] `x-required-role` is present on every operation and matches the golden table for the same `(method, path)`. This is the only cross-check between runtime metadata, which never reaches the spec, and the document.
- [ ] `oasdiff breaking` fails the `client` job when a breaking change lands with no MINOR bump, proven once by a negative-test commit.

**Client package**

- [ ] The `client` job packs the tarball on every push and pull request, and a `yq` assertion confirms the provenance check is the first step of the `frontend` job after install.
- [ ] The package passes `publint` and `attw --pack`: ESM, `.d.ts`, zero runtime dependencies, with `@tanstack/react-query` and the fetch client as peers (7.4 is the authority).
- [ ] In the `frontend` job, the installed package's version contains the short sha of the commit under test. The in-image pack is exempt and uses D8's fallback version.
- [ ] `git ls-files clients/ts/src clients/ts/dist` returns nothing.
- [ ] Phase 2's two-commit consumption proof is committed as `docs/contract-proof.md` recording both commit shas and both CI run URLs, one red and one green. A `contract-negative` job re-runs that proof on `workflow_dispatch` and on any change to `ci.yml`, `clients/ts/**` or `cmd/openapi/**`, so the guarantee cannot decay into a one-off.
- [ ] The SPA issues no HTTP request except through the generated client. ESLint bans `fetch(`, `XMLHttpRequest`, `axios` and `EventSource` in `frontend/src/**` outside `src/api/client.ts`. **This is what makes the contract guarantee real rather than customary**, and it lands in Phase 3 before any domain phase writes a call site.
- [ ] A script fails the `frontend` job if the spec contains an operation whose generated SDK function is imported nowhere, unless its `operationId` is in `clients/ts/unused-allowlist.txt`. Dead API surface is a review decision, not an accident.

**Backend**

- [ ] **(Phase 9)** `chi.Walk` over the real router produces a route set byte-equal to the route golden file `testdata/routes.golden`. The only non-`/api/v1` patterns permitted are `/health`, `/ready`, `/auth/oidc/login`, `/auth/oidc/callback`, `/openapi.json`, `NotFound` and `MethodNotAllowed`. No `/assets/*` and no SPA fallback: nginx owns both (D5).
- [ ] **(Phases 3c to 8)** The same golden file additionally carries every still-registered template route and `/static/*`. Progress is measured by the role table's `templateRouteRetired` count (R22), never by shrinking this list, which would break Phase 3c's exit criterion.
- [ ] `rg` over `backend/` returns zero hits for `Handler.render`, `Handler.flash`, `Handler.base`, `Handler.tmpls`, `templateFuncs`, `html/template` and `ExecuteTemplate`. `templates/` and `static/js/` do not exist.
- [ ] Registering an operation with neither `role` nor `auth` metadata yields `403`, asserted by unit test.
- [ ] The role golden table records `(method, path, auth, role, tag, csrf, ratelimit, templateRouteRetired)`. A table-driven test replays **every** registered mutating operation with a valid session and no `X-CSRF-Token` and asserts `403` with `type` ending `/csrf`, with no exemptions. It replays every operation with `auth` absent and no cookie and asserts `401 application/problem+json` with no `Location`. And it asserts the `ratelimit` set is exactly `{createSession, submitMfa, requestPasswordReset, confirmPasswordReset}`.
- [ ] `gomodguard` denies `github.com/rs/cors` and `github.com/go-chi/cors`, `rg -n 'Access-Control-|CORS|cors\.' backend/` returns zero non-test lines, and a table-driven test replays every route in `routes.golden` with `Origin: https://evil.example` plus an `OPTIONS` preflight and asserts no `Access-Control-*` header on any response and 404 or 405 on every preflight. **This test is the CSRF design's load-bearing assumption** (D6) and says so in a comment.
- [ ] With `SESSION_SECURE=true`, every `Set-Cookie` is named `__Host-step-ui`, `__Host-step-ui-csrf` or `__Host-step-ui-oidc`, each with `Path=/`, `Secure` and no `Domain`. With it false, the bare names apply. Table-driven over login, MFA, logout and the OIDC round trip.
- [ ] After a `session_epoch` bump, and separately past `SessionMaxLifetime`, the old cookie gets `401 application/problem+json` on an `/api/v1` operation and `state: 'anonymous'` from `GET /api/v1/session`.
- [ ] Repeated `GET /api/v1/session` calls do not extend `last_activity`: a test calls it past the idle window and asserts the session still expires. Without this the idle timeout is decorative, which is precisely what 5.3 guards against. Every `/api/v1` response carries `X-Session-Expires-At`.
- [ ] `DELETE /api/v1/users/{id}` and `PATCH /api/v1/users/{id}` reject a target equal to the acting user, and profile operations accept no id. The failure mode is an installation with no admin.
- [ ] Every `writeOnly` property is absent from every response schema, a `PATCH` omitting a secret leaves it unchanged, and an explicit `null` in a `PATCH` body returns `422`. Table-driven over the settings singletons.
- [ ] `GET /api/v1/admin/console/commands` returns objects with exactly `id`, `label`, `description`, and the body contains no substring of `cfg.DatabaseURL`. The property-name test does not catch `Args`.
- [ ] Posting a wrong-typed `password` returns `422` whose body does not contain the submitted value. Table-driven over every operation with a `writeOnly` field.
- [ ] `GET /openapi.json` without a session returns `401`. `/docs`, `/openapi.yaml` and `/schemas/*` return `404`. D9's failure mode is huma silently re-registering them after a config regression.
- [ ] A 6 MiB body to a multipart operation returns `413` before any parser runs, and `MultipartMaxMemory` is 1 MiB.
- [ ] Aggregate DTO fields each carry a `source:"<operationId>"` struct tag. A test asserts every field has one, that the operation exists, and that the aggregate's role is at least the source's under `roleLevels`. **This is the only mechanical form D10's two rules can take**, and without it rule 2 is exactly what the role-matrix test structurally cannot catch.
- [ ] A list of 60 rows sharing one `created_at`, paged at 10, returns 60 distinct ids across 6 pages with no repeat and no omission, against `history`, `audit-events`, `security/events` and `acme/events`. `pageSize=201` returns `422`, absent defaults to 25, and `page * pageSize > 10000` returns `422`, with `maximum: 200` present in the spec and not only in code.
- [ ] An unmatched path under `/api/` or `/auth/` returns `404 application/problem+json`, including the 405 case.
- [ ] On a clean checkout with no Node run: `go build ./...`, `go vet ./...`, `golangci-lint run` and `go test -race ./...` all pass, and a `yq` assertion confirms `build-test-lint` contains no `setup-node` or `npm` step. Trivially true now that no Go package embeds a build artifact.
- [ ] `TRUST_PROXY=true` **and** `TRUSTED_PROXY_CIDRS` set to nginx's single `/32` are present in the base compose environment block and in `.env.example`, and `step-net` declares an explicit `ipam.config.subnet` with a static address for `step-ui-web`. Setting the first without the second is `log.Fatalf` (`main.go:140-147`), not a degradation.
- [ ] **Four forgery tests, not one.** Table-driven over `X-Forwarded-For`, `X-Real-IP`, `True-Client-IP` and `Forwarded`, against both an `/api/v1` operation and (during the migration window) `/login`, asserting the recorded address is the real peer in every case. A test that only forges `X-Forwarded-For` passes while the `realip.go:71-74` fallback is wide open, which is exactly how the CRITICAL finding survived an earlier draft.
- [ ] `middleware.forwardedHeaders` is **deleted**, and `rg -n 'X-Real-IP|True-Client-IP|forwardedHeaders' backend/middleware/` returns only the test asserting they are ignored. "Or gated behind an opt-in" is withdrawn: an opt-in is a config a future edit can set, which is the failure mode the criterion exists to remove.
- [ ] Two rate-limit tests, not one. A wrong password from one client address does not block a second. And a request carrying a forged `X-Forwarded-For` rate-limits on the real peer, which is what `proxy_set_header X-Forwarded-For $remote_addr` guarantees. **Getting either wrong locks out every user after five failures from anyone**, on a rolling five-minute window (`security.go:112`; the 15 minutes the login page claims comes from `BlockTime`, which is dead code).
- [ ] **(Phase 9)** No Go route serves `text/html` and no Go handler writes a `Content-Security-Policy` header.
- [ ] **(From Phase 3b)** No response under `/api/v1` carries a document CSP, and `mw.SecurityHeaders` emits `Strict-Transport-Security` on no path at all. Legacy template routes keep Go's document CSP and, deliberately, no HSTS, for the length of the migration window (R27).
- [ ] `go.mod` pins huma at `v2.30.0` or newer, asserted with `semver.Compare`.
- [ ] `scripts/coverage-gate.sh` fails both below the floor **and** when measured coverage exceeds the floor by more than 5 points, so the floor ratchets without anyone deciding to raise it.
- [ ] `step-ui-go/` does not exist and `rg --hidden -g '!.git' -g '!plans/**' 'step-ui-go'` returns nothing outside quoted pre-rename material.
- [ ] **(Phase 9)** `rg -n 'staticHandlerFromFS|mimeByExt|go:embed templates|go:embed static|fs\.Sub' backend/` returns nothing, and the backend Dockerfile contains no `COPY templates` or `COPY static` line.
- [ ] `rg -n 'go:embed' backend/` returns exactly one hit after Phase 9, `openapi/openapi.json`, and a `yq` assertion confirms `build-test-lint` declares no `setup-node` and no `npm` step. The property is that the Go build never depends on a Node-produced artefact.

**Frontend**

- [ ] `npm ci && npm run build` emits content-hashed filenames for every JS, CSS and font asset.
- [ ] A test computes the sha256 of every inline `<script>` and `<style>` in the built `index.html` and asserts each appears in the CSP the Go server emits, failing on any unhashed inline block. This is what lets D12.3's theme bootstrap exist without `unsafe-inline`. `rg 'eval\(|new Function\(' dist` returns nothing.
- [ ] A CSP smoke spec loads every route against the compose stack with a console collector and fails on any Content Security Policy message. It is registered as a new ID in `plans/e2e-tests.md`'s appendix rather than borrowing an existing one.
- [ ] `npm run lint`, `typecheck` and `test` run in the `frontend` job.
- [ ] Every route in `navigation.ts` and the router table renders against a seeded stack with a 200 shell, its `data-testid="page-<name>"` present, and zero console errors. Table-driven from the committed route list, so a route with no smoke coverage fails.
- [ ] **Judgement, owner: the repository owner.** Each phase PR carries a `Parity` table, one row per template it replaces, listing the fields rendered, the actions offered, and either the covering e2e test ID or a one-line deliberate-deviation note. Equivalence is not the goal: `import/scan` is removed, `GET /logout` is removed, `tab` is dropped, `auto` theme now resolves client-side, and OIDC operations exist and 404 when disabled.
- [ ] Deep-link refresh and refresh-mid-MFA are covered by named Playwright specs, table-driven over the route list.
- [ ] Session expiry (driven by a `psql` epoch bump through the existing `compose.ts` helper) redirects to login, and a wrong password renders a field error rather than a page reset.
- [ ] A global Playwright fixture collects `pageerror` and `unhandledrejection` across **every** spec and fails any spec producing either. Scoped to one flow it was unverifiable. Global it is trivial and far more valuable.
- [ ] A unit test drives the response interceptor with a `401` for each auth endpoint and asserts no cache clear and no redirect, and with a `401` for one other operation and asserts both. Driven off `isAuthEndpoint`'s own list so the list and the test cannot diverge.

**Integration**

- [ ] Every `location` in `nginx -T` output containing `proxy_pass` also contains `include conf.d/proxy-headers.conf`, asserted against the dumped config rather than the source file (D5 rule 2). The behavioural backstop is that the forgery table runs against every proxied prefix in the shared list, legacy prefixes included.
- [ ] With `UI_TLS_MODE=stepca` and step-ca unreachable, the backend self-signs, writes the `.fallback` marker, `/ready` returns non-200 naming it, `step-ui` reports unhealthy and `step-ui-web` never starts. Bringing step-ca up then clears the marker within one interval with no restart, which is the only check on the lazy per-iteration CA client.
- [ ] `rg -n 'os\.WriteFile' backend/tlsbootstrap.go` returns nothing for certificate or key writes, and a unit test asserts a `.tmp` sibling plus `os.Rename`. A second asserts the reload watcher runs `nginx -t` before `nginx -s reload` and re-arms after failure rather than exiting.
- [ ] `/ready` reports `notAfter` for **both** leaves and goes non-200 when either is more than one interval inside its renewal window, so a repeatedly failing web-leaf renewal is loud before it is fatal (R32).
- [ ] Go reads inbound `X-Request-Id`, mints one when absent, uses it as the log correlation id and echoes it on every `/api/v1` response. A test issues one request with a known id and asserts it appears in nginx's access log, the backend's structured log and the response. Without a consumer the header and the log format are decorative.
- [ ] A test parses `proxy_read_timeout` from `nginx.conf` and asserts it exceeds every backend operation-timeout constant, at minimum `handlers/backup.go`'s `pg_dump` context, which is exported as a named constant for the purpose. A new long operation raising its own ceiling then fails this test rather than producing an unattributed 504.
- [ ] **No compose file publishes a host port for `step-ui`.** `yq` over `docker-compose.yml` and every override asserts `ports` is absent on that service, and `make preview` fails if present. "nginx is the one public origin", which D1 says the document rests on, is otherwise a deployment fact no code enforces (R33).
- [ ] `collect.sh`'s service list contains `step-ui-web`, asserted in CI, so the new origin is not the one component with no diagnostics on failure.
- [ ] **(Phase 9)** `rg -n '/app/' frontend/ backend/ test/e2e/ docker-compose.yml compose.e2e-*.yml .env.example` returns nothing and a cold deep link to `/certificates/1` returns the shell. During the window the same command returns hits only inside blocks carrying the `PHASE9-DELETE` marker.
- [ ] `docker compose up` from a fresh clone with empty volumes yields a working UI on four containers, with the SPA and the client package both built inside the frontend image.
- [ ] Neither runtime image contains Node: `docker run --rm --entrypoint sh <img> -c '! command -v node && ! command -v npm'` succeeds for both. **After Phase 9** the backend image additionally contains no `.html`, `.css` or `.js` file. It does contain the committed `openapi.json`, which is data rather than a served asset.
- [ ] nginx serves `/assets/*` with `immutable`, serves `index.html` with `no-store`, falls back to `index.html` for an unknown SPA path, and does **not** fall back for `/api/`, `/auth/`, `/health`, `/ready` or `/openapi.json`, which reach the backend and return its own status. Table-driven against the running stack.
- [ ] nginx emits no `Access-Control-Allow-*` header on any path, and a request with `Origin: https://evil.example` to the public port is answered without one. The CORS ban is enforced at two layers and tested at both (D6).
- [ ] `nginx -T` inside the running container contains `proxy_ssl_verify on`, `proxy_ssl_verify_depth 2`, `proxy_ssl_name step-ui` and a trusted certificate path **on `step-ui-web-ssl`**, and a backend presenting a leaf from an untrusted root makes every `/api/v1` path answer with the `@apidown` 503. The depth is asserted as configuration text: proving `2` is the right number needs a manufactured chain of a third length and buys nothing.
- [ ] `docker inspect step-ui-web` shows `step-ui-web-ssl:ro` as its only certificate-bearing mount. `step-ui-ssl`, which holds the backend's `server.key`, and `step-ca-data` are not mounted, and reading `server.key` from inside the nginx container fails. This is D5 rule 5's key-separation argument made mechanical, with `proxy_ssl_name step-ui`, and an untrusted upstream certificate 502s rather than silently downgrading. The compose default `UI_TLS_MODE` is `stepca`, since a self-signed backend leaf cannot satisfy this.
- [ ] nginx serves its **own** leaf (`web.crt`) carrying the public hostname, and the backend leaf carries `step-ui`. One certificate cannot satisfy both roles, and sharing one private key across both containers is rejected on its own merits (D5 rule 4).
- [ ] `docker compose restart step-ui` and the next request through nginx succeeds with no action on `step-ui-web`, which is the `resolver` plus variable `proxy_pass` behaviour (D5 rule 5).
- [ ] A 6 MiB upload returns `413` with `application/problem+json` from Go, not nginx's HTML 413, which requires `client_max_body_size` above the Go ceiling.
- [ ] No `/api/v1` response carries a `Strict-Transport-Security` header or a document-shaped CSP, and no response discloses a `Server` version. (`server_tokens off` removes the version, not the header, and stock `nginx:alpine` cannot remove the header.)
- [ ] With `step-ui` stopped, every `/api/v1` path through nginx returns `application/problem+json` with `type` ending `/upstream-unavailable`, and the SPA renders a named unavailable state rather than a login form or a parse failure.
- [ ] A Vitest unit test renders the shell with `__CONTRACT_SHA__` differing from a stubbed `GET /api/v1/config` and asserts the skew banner is present and non-blocking, absent when they match, and absent when `__CONTRACT_SHA__` is empty. A `compose.e2e-skew.yml` builds the frontend image with `--build-arg CONTRACT_SHA=0000…` and asserts the same against a real stack, which requires 5.10's build arg to exist.
- [ ] Both application services declare `image:` alongside `build:` and default to the **same** `${IMAGE_TAG}`, with two explicit per-half override variables for rollback. One shared default makes an accidental partial upgrade impossible; the two overrides make a deliberate one possible. These are the same variable used two ways and must not become two independent defaults.
- [ ] A backup create against a seeded stack returns `200` through nginx rather than a 504, which requires `proxy_read_timeout` above `handlers/backup.go:179`'s two-minute `pg_dump` ceiling.
- [ ] With `WEB_TLS_MODE=provided` and neither file present, **`step-ui-web` exits non-zero within five seconds** with a message naming both missing paths and never reaches healthy. It does not poll: D5 rule 9's wait-and-serve-503 applies to `stepca` mode, where the file is expected to arrive, and must not apply to `provided`, where it never will.
- [ ] Every file under `dist/assets/` matches a content-hash pattern, so the `immutable` policy cannot be applied to a file that can change.
- [ ] `rg -n '"/api/v1'` over `backend/` returns exactly one non-test hit (5.1), and `jq -e '[.paths|keys[]|select(startswith("/")|not)]|length==0'` passes, since a path without a leading slash resolves relative to the current route under `baseUrl: ''` and lands on the SPA shell with `200`.
- [ ] `make nginx-lint` starts a **prebuilt fixture image** (nginx plus the real config and a committed placeholder `dist/`, no Node stage) against a stub upstream and a throwaway self-signed leaf, with no postgres, no step-ca and no compose project, and asserts the SPA fallback, the proxied prefixes, the header set, the cache directives and the `@apidown` document. It depends on no target that builds the real image, so it is fast by construction. Fail above 60 seconds if a budget is wanted: a fifteen-second assertion on a shared runner is a flaky gate that gets deleted, taking the rung with it. Without a fast rung, every `nginx.conf` edit costs a full stack bring-up and the config stops being tested.
- [ ] The compose healthcheck on `step-ui` is `curl -fsk https://localhost:8443/health`. It currently curls `/login` without `-f`, so it both passes on any status and points at a route this plan deletes.
- [ ] The proxied path prefix list in `frontend/nginx.conf` and in `frontend/vite.config.ts` derive from one source, asserted by a test (4.4).
- [ ] After the backend renews its leaf, nginx serves the new certificate without a container restart (D5, R13).
- [ ] `vite.config.ts` declares proxies for `/api`, `/auth`, `/health`, `/ready` and `/openapi.json` with `secure: false` and declares no `server.cors` key. Hot reload is verified by hand in the Phase 3 PR and is not a CI gate.
- [ ] The e2e suite meets the pass level stated in Section 10, and `e2e-gate` is a required check.

## 3. Decisions and alternatives

### D1. One repository, two build units, two containers, one origin

**Decision, set by the repository owner.** Keep everything in `andremmfaria/step-ca-ui`. Add `frontend/` at the repository root, beside `backend/` and `test/e2e/`. The directory names are fixed and not up for revision. Ship **two images**: `backend`, a Go binary with no static assets, and `frontend`, an nginx image containing the built SPA.

**The single most important consequence, and the thing that makes the rest of this document survive.** Two containers is a *deployment* split, not an *origin* split. **nginx is the one public origin.** It serves `/` and `/assets/*` from the bundle it carries, and reverse-proxies `/api/`, `/auth/`, `/health`, `/ready` and `/openapi.json` to the Go container. The browser therefore sees one origin for both the document and the API, exactly as it did when the Go binary served both. That is what keeps `SameSite=Strict` viable, keeps CORS out of the design entirely, keeps the `__Host-` cookie prefixes legal, and keeps D6's session-bound CSRF working without a single change.

**Rejected: two genuinely separate origins with CORS.** This is what "two containers" naively becomes if nginx only serves static files and the browser talks to the API on a different host or port. It costs `SameSite=None` on the session cookie, a CORS allowlist, a preflight on every mutation, and the loss of the custom-header CSRF defence, which is only a defence while a cross-origin page cannot get a preflight approved. There is no benefit here to pay for that. Path-routing through nginx gives the operational separation that was asked for at none of the security cost.

**Rejected: two repositories.** The contract is the coupling and it changes on nearly every feature. Two repositories turn a one-PR change into a three-PR dance with a broken intermediate state at every step. **The strongest case for them, stated fairly**, is separate ownership, separate release cadence, and frontend contributors who never install Go. That case is real and loses here only because there is one team and one consumer. It wins the moment either changes.

**What rests on this decision.** D5 (two images, nginx as origin and TLS terminator, the asset and fallback behaviour), D6 (no CORS, `SameSite=Strict`, `__Host-` prefixes), D8 (a path-resolved tarball rather than a registry version), 4.4's dev loop, 5.7's blob downloads and Section 2's CORS test. A reader who moves the API to a second origin is reopening all of them.

**What the artifact mechanism does not give you.** The request asked for a package generated by the BFF's CI. This plan produces that package on every push and pull request and consumes it from the same run, which is stronger provenance than a registry lookup, and publishes to GitHub Packages only on a `v*` tag. Two things are given up. There is no version of the client any consumer outside this repository can install, so adding one is a new architectural decision rather than an `npm install`. And local development consumes nothing from CI at all: `make client` builds its own, so the provenance assertion D8 calls the point of the whole design exists only inside GitHub Actions. Both are correct while this repository is the only consumer, and both become wrong the moment it is not. That is Q1, not a question about publication cadence.

**The strongest case for two repositories, stated fairly.** Separate ownership, separate release cadence, and frontend contributors who never install Go. That case is real and it loses here only because there is one team and one consumer. It wins the moment either changes.

### D2. Code-first OpenAPI with `huma` v2

**Decision.** Adopt `github.com/danielgtaylor/huma/v2` with the `humachi` adapter over the existing chi router. Operations are `huma.Register(api, huma.Operation{...}, func(ctx, *In) (*Out, error))` with Go structs for input and output. Huma derives OpenAPI 3.1 from those structs, validates requests against the derived schema before the handler runs, and returns RFC 9457 `application/problem+json` on failure.

**Why.** The handlers already exist as Go code with Go types. Keeping the type as the single source of truth means a field that gains a `json` tag cannot silently fail to appear in the spec. It also removes request binding, required-field checks, enum checks and the error envelope as hand-written code.

**Rejected: spec-first with `oapi-codegen`.** Hand-writing a document for 60-plus operations gives a reviewable YAML diff and a compile-time guarantee that handlers match. It also makes the source of truth for a Go service a YAML file no Go tool checks, writes every field twice, and maintains a schema language alongside the language being worked in. The reviewable-diff benefit is recovered free by D3.

**Rejected: `swaggo/swag`.** Comment annotations are a third representation that drifts from both the types and the runtime, and stable OpenAPI 3.1 support does not exist.

**Cost to accept.** Four things, all real.

1. **Huma owns the request lifecycle for the operations it registers.** Existing chi middleware still runs, but per-operation concerns move from `r.Group(...)` into huma middleware. Section 5.5 fixes the mechanism.
2. **A huma operation handler has the signature `func(ctx context.Context, in *In) (*Out, error)`.** No `http.ResponseWriter`, no `*http.Request`. `gorilla/sessions` needs both, so every session write in this codebase (`completeLogin` at `handlers/auth.go:192`, `Handler.sess` at `handlers/handler.go:260`, `Handler.csrf` at `:301`, `Handler.Logout` at `handlers/auth.go:227`, the MFA park at `:117-124`, `mw.RequireLogin`'s slide at `middleware/middleware.go:113,130`) is unreachable from a handler as written. The fix is one middleware that unwraps the pair onto the context. See 5.5.
3. **Huma is pinned at `v2.30.0` or newer**, because `humachi.Unwrap` does not exist before it. Current release is `v2.39.1`. Without `Unwrap`, point 2 degrades to hand-encoding `securecookie` values and abandoning `store.Save`, which is bad enough that `oapi-codegen` becomes the better answer.
4. **Huma auto-registers routes on the adapter, below `api.UseMiddleware`.** `/openapi.json`, `/openapi.yaml` and `/docs` from `DefaultConfig` never see the authorisation middleware. That is not merely an exposure question, it is a demonstration that the middleware is not a perimeter, and it is why D9 turns those routes off.

**The price of the Phase 0 off-ramp, so it is a real option and not a gesture.** If the spike rejects huma and the plan switches to `oapi-codegen`, five things change and the reader should know that before Phase 0 rather than during it. The drift gate reverses direction, from "the spec must match the Go" to "the Go must match the spec", which is a different tool and a different failure mode. All of 5.5 is rewritten: `roleOp`, `Metadata["role"]`, `x-required-role` and the in-process role-matrix test are huma constructs, and roles would live in the document with a differently-shaped runtime check. 5.2's specifics go with it, since 422-not-400, the problem-document default and the auto-registered error responses are all "what huma emits". R12 and the entire unwrap middleware evaporate, because `oapi-codegen` hands the handler `(r, w)`, which removes the plan's self-declared highest-risk mechanic. And Section 2 loses the huma pin and the `CreateHooks` criterion while D9 loses its rationale entirely.

### D3. The spec is committed, and CI gates the diff

**Decision.** `backend/openapi/openapi.json` is a committed, generated file. A `cmd/openapi` binary writes it. CI regenerates and fails on any difference, in the same job that then generates the client.

**Why.** This recovers the one real advantage of spec-first. Every API change shows up as a reviewable diff next to the Go change that caused it, and an unintended breaking change (a field going required, an enum losing a value, an operation disappearing, **a secret field appearing**) is visible to a human reviewer rather than discovered later.

**Requirement.** Generation must be deterministic and side-effect free. That is a small job. Every constructor was traced: `handlers.NewWithFS` (`handlers/handler.go:70-77`) calls `initOIDC` only when `cfg.OIDCEnabled`, so the `log.Fatalf` at `:85` is unreachable with a zero `config.Config`. `loadTemplates` degrades to `slog.Error` plus `continue` (`:166`, `:187`). `caClient()` (`:103`) is lazy. The `*sql.DB` is never dereferenced during registration. Huma derives schemas by reflection over static types. What is required is a **split, not an abstraction**: separate `api.Register(huma.API, *handlers.Handler)` from construction, add a roughly ten-line `handlers.NewForSpec()`, and have `cmd/openapi` call `humachi.New(chi.NewRouter(), cfg)` then `api.Register` then marshal.

**Three rules that keep the spec stable.**

1. **No configuration may reach a schema.** Provisioner names, allowed domain suffixes, duration bounds and template lists are response **data**, never schema `enum`, `maximum` or `default`. Break this and the spec becomes environment-dependent and the drift gate fails per machine. `GET /api/v1/certificates/options` is where it would happen.
2. **`apitypes/` references only stdlib and in-repo types.** No third-party type appears in a DTO, so no dependency bump other than huma's can move the spec.
3. **Response DTOs are hand-written field by field.** Embedding or aliasing a `models` type is forbidden even where the fields happen to be safe today, because the next field added to the model is the leak. See R15.

### D4. TypeScript client generated with `@hey-api/openapi-ts`

**Decision.** Generate with `@hey-api/openapi-ts` (current release `0.99.0`), configured with the fetch client plugin and the TanStack Query plugin. Output is one package: typed operation functions, request and response types, and query and mutation options per operation.

**Why.** It reads OpenAPI 3.1, which huma emits. It generates both typed operations and query hooks, so the frontend gets caching, deduplication and invalidation without hand-writing a hook per operation.

**Rejected: `openapi-typescript` plus `openapi-fetch`.** Smaller and more stable, but types only, so every call site writes the path and method as literals and there is no generated query layer. This is the escape hatch if D4 goes wrong.

**Rejected: `orval`.** Comparable. `@hey-api/openapi-ts` has the simpler configuration.

**Rejected: `openapi-generator` (`typescript-fetch`).** A Java toolchain in a Node build, verbose output, partial 3.1 support.

**Cost to accept.** `@hey-api/openapi-ts` is pre-1.0 and its output shape has changed between minor versions. Pin it exactly. Treat a generator bump as its own pull request with the generated diff reviewed. See R2.

### D5. Two images, nginx as the origin and the TLS terminator

**Decision.** The SPA is built to static files and baked into an `nginx:alpine` image. The Go image serves no static assets once Phase 9 lands. This is the one place where the two-container answer makes the plan **smaller**: `//go:embed static`, `staticHandlerFromFS`, the hand-written traversal check and the hand-written MIME table all leave the Go side.

Everything below was verified against the repository. Nine things that looked obvious turned out to be wrong, so they are stated as numbered rules rather than as advice, and the config is given as the artefact rather than as prose.

```nginx
# frontend/nginx.conf — REPLACES /etc/nginx/conf.d/default.conf, which must be
# deleted in the image or the stock port-80 server survives inside the network.

server_tokens off;
log_format origin '$remote_addr $request_id "$request_method $uri" $status '
                  'ua=$upstream_status urt=$upstream_response_time';   # $uri, never $request
access_log /dev/stdout origin;

client_header_timeout 10s;  client_body_timeout 15s;  send_timeout 15s;
keepalive_timeout 30s;      proxy_connect_timeout 3s;  proxy_read_timeout 180s;
limit_conn_zone $binary_remote_addr zone=perip:10m;
limit_req_zone  $binary_remote_addr zone=api:10m rate=20r/s;
resolver 127.0.0.11 valid=10s ipv6=off timeout=2s;

server {
  listen 8443 ssl;  http2 on;
  server_name ${WEB_HOSTNAME};
  ssl_certificate     /etc/step-ui/ssl/web.crt;
  ssl_certificate_key /etc/step-ui/ssl/web.key;
  ssl_protocols TLSv1.2 TLSv1.3;
  gzip on;
  client_max_body_size 1m;
  limit_conn perip 32;  limit_req zone=api burst=40 nodelay;

  location = /nginx-health { access_log off; return 200; }
  location ~ ^/(docs|openapi\.yaml|schemas)(/|$) { return 404; }

  location /assets/ {
    include conf.d/doc-headers.conf;
    add_header Cache-Control "public, max-age=31536000, immutable" always;
    try_files $uri =404;
  }

  location / {
    include conf.d/doc-headers.conf;          # CSP, XFO, nosniff, Referrer-Policy, HSTS
    add_header Cache-Control "no-store" always;
    try_files $uri /index.html;
  }

  location ~ ^/(api|auth)/|^/(health|ready|openapi\.json)$ {
    include conf.d/proxy-headers.conf;        # MUST be included in EVERY proxying block
    add_header X-Content-Type-Options "nosniff" always;   # opts this block OUT of inheritance
    client_max_body_size 8m;                  # above 5.7's 5 MiB Go ceiling, so Go owns the 413
    proxy_buffering off;
    set $backend "step-ui:8443";
    proxy_pass https://$backend$request_uri;  # $request_uri mandatory with a variable
    proxy_ssl_trusted_certificate /etc/step-ui/ssl/root_ca.crt;
    proxy_ssl_verify on;  proxy_ssl_verify_depth 2;
    proxy_ssl_name step-ui;  proxy_ssl_server_name on;
    proxy_ssl_protocols TLSv1.2 TLSv1.3;
    proxy_intercept_errors on;
    error_page 502 503 504 = @apidown;
  }

  location @apidown {
    default_type application/problem+json;
    add_header Cache-Control "no-store" always;
    return 503 '{"type":"https://step-ca-ui/errors/upstream-unavailable",'
               '"title":"Service Unavailable","status":503}';
  }
}
server { listen 8443 ssl default_server; ssl_reject_handshake on; return 444; }
```

`conf.d/proxy-headers.conf`, included by **every** location that proxies, legacy blocks included:

```nginx
proxy_set_header Host             $host;
proxy_set_header X-Forwarded-For  $remote_addr;   # replace, never $proxy_add_x_forwarded_for
proxy_set_header X-Real-IP        $remote_addr;   # MUST be set. see rule 1.
proxy_set_header True-Client-IP   "";             # MUST be cleared. see rule 1.
proxy_set_header Forwarded        "";
proxy_set_header X-Request-Id     $request_id;
```

**Rule 1: setting `X-Forwarded-For` is not enough, and an earlier draft's claim that it made a client-supplied header "unsurvivable" was wrong.** `clientFromHeaders` (`middleware/realip.go:55-77`) walks XFF right to left, skips every entry that is itself trusted, and **falls through to `X-Real-IP` then `True-Client-IP`** when XFF yields nothing (`realip.go:12`, `:71-74`). With the replace form, XFF contains exactly one entry, nginx's own address, which is trusted, so the loop always exhausts and **the fallback fires on every single request**. An unauthenticated caller sending `X-Real-IP: 203.0.113.9` therefore controls the login rate limiter, `auth_log.ip`, `users.last_ip` and the security log: unlimited password guessing by rotating a header, and the ability to block a colleague by forging theirs. This is the single most serious defect the review found. The fix is both halves: nginx sets `X-Real-IP` and clears `True-Client-IP` and `Forwarded`, **and** Go's `forwardedHeaders` fallback (`realip.go:12`) is deleted or put behind an explicit opt-in, because one is a config file a future edit can drop and the other is compiled in.

**Rule 2: `proxy_set_header` inherits exactly like `add_header`, and the legacy blocks are where it bites.** Directives at one level are inherited only when that level defines none of its own. Phase 3's legacy passthrough blocks either define a subset and silently lose the rest, or define none and inherit from a server level that defines none. Either way `POST /login`, the one route the rate limiter protects during Phases 3 to 8, would take the client's raw headers. Hence the `include` file above, in every proxying block, with no exceptions.

**Rule 3: the trusted list is nginx's single address, and the network needs a pinned subnet to have one.** `docker-compose.yml:151-156` declares `step-net` with no `ipam` block, so Docker allocates dynamically and no hardcoded CIDR survives a `docker network rm`. Add `ipam.config.subnet` (as an env-var default, since `172.16/12` is where corporate VPNs live), give both application services static addresses, and set `TRUSTED_PROXY_CIDRS` to nginx's single `/32`. Trusting the subnet is wrong: the bridge gateway sits inside it. **Adding an upstream hop later is a change to nginx, not to this list**: the mechanism is nginx's realip module (`set_real_ip_from`, `real_ip_header`, `real_ip_recursive on`), after which `$remote_addr` is the true client and the replace form stays correct. Widening `TRUSTED_PROXY_CIDRS` instead collapses the rate limiter to one bucket for the whole internet.

**Rule 4: `TRUST_PROXY=true` without `TRUSTED_PROXY_CIDRS` is a crash, not a degradation.** `main.go:140-147` `log.Fatalf`s, and `ParseTrustedProxies` errors on an empty list (`realip.go:32-34`). Neither key reaches the container today (`docker-compose.yml:76-109`), so setting them in `.env` alone is a silent no-op. Both go in the compose environment block and `.env.example`. Note `compose.e2e-oidc.yml:62-63` currently defaults `TRUST_PROXY` to false and would silently flip it off for that scenario.

**Rule 5: nginx gets its own certificate, governed by its own mode key.** Four independent reasons a shared one cannot work. The root CA is not in `step-ui-ssl` (it is at `/home/step/certs/root_ca.crt` on `step-ca-data`, `config/config.go:93`). The compose default `UI_TLS_MODE` is `self-signed` (`docker-compose.yml:95`), which chains to nothing and 502s every request under `proxy_ssl_verify on`. One certificate cannot carry both the public browser name and `step-ui`, because `issueUICert` requests exactly one SAN (`stepca/issue.go:58,69-70`) and `IssueRequest` has no SAN list. And sharing the file shares a private key between the Go binary and a C web server with a far larger attack surface.

So: **`UI_TLS_MODE` keeps its current three-way meaning for the backend leaf** (SAN `step-ui`, internal hop only), unchanged, so E2E-BOOT-01 to -09 stay valid. A new **`WEB_TLS_MODE`** with values `stepca` (default) and `provided` governs `web.crt`/`web.key`. Collapsing both under one key produces two unworkable states: `provided` writes no web leaf and nginx cannot start (`tlsbootstrap.go:208-209` is a bare `return nil`), and `self-signed` writes a backend leaf `proxy_ssl_verify` rejects. `WEB_HOSTNAME` has **no hostname fallback** and an empty value is `log.Fatalf`, because falling through to `os.Hostname()` writes a `web.crt` whose only SAN is a container id, which survives on the volume and is rejected by every browser on the next `make up` with no clue where it came from.

The Go container also **copies `cfg.RootCert` into `/opt/step-ui/ssl/root_ca.crt`** at the end of the bootstrap, so nginx mounts exactly one volume, `step-ui-ssl:ro`, and never gets read access to the CA's `/home/step`. That also keeps `compose.e2e-fingerprint.yml` working unchanged. **A separate `step-ui-web-ssl` volume carries `web.crt` and `web.key`**, read-write to Go and read-only to nginx, so an RCE in nginx does not yield `server.key`, the credential for impersonating the backend.

**Rule 6: the self-signed fallback and `proxy_ssl_verify on` are incompatible, and the stack must say so rather than 502 in silence.** `ensureUICert` falls back to self-signed after 30 retries, on context cancel and on a nil CA client (`tlsbootstrap.go:215-245`). That fallback exists to keep the UI up when the CA is down; under this design it guarantees the opposite, with both healthchecks green and every API call 502ing. Three changes: `ensureUICert` writes a `.fallback` marker whenever it self-signs in `stepca` mode, `/ready` reports it and the compose healthcheck treats it as unhealthy so `depends_on` holds nginx off, and `startUICertRenewer` runs whenever the mode is `stepca`, constructing its CA client lazily per iteration rather than requiring a non-nil client at boot (`main.go:436`), so a CA that comes up late is recovered from without a restart. For the **web** leaf specifically the fallback is a hard failure rather than a self-signed cert, because nothing downstream can work and a fast crash-loop is diagnosable where a silent 502 wall is not.

**Rule 7: `proxy_pass` must resolve at request time.** With a literal hostname nginx resolves once at configuration load. `step-ui` carries `restart: unless-stopped` and a recreated container gets a new address, after which every proxied request 502s until nginx itself restarts. `depends_on: service_healthy` is a start-order gate and does nothing here. The `resolver` plus `set $backend` plus `$request_uri` form above fixes it. **The trade being made, recorded rather than left silent:** a variable `proxy_pass` cannot use a named `upstream` block, so there is no upstream keepalive and every proxied request costs a fresh TCP connect and TLS handshake to Go. At this scale that is acceptable, and it has one useful side effect noted in R29.

**Rule 8: `add_header` needs `always`, any `add_header` in a block drops every inherited header, and the proxy block therefore defines one deliberately.** Without `always` a header is emitted only on 2xx and 3xx, so a CSP set in `location /` is absent from every 4xx and 5xx. And because inheritance is all-or-nothing per level, the proxy block currently inherits nothing only because it defines nothing: the day someone adds a single server-level `add_header`, the document CSP and HSTS get stamped onto every API response and browsers enforce the intersection with Go's `default-src 'none'`. The `X-Content-Type-Options` line in the proxy block exists to opt out of that, and says so in a comment.

**Rule 9: nginx waits for its certificate instead of dying on it, and the reload lives in its own container.** `ssl_certificate` is fatal at config load, and `depends_on` does not survive a host reboot, where the daemon restarts containers in no guaranteed order. `frontend/entrypoint.sh` polls for `web.crt` and `web.key` for up to 120 seconds, logging the paths, then starts nginx against a bundled ephemeral certificate serving a fixed 503 naming the missing file. The same entrypoint runs the reload watcher: sha256 the certificate every 60 seconds, and on change `nginx -t && nginx -s reload`, logging both outcomes and re-arming on failure. **It cannot live in the Go container**: `nginx -s reload` signals a process in another container, so a Go-side watcher would need the Docker socket, which is host root held by the container that runs an allowlisted shell console. That is a prohibition, not a preference. Separately, `tlsbootstrap.go`'s two `os.WriteFile` calls become write-to-`.tmp`-then-`os.Rename`, which removes the torn-PEM race at the source for both readers.

**Rule 10: three absolute URLs must name nginx's origin, and one of them is not the variable an earlier draft named.** `OIDC_REDIRECT_URL` (`config/config.go:116`, used at `handlers/handler.go:90`) is what the OIDC flow registers, **not** `PUBLIC_BASE_URL`, which `resetLink` uses for password-reset emails (`handlers/password_reset.go:112,260-271`) and which refuses to send when unset. Both, plus `WEB_HOSTNAME`, must name the published origin, and all three go in the compose environment block and `.env.example`. Note the emailed reset link targets `/reset-password?token=...`, which is now an SPA route resolved by `try_files`, so an e2e must assert the path `resetLink` emits actually resolves.

**Rule 11: two long operations exceed nginx's default timeouts.** `writePGDump` runs under a two-minute context (`handlers/backup.go:179`) and `buildBackupBundle` tars three directories unbounded, against a default `proxy_read_timeout` of 60s. The invariant, which any new long operation moves: **`proxy_read_timeout` exceeds the longest backend operation.** 180s covers today. `proxy_buffering off` on the proxy block stops nginx spooling a whole backup bundle to disk before the first byte reaches the browser. `/admin/integrity`, whose cost Phase 6 measures, joins this class if it walks every certificate.

**Who owns the security headers.** nginx owns the document headers because Go serves no document. Three specific corrections rather than a principle. **Go stops emitting `Strict-Transport-Security` entirely**: it emits `max-age=0` when `EnableHSTS` is false (`middleware/middleware.go:47`), the compose default, and on one origin that API response **clears** the policy nginx set on the document. nginx's HSTS stays env-gated through an `/etc/nginx/templates/` substitution rather than being hardcoded, because a two-year `max-age` on a name whose certificate chains to a private root is a trap with no downgrade path. **Go's CSP is scoped off `/api/v1`.** And `server_tokens off` removes the version but not the `Server` header, which stock `nginx:alpine` cannot do without a third-party module, so the acceptance criterion says "no version disclosed" rather than "no header".

What Go keeps on JSON responses is the subset that means anything there: `nosniff`, `Cache-Control: no-store`, `X-Frame-Options`, `Referrer-Policy`, and a minimal `default-src 'none'; frame-ancestors 'none'`. `form-action`, `base-uri`, `style-src`, `font-src` and `img-src` are inert on `application/json`.

**A file-permission dependency worth writing down.** The cert files are `0600` owned by uid 10001. `nginx:alpine` works only because its master runs as root and reads them before dropping to the worker user. `nginxinc/nginx-unprivileged`, or a `USER` line added during hardening, breaks TLS startup silently.

**Two images.** `openapi.json` is committed, so the frontend image reads it from the build context and needs no Go stage.

```
frontend/Dockerfile   node:22-alpine -> npm ci, generate the client, build the SPA
                      nginx:alpine   -> COPY dist + nginx.conf, no Node at runtime
backend/Dockerfile    golang-alpine  -> go build
                      alpine         -> the binary plus the committed openapi.json
```

`nginx:alpine` is **pinned to a minor or a digest**, not a floating tag, and `frontend/Dockerfile` joins dependabot's docker ecosystem. The frontend Dockerfile **copies narrowly and in dependency order** (`frontend/package*.json` and `clients/ts/package*.json`, then `npm ci`, then `backend/openapi/openapi.json`, then `clients/ts/`, then `frontend/`): a bare `COPY . .` at a root context hashes the whole repository, so every backend commit would trigger a full `npm ci` and Vite build, which is exactly the cost 9.1 claims the split avoids.

The backend image keeps `context: ./backend`. The frontend image's context is the repository root, hence `.dockerignore`, covering **five** `node_modules` trees after the split:

```
.git
**/node_modules/
secrets/
.env
.env.local
backups/
test/e2e/artifacts/
test/e2e/fixtures/pebble/certs/
frontend/dist/
clients/ts/src/
clients/ts/dist/
backend/ssl/
*.log
```

`secrets/` holds real credential material from `make setup`, `.env` the real database and session configuration, and `backups/` `pg_dump` output containing password hashes. All three exist on disk now, and a Docker layer is immutable, so a file copied in and later deleted is still in the image.

**Independent rollback needs image tags, which compose does not have today.** Both application services use `build:` with no `image:`, so "roll back one half" is a `git checkout` and a rebuild, which the single image could already do. Both services gain `image: ${BACKEND_IMAGE:-...}` / `${WEB_IMAGE:-...}` alongside `build:`, and `docker-build.yml` tags both with the commit sha. Without that indirection the deployment split buys nothing operationally over one image.

### D6. Keep cookie sessions, add session-bound CSRF, add `__Host-` prefixes

**Decision.** The cookie session stays. `SameSite` tightens from `Lax` to `Strict`. The entire OIDC round-trip state moves to a separate `Lax` cookie (5.3). CSRF moves from a hidden form field to a readable `step-ui-csrf` cookie plus an `X-CSRF-Token` header, compared against the value **in the session** with `subtle.ConstantTimeCompare`, exactly as `h.csrfOK` does today.

**All three cookies carry the `__Host-` prefix when `SESSION_SECURE=true`:** `__Host-step-ui`, `__Host-step-ui-csrf` and `__Host-step-ui-oidc` (5.3). Cookies are not origin-scoped, so without the prefix any sibling host on the same registrable domain, or anything that can take over a subdomain, can set `Domain=corp.example` cookies that shadow ours. The consequences are real: tossing a session cookie lands the victim inside the attacker's account, and tossing a mismatched CSRF value is a permanent denial of service on every mutation. The prefix binds both cookies to this exact host with `Path=/` and no `Domain`, which is precisely the shape `store.Options` already has (`main.go:194-200`). Cookie names become config-derived, prefixed when `SessionSecure` and bare otherwise, so local HTTP development still works. `test/e2e` helpers and `src/api/client.ts` read the prefixed name.

**Why the rest.** No token in `localStorage`, no XSS-exfiltratable credential, no refresh machinery, no change to the session epoch logic. The generated client sets the header in one interceptor, so no call site knows CSRF exists.

**Why this is not classic double-submit.** The cookie is transport, not the comparand. The server compares the header against the session, which is the session-bound variant and needs no additional signing. **This depends on one thing that must never change: no CORS middleware is ever added and no `Access-Control-Allow-*` header is ever emitted.** The custom-header requirement is only a CSRF defence while a cross-origin page cannot get a preflight approved. It is an acceptance criterion for that reason.

**Rejected: JWT in `localStorage`.** Strictly worse security for zero benefit in a same-origin BFF.

### D7. React 19, Vite, TanStack Query, React Router, existing CSS

**Decision.** Vite + React 19 + TypeScript strict. TanStack Query for all server state, fed by the generated query options. React Router for routing. No global client state library. The existing CSS moves to `frontend/src/styles/` mostly unchanged.

**Rejected: Next.js.** SSR and a Node runtime in the image, to replace the SSR being removed.

**Rejected: a component library (MUI, Chakra, shadcn).** Not in the first pass. The existing CSS is complete, themed and already passes the CSP. Adopting a library is a visual redesign wearing a technical hat, and it would triple the diff of every phase. There is also a hard constraint most people discover late: `style-src 'self'` with no `unsafe-inline` kills any library that injects a `<style>` tag or uses a CSS-in-JS runtime, which is most of them.

**Rejected: TanStack Router.** Better types, but React Router is what most contributors assume and the routing here is trivial.

See D12 for the eight architecture decisions this stack does not make on its own.

### D8. Versioning, consumption, and publication

**Decision, in three parts.**

**Version.** `0.<MINOR>.<PATCH>-sha.<short-sha>`, where MINOR lives in `openapi/package-version.txt` as a human signal of contract era, and **PATCH is derived**, not hand-edited: `git rev-list --count origin/main`, which is monotonic by construction and unique per commit. That command needs the full history, so **the `client` job must set `fetch-depth: 0`**. With the default depth of 1 there is no `origin/main` ref at all and the command fails outright rather than returning a wrong number. `ci.yml`'s two existing jobs already set it. The new job must too. The in-image pack cannot run it at all and stamps `0.<MINOR>.0-dev` instead (D5). A hand-edited version number earns its keep only when a resolver consumes the human's compatibility judgement, and here the sole consumer resolves by path. Two pull requests bumping a hand-edited file to the same value **merge without conflict**, hiding the collision until `npm publish` rejects the duplicate and reddens `main` on a clean merge with nobody at fault. Deriving PATCH removes that failure mode entirely.

**Consumption.** The client is **not** a declared dependency of `frontend` and does not appear in its lockfile. Every consumer runs `npm ci` first, then `npm install --no-save ../clients/ts/dist/client.tgz`, **in that order and never the reverse**, because `npm ci` deletes `node_modules` wholesale and would discard an earlier install. CI then asserts provenance before any other frontend step:

```
node -p "require('@andremmfaria/step-ca-ui-client/package.json').version"   # must contain the short sha
```

That assertion is the point of the whole design. Without it the frontend job can install a stale client from an npm cache, a previously published version, or a lockfile a developer edited, and go green against a contract the Go code does not implement. Keeping the client out of the lockfile also removes the content-addressed-integrity question that would otherwise churn the lockfile on every contract change.

**Publication.** Not on every merge. Every push and pull request produces the tarball as a workflow artifact. **A `v*` tag additionally publishes to GitHub Packages and attaches the tarball to the release.** Both in-repo consumers, `frontend` and eventually `test/e2e`, resolve the artifact from the current run and never the registry. Publishing per merge would add the republish-collision failure class, the R3 token trap, and a registry version that a developer could install and thereby reproduce exactly the stale-client bug the design exists to prevent.

### D9. `/openapi.json` and `/docs` are not public

**Decision.** Set `cfg.DocsPath = ""` and `cfg.OpenAPIPath = ""` so huma registers nothing on the adapter. `/openapi.json` is served by an explicit chi route from the committed file, behind **the API-contract session wrapper from Phase 1**, not `mw.RequireLogin`, which redirects with `302` to `/login` on every rejection path (`middleware/middleware.go:81,86,97,109,140`) and so cannot satisfy a 401 criterion and would point at a deleted route after Phase 9. The committed spec reaches the image via `//go:embed openapi/openapi.json` in the backend, which does not violate the clean-checkout criterion because it is committed source rather than a build artifact. `/docs` is not served at all, and nginx has no static rule for `/openapi.json`: it proxies it like any other API path so the authorisation actually applies.

**Why, and why it is not obscurity.** Endpoint names are guessable from the SPA bundle, so hiding the spec from an authenticated user buys nothing. Two things are not obscurity. First, anything huma auto-registers on the adapter is a path `api.UseMiddleware` never sees, and this design has exactly one authorisation chokepoint, so leaving adapter-registered routes in place is a standing hole in the mechanism rather than a decision about one document. Second, 5.5 emits `x-required-role` into the spec. A public spec then hands an unauthenticated caller a machine-readable map of which paths are admin, which is a targeting aid that costs nothing to withhold. The same reasoning applies to the admin console, and both are decided here, once.

### D10. The API is a BFF, with two rules that keep aggregates honest

**Decision.** Where a page needs four resources, the API gets one page-shaped aggregate rather than four requests stitched in TypeScript. `GET /api/v1/dashboard` and `GET /api/v1/admin/overview` are the canonical examples. Mutations are always resource-shaped and single-purpose.

**Two kinds of aggregate, only one of which is page-coupled.** `certificates/options` and `acme/options` are not view models: they enumerate what the sibling `POST` accepts, their shape is pinned by a request schema, and a page redesign does not move them. `dashboard` and `admin/overview` *are* view models and will change shape when the page changes. That is fine, and it is the point of a BFF, but only under two rules:

1. **An aggregate may only compose fields independently obtainable from a resource-shaped operation.** If an aggregate needs a value with no independent source, the source is added first and the aggregate calls it. Aggregates are then free to change shape with the page, because no consumer is ever forced through one to reach data.
2. **An aggregate carries exactly one role, and every part it composes requires that role or less.** A field needing a higher role forces a split into two operations, never an optional field. This matters because the role-matrix test sees one `(method, path, role)` triple per operation and structurally cannot catch a mixed-tier aggregate. Concretely: `GET /api/v1/dashboard` is viewer and draws from certificate and ACME counts and CA health only, never from `auth_log`, the security log, `users`, or system info.

**Boundary on mutations.** Today's `POST /admin/users` switches on a form field with six cases. It is split into five operations. `POST /profile` splits into three and `POST /import` into two (Section 6).

### D11. `step-ui-go/` is renamed to `backend/`, and the SPA lives in `frontend/`

**Decision.** Fixed by the repository owner: the Go directory becomes `backend/` and the new SPA directory is `frontend/`. Neither name is up for revision. The rename lands **directly on `main` as its own pull request, before any API work starts.** Every phase lands on `main` (Section 8), and a tree-wide rename that is not first in that sequence turns every subsequent phase pull request into a rename-versus-content conflict across the whole repository.

**What the rename does not touch.** The Go module is `module step-ui` (`go.mod:1`) and every internal import is `step-ui/handlers` and similar. The module path is independent of the directory and is left alone. The reason is not "no benefit": the binary, the compose service name, the session cookie name and the image name are all `step-ui`, the compose service name and the cookie name are load-bearing (`BASE_URL=https://step-ui:8443` and `docker compose exec step-ui` throughout the e2e harness), and neither can be renamed cheaply. Renaming the module alone would trade one inconsistency for a worse one.

**The compose service names also stay**, for the same reason. The Go service remains `step-ui` and the new nginx service is `step-ui-web`. Renaming the Go service to match its new directory would churn every `docker compose exec step-ui` and every `BASE_URL` in the harness in the same commit as a tree-wide path rename, which is exactly the kind of compound diff this decision exists to avoid.

**Blast radius, all in the same commit.** 52 occurrences across 15 files:

| File | What breaks |
|---|---|
| `.github/workflows/ci.yml` | `working-directory` on both jobs, `go-version-file`, `cache-dependency-path`, the coverage artifact path, golangci-lint's `working-directory` |
| `.github/workflows/codeql.yml` | `go-version-file`, `cache-dependency-path`, `cd step-ui-go && go build ./...`. Goes red on the rename commit if missed |
| `.github/workflows/e2e.yml` | `context:` and `file:` on the application build (the harness build at `context: ./test/e2e` is unaffected) |
| `.github/workflows/docker-build.yml` | same context and file pins |
| `.github/workflows/security.yml` | four distinct sites: gosec's `./step-ui-go/...` plus its setup-go pair, govulncheck's `working-directory` plus its setup-go pair, and `trivy-image`'s `docker build -f step-ui-go/Dockerfile step-ui-go`, which is a **Docker context** and therefore also needs Phase 3's context widening |
| `.github/workflows/lint-meta.yml` | the `style` job's stylelint, eslint and djlint globs, **and** the `hadolint` job's `dockerfile:` path |
| `.github/dependabot.yml` | the `gomod` and `docker` directories |
| `docker-compose.yml` | `context: ./step-ui-go` |
| `Makefile` | `GO_DIR := step-ui-go` at line 7 |
| `.gitignore` | `step-ui-go/step-ui`, `*.exe`, `ssl/`, `vendor/` |
| `.pre-commit-config.yaml` | five `--config` and `files` sites plus the golangci config path |
| `test/e2e/helpers/routes.ts` | reads `step-ui-go/main.go` by path and **throws if it derives zero routes**, so it fails loudly |
| `test/e2e/specs/config/static-01.api.spec.ts:82` | `path.join(REPO_ROOT, "step-ui-go", "static")`, a written passing spec |
| `test/e2e/eslint.config.js` | comments only |
| `plans/*.md`, `README.md` | prose, can follow |

Also confirm `scripts/coverage-gate.sh` holds no hardcoded path. **`dependabot.yml` is read from the default branch only**, so its path change takes effect only after merge, and open Dependabot pull requests should be closed before the rename lands.

### D12. Frontend architecture decisions, made once

D7 picks dependencies. It does not make these eight decisions, and without them five phases will answer each of them differently.

1. **Forms.** React Hook Form plus zod for client-side shape. The server remains the only authority on rules, and generated types give request shapes, never validators.
2. **Toast.** The flash mechanism is deleted and 5.2 promises client-side toast. Most toast libraries inject a runtime `<style>` tag, which `style-src 'self'` blocks. **Hand-roll a small toast context against the ported CSS.** Discovering this in Phase 4 is the failure mode.
3. **Theme, and the flash of wrong theme.** `base.html:4` sets `data-theme` server-side from the session. In an SPA the theme is unknown until the session query resolves, and the usual inline bootstrap script is illegal under this CSP. Decision: a small **hashed** `<head>` script reads `localStorage`, applies `data-theme` before first paint, and reconciles against `GET /api/v1/session`. `auto` resolves via `prefers-color-scheme`, which is an improvement, since the server currently degrades `auto` to dark.
4. **Router versus data.** No React Router loaders. All server data flows through TanStack Query inside components. The router routes.
5. **Auth gate.** `<AuthGate>` renders a splash until the boot session query settles and branches on the `state` discriminator (5.3), never on a 401.
6. **Boundaries and states.** One root error boundary plus one per layout. Three shared primitives, `<Skeleton>`, `<EmptyState>`, `<ErrorState>`, with the rule that no route renders a bare spinner and no list renders nothing.
7. **Code splitting.** Admin routes are a lazy chunk. The rest ships in the main bundle. `manualChunks` is not hand-tuned until a measured problem exists.
8. **Route structure.** `routes/` does **not** mirror templates one for one. Two layout routes (`AppLayout`, `AdminLayout`) with nested children, an auth layout shared by login, forgot and reset, `/` and `/dashboard` as one component, and nav items in a single `navigation.ts` carrying `requiredRole`, filtered by the role ordering from `GET /api/v1/config`. 33 templates is roughly 28 routes and two layouts.

---

## 4. Target architecture

### 4.1 Repository layout

Post-rename (D11).

```
step-ca-ui/
├── frontend/                NEW. Vite + React + TS. Its own package.json.
│   ├── src/
│   │   ├── api/                     client config, CSRF interceptor, scoped 401 handling, invalidation map
│   │   ├── routes/                  two layouts plus nested route components (D12.8)
│   │   ├── components/              shared primitives extracted from base.html and admin_base.html
│   │   ├── styles/                  ported from backend/static/css
│   │   └── main.tsx
│   ├── index.html
│   ├── vite.config.ts
│   ├── nginx.conf                   the origin: static, SPA fallback, CSP, proxy to backend (D5)
│   ├── Dockerfile                   node build stage -> nginx:alpine runtime
│   └── package.json                 does NOT declare the client package (D8)
├── backend/
│   ├── api/                         huma operation registration, split by domain
│   │   ├── api.go                   huma.API construction, middleware chain, error transformer
│   │   ├── session.go  certificates.go  acme.go  admin.go  users.go  profile.go  system.go
│   ├── apitypes/                    request and response DTOs. stdlib and in-repo types only (D3).
│   ├── cmd/openapi/main.go          deterministic spec dump. No DB, no CA, no env.
│   ├── openapi/
│   │   ├── openapi.json             generated, committed, gated
│   │   └── package-version.txt      MINOR only. PATCH is derived (D8).
│   ├── handlers/                    KEPT. becomes the service layer api/ calls.
│   ├── templates/                   DELETED in Phase 9
│   └── static/                      DELETED in Phase 9 (css moves, not deleted, in Phase 3)
├── clients/ts/                      generator config, package manifest, committed lockfile.
│                                    src/ and dist/ are gitignored.
├── test/e2e/
└── plans/
```

### 4.2 Runtime topology

**Four containers**, up from three. `postgres` and `step-ca` are unchanged. `step-ui` keeps its name (renaming it would churn every `docker compose exec step-ui` and `BASE_URL` in the e2e harness for no benefit) and stops publishing a host port. A new `step-ui-web` runs nginx, publishes the host port, and is the only service the browser or the e2e harness ever addresses.

```
browser ──TLS──> step-ui-web (nginx)  ──TLS──> step-ui (Go)  ──> postgres
                   │  /  and /assets/                          └─> step-ca
                   └─ proxy: /api /auth /health /ready /openapi.json
```

Volumes: the six existing ones, with `step-ui-ssl` now mounted read-only into `step-ui-web` so nginx can serve the leaf the Go container issues (D5). Compose gains `step-ui-web depends_on: step-ui: condition: service_healthy`, and the healthcheck that today curls `https://localhost:8443/health` inside the Go container stays exactly where it is, with a second, simpler one on nginx.

The frontend image's build context is the repository root, which requires `.dockerignore` in the same commit or all five `node_modules` trees land in the context and destroy caching. The backend image's context stays `./backend`.

### 4.3 Request lifecycle

```
browser
  └─ GET /certificates/12                  -> nginx -> try_files -> index.html   (Go never sees it)
  └─ GET /assets/index-a1b2c3.js           -> nginx -> static     -> immutable cache
  └─ GET /api/v1/certificates/12           -> nginx -> proxy_pass https://step-ui:8443
                                              -> chi -> Recoverer, RealIP, SecurityHeaders
                                              -> huma adapter
                                              -> unwrap middleware   (stashes r and w on the huma context)
                                              -> session middleware  (401 unless auth: public/optional)
                                              -> role middleware     (403; DENIES if no role metadata)
                                              -> rate-limit middleware (only if Metadata["ratelimit"])
                                              -> huma schema validation (422 on failure)
                                              -> api.getCertificate(ctx, *GetCertificateInput)
                                              -> handlers service call
                                              -> *GetCertificateOutput -> JSON
  └─ GET /api/v1/typo                      -> nginx -> proxy -> chi NotFound -> 404 problem+json
```

Two consequences of nginx owning the first hop. The SPA fallback is `try_files` in nginx, not Go, so the earlier design's "the fallback must not swallow `/api/`" trap disappears: nginx proxies those paths and never falls back for them. And `RealIP` only produces the true client address because nginx sets `X-Forwarded-For` and `cfg.TrustProxy` is on, which D5 explains is mandatory rather than optional now.

`chiMiddleware.Recoverer` writes `text/plain` on panic and huma has no recovery of its own, so a panic inside an operation produces a 500 that is not a problem document. Add a problem-emitting recoverer inside the huma chain in Phase 1.

### 4.4 Development loop

`make dev` brings postgres, step-ca **and the Go container** up under compose with `--wait` (a `stepca` bootstrap can take a minute, and Vite starting immediately means the first `getSession` hits nothing), and runs only Vite on the host. The Go container still issues `web.crt` in this mode even though nothing reads it, which is harmless in itself and is the reason `WEB_HOSTNAME` must be required rather than falling back to `os.Hostname()`: otherwise a `make dev` run writes a certificate whose only SAN is a container id, it survives on the volume, and the next `make up` serves it to a browser that rejects it with no clue where it came from. `step-ui-web` is not started in this mode: Vite replaces nginx as the origin, using the same path-routing rules, which is what keeps the dev loop honest. Running the Go binary on the host would need postgres and step-ca reachable from the host and the secrets files readable there, which is a mode the repository does not support and this plan is not going to build.

Vite proxies the same five path prefixes nginx proxies, to the same backend, with `secure: false`. **The proxy rules in `vite.config.ts` and the `location` blocks in `nginx.conf` are the same routing table written twice**, and a divergence between them is a bug that only shows up in one environment. Keep the prefix list in one place in the repository, generate or lint both from it, and say so in the Phase 3 PR.

**The claim that Vite "keeps the dev loop honest" is only half true, and the half it misses is the expensive one.** It keeps origin-sameness honest, which is what cookies and CSRF depend on. It exercises nothing else nginx does. The full divergence list: no CSP at all, so `style-src 'self'` with no `unsafe-inline`, the single hardest constraint in D7 and D12, is never tested where the code is written; no `X-Forwarded-For`, so with `TRUST_PROXY=true` and a `/32` list every dev request shares the gateway address and five bad passwords lock the developer out in a way that reads as a login bug; no cache headers; no `client_max_body_size`; different `try_files` precedence on encoded paths; a different `Host`; no HTTP/2; no compression. **A dev loop that never exercises the origin is fine for component work and unacceptable for anything touching headers, CSP, routing prefixes, caching or auth transport**, which is precisely the set this migration introduces.

The answer is not to run nginx in dev (HMR websockets need their own proxy pair and a dev-only config, giving three routing tables instead of two, which R26 argues against) and not to make the loop a build (a sub-second edit becomes a 10 to 30 second rebuild). It is two additions. **`make preview` builds both images and runs the full four-container stack**, and is a mandatory pre-push step for any change touching `nginx.conf`, `vite.config.ts`, the CSP or the shared prefix list. And 4.4's single source of routing truth extends to carry the header set, with `vite.config.ts` emitting the same CSP from it through a `configureServer` middleware.

Same origin from the browser's point of view, so cookies and CSRF behave as in production, **with these documented exceptions**:

- Dev is HTTP, so TLS is not exercised and `Secure` cookies work only because browsers treat `http://localhost` as a secure context. `__Host-` prefixing is therefore off in dev (D6), and cookies are not scoped by port, so a session set against the compose UI port is also sent to `:5173`, which produces confusing state if both are used in one browser profile.
- **OIDC does not traverse the Vite proxy.** The issuer redirects the browser to the registered redirect URI, which the app builds from `PUBLIC_BASE_URL`. Without `PUBLIC_BASE_URL=http://localhost:5173` in the dev environment and a matching redirect URI registered at the issuer, the callback lands on the backend origin and is served the embedded placeholder.

Makefile targets, following the existing file's conventions (`GO_DIR`, `##` help comments), with two new directory variables `WEB_DIR := frontend` and `CLIENT_DIR := clients/ts` (and `GO_DIR` becomes `backend`). None of these names collide with existing targets.

```makefile
openapi:   ## Regenerate openapi.json from Go source
	cd $(GO_DIR) && go run ./cmd/openapi -out openapi/openapi.json

client:    ## Generate, build and pack the TS client, install into the React app
	cd $(CLIENT_DIR) && npm ci && npm run generate && npm run build && npm pack
	mv $(CLIENT_DIR)/*.tgz $(CLIENT_DIR)/dist/client.tgz
	cd $(WEB_DIR) && npm ci && npm install --no-save ../$(CLIENT_DIR)/dist/client.tgz

dev:       ## Compose backend up, Vite on host with hot reload
	$(COMPOSE) up -d postgres step-ca step-ui
	cd $(WEB_DIR) && npm run dev

clean:     ## also removes the generated client and the SPA bundle
	rm -rf $(CLIENT_DIR)/src $(CLIENT_DIR)/dist $(WEB_DIR)/dist
```

Plus `frontend-install`, `frontend-build`, `frontend-lint`, `frontend-typecheck`, `frontend-test`, and `hooks` (registers 7.2's merge driver). The **rename of the packed tarball to the fixed path `clients/ts/dist/client.tgz` is load-bearing**, because `npm pack` emits a version-stamped filename and both CI and local development resolve the fixed path (D8).

**`make dev` never passes `--build`.** With it, every invocation triggers the full frontend image build, Node and SPA stages included, just to start a backend whose SPA nobody is about to load. Without it, a changed Go source silently serves a stale image. The rule: `make up` builds, `make dev` starts what is already built. Say so in the target's help text.

---

## 5. API conventions

### 5.1 Base path and versioning

All JSON operations live under `/api/v1`.

**Versioning is a requirement, and the honest policy is narrower than the obvious one.** The repository owner asked for a versioned API despite there being no second consumer. Keeping the segment is right. Writing the textbook policy around it would not be, and an earlier draft did, in a way that contradicted Section 2's own gate.

The obvious policy is "additive does not bump, breaking bumps the segment, `v1` and `v2` coexist". That is the policy for a **published** API. This one is not: D8 publishes on `v*` tags only, none is planned, the sole consumer resolves the client by path from the same CI run, and 1.3 makes a public surface a non-goal. **An old client cannot exist**, so a mechanism whose purpose is keeping old clients working is a naming convention wearing a policy's clothes. Coexistence is also undeliverable here for a reason worth naming: with no telemetry, no access-log analysis and no consumer registry, **nobody can tell when `v1` has stopped being called**, so "coexist" with no sunset rule means `v1` is maintained forever by one team across two role tables and two aggregate trees.

**The operative rule.** Breaking changes land in `v1` in lockstep, because both artefacts are built from one commit. `oasdiff breaking` is a review signal that such a change is happening, plus the prompt to re-ask whether an external consumer now exists, and Section 2's MINOR-bump criterion is what it enforces. **The path segment bumps when, and only when, a consumer exists that this repository does not build in the same CI run.** That is the trigger, and it is also the trigger for reopening 1.3's non-goal and D10's aggregates.

**Two things are done now because they are cheap now and expensive later.** `/api/v1` exists once, as a Go constant, and every scope derives from it: today it would be a string literal in the CSRF middleware's scope (5.4), `MaxBytesReader`'s wrap (5.7), Go's CSP scoping (D5), `X-Session-Expires-At`'s emission scope (5.3) and the 404 handler, plus `nginx.conf` and `vite.config.ts`. That is a correctness defect independent of versioning. And the role golden table carries a `version` column keyed as `(version, method, path)`, without which a `v1` operation and its `v2` successor cannot carry different roles, which is the main reason to ship a `v2` at all.

Deferred, with the cost stated so it is a decision rather than a surprise: one spec document per version with the drift gate and `oasdiff` looping; a version axis in the client package's `exports` map rather than two packages, since D8's provenance assertion is per package; `apitypes/` split per version with `depguard` forbidding v2 importing v1, because a shared DTO is exactly how `v1` breaks when `v2` changes; D10 rule 3, that an aggregate composes only sources of its own version; and a sunset rule, which needs a consumer register that does not exist.

The segment also makes `/api/` an unambiguous prefix for nginx's proxy rules (D5), which is a second, smaller reason to have it.

`GET /api/status` becomes `GET /api/v1/status`. **`GET /health` and `GET /ready` stay unversioned probe routes and are not mirrored under `/api/v1`**, but the reason an earlier draft gave for that is now false: nginx serves the SPA from its own image, so the document loads with the backend down. Three reasons that survive. Mirroring needs a third `auth` carve-out and 5.5's test currently fails on any `optional` operation other than `getSession`, for no gain. The SPA does not need a probe, it needs to know whether the API it talks to answers, and it learns that from its own boot query failing (5.10) rather than from a second way to be wrong about the same fact. And `/ready` does a database ping plus a three-second call to the CA (`handlers/health.go:29-59,64-104`), unauthenticated, so handing it to the browser makes it a poll target for every open tab. The SPA's system view is `GET /api/v1/status`.

### 5.2 Error model

RFC 9457 `application/problem+json`, huma's default (9457 obsoletes 7807 and keeps the media type).

```json
{
  "type": "https://step-ca-ui/errors/validation",
  "title": "Unprocessable Entity",
  "status": 422,
  "detail": "validation failed",
  "instance": "/api/v1/certificates",
  "errors": [{ "location": "body.name", "message": "expected string, got null" }]
}
```

Rules:

- **Schema validation failure is `422`, not `400`.** That is what huma emits, and it registers a 422 response on every operation automatically, which satisfies the "zero operations without a declared error response" criterion for free. `400` is reserved for malformed JSON.
- **An error transformer strips `value` from every `errors[]` entry.** Huma's `ErrorDetail` carries the submitted value, so without this a malformed login body puts the submitted password into a problem document that the SPA surfaces in a toast and any error reporter or e2e artifact then carries. Validation feedback names the field, never the content. A test posts a wrong-typed password field and asserts the value is absent from the response.
- Never return a `302` from `/api/v1`. Session expiry is `401`. Wrong role is `403`. Missing entity is `404`. CSRF mismatch is `403` with `type: .../csrf`.
- Login failure is `401` with a deliberately non-specific `detail`, matching today's refusal to distinguish unknown user from wrong password.
- Rate-limit block is `429` with `Retry-After` computed from the window, never echoed from input. `security.RL.Left(ip)` goes in a problem extension, which is the same disclosure already made in today's rendered failure text (`handlers/auth.go:93`).
- **Bulk operations return `200` with a per-item `results: [{ref, status, error?}]` array** and never a problem document for partial failure. A problem document means the whole operation was rejected.
- The flash mechanism disappears. Success feedback is the response body plus a client-side toast. Failure feedback is the problem document.
- Every error response references one named `#/components/schemas/ErrorModel`, including `errors[]`, so the generated `error` union is narrowable rather than `unknown`.

### 5.3 Authentication

**`GET /api/v1/session` returns `200` with a required discriminator and never `401`.**

```
state: 'anonymous' | 'pendingMfa' | 'authenticated'
```

This is load-bearing in three places. It lets a refresh mid-TOTP resume on the code step, which today works because `LoginGet` reads `pending_2fa_user_id` and would otherwise regress silently. It gives the SPA one source of auth state instead of a 401 that means "anonymous" and a 401 that means "expired". And it removes the anonymous-401-that-must-still-set-a-CSRF-cookie sequence, which was the most fragile mechanic in the earlier draft: get the middleware order wrong there and login becomes permanently impossible rather than flaky.

| | |
|---|---|
| `GET /api/v1/session` | `200` with the discriminated `Session` object, the user when authenticated, and the CSRF cookie set on every response. `auth: optional`. The SPA's boot call. |
| `POST /api/v1/session` | body `{username, password}`. Returns the same discriminated `Session`. Clears any existing `pending_2fa_user_id` **before** evaluating credentials, because today `LoginPost` checks the pending state first (`handlers/auth.go:79`) and a parked state would otherwise shadow a fresh credential submission. |
| `POST /api/v1/session/mfa` | body `{code, codeType: 'totp' \| 'recovery'}`. One required pair rather than two optionals, which a flat schema cannot express as "exactly one of". Verifies against the pending user only, never a user named in the request. |
| `DELETE /api/v1/session` | logout. Requires CSRF. Expires both the session and the CSRF cookie. |

**`auth: optional` means the middleware does not `401` on a missing session. It does not mean the session is trusted.** A present session is validated identically to a required one: user reload, `IsActive`, `session_epoch`, absolute lifetime, idle window. A session failing any check is cleared and the response is `state: 'anonymous'`. Without this, an admin who deactivates a user or bumps their epoch still sees the SPA render the full admin surface for up to 8 hours, because the cookie is still cryptographically valid.

**`GET /api/v1/session` is exempt from sliding-window renewal**, and the session middleware sets `X-Session-Expires-At` on every `/api/v1` response so the client always has a current value. `mw.RequireLogin` slides `last_activity` on every request (`middleware/middleware.go:113,130`), and TanStack Query's default `refetchOnWindowFocus: true` would otherwise let an open tab renew an idle session forever, making the idle timeout decorative.

**One implementation of session validation, wrapped twice.** The chi path and the huma path share it. A test asserts that after a `session_epoch` bump the old cookie gets `401 application/problem+json` on an `/api/v1` operation, and that a session past `SessionMaxLifetime` does too. Epoch is the only real revocation available to a client-side cookie store and losing it would be silent.

**OIDC.** The browser redirect flow stays at `/auth/oidc/login` and `/auth/oidc/callback`, outside `/api/v1` and outside the spec.

- **The round-trip cookie carries three values, not one.** `handlers/oidc.go:62-66` stashes `oidc_state`, `oidc_nonce` and `oidc_verifier`, and the callback needs all three: state at `:88`, verifier for PKCE at `:107`, nonce for replay defence at `:137`. With the session at `Strict`, none reach a cross-site callback. All three move to a separate session `step-ui-oidc`, `HttpOnly`, `SameSite=Lax`, `MaxAge=300`, `__Host-` prefixed under D6, deleted on success and failure. This is safe on the shared store because `CookieStore.New` copies `store.Options` per session, exactly as `handlers/auth.go:229`'s `MaxAge = -1` already relies on.
- **Why `Strict` survives anyway.** `completeLogin` sets a `Strict` cookie on the `302`, and the browser will not send it on the redirected navigation to `/`. That does not matter: `/` is the static SPA fallback, and the SPA's subsequent `GET /api/v1/session` is a same-origin fetch that does carry the cookie. This generalises: an emailed link, a bookmark or a typed URL all land on the fallback. Since 5.7 moves downloads to fetches, **no endpoint outside the SPA fallback is ever reached by a cross-site top-level navigation**, and any future emailed deep link targets an SPA route, never an API path.
- **Failure must be able to say something.** The callback flashes an error today, and flash is deleted. On failure it redirects to `/login?error=<code>` with `<code>` from a closed enum, and the SPA maps codes to text. No server-supplied message string is ever rendered into the DOM from a query parameter.

### 5.4 CSRF

- `completeLogin` already mints `s.Values["csrf_token"]` (`handlers/auth.go:201`). It additionally sets the readable CSRF cookie with the same value, `Secure`, `SameSite=Strict`, **not** `HttpOnly`, same `MaxAge` as the session.
- **`GET /api/v1/session` is the only operation that mints a CSRF token for an anonymous caller**, and `completeLogin` re-mints on authentication. Both set the session value and the readable cookie from one value in one response. The SPA configures `retry: false` on that query and runs no other query before it resolves, so two anonymous requests cannot race and pair a readable token with a different response's session.
- A huma middleware rejects any `POST`, `PUT`, `PATCH` or `DELETE` under `/api/v1` whose `X-CSRF-Token` header does not constant-time-match the session value. `POST /api/v1/session` and the password-reset operations are included, matching today.
- The generated client sets the header in one request interceptor. No call site touches it.
- **The `401` interceptor is scoped.** It exempts `POST /api/v1/session`, `POST /api/v1/session/mfa` and every password-reset operation, which return their `401` to the caller as a value. `403` never redirects. Without this scoping a wrong password becomes a page reset and a cleared cache instead of a field error.

**Login CSRF and session fixation are closed.** An attacker cannot set a cookie for our host cross-site, cannot send the custom header cross-origin without CORS, and `SameSite=Strict` blocks the cookie anyway. `completeLogin` wiping `s.Values` (`handlers/auth.go:195`) and minting a fresh token at `:201` closes post-auth fixation, because the cookie **is** the session content. The residual vector is cookie tossing from a sibling host, which is what D6's `__Host-` prefix addresses.

### 5.5 Authorisation

Role requirements live in the operation declaration, not the router:

```go
huma.Register(api, roleOp("admin", huma.Operation{
    OperationID: "revokeCertificate",
    Method:      http.MethodPost,
    Path:        "/api/v1/certificates/{id}/revoke",
    Tags:        []string{"certificates"},
}), a.revokeCertificate)
```

**`roleOp` is the only way a role is written.** It derives `Metadata["role"]`, the `x-required-role` extension and the `Security` entry from one argument. Writing all three by hand invites a change that updates one and leaves the spec documenting an authorisation posture the runtime does not enforce, which is a spec that lies in exactly the place MC-5421 taught this team to look. The role-matrix test asserts all three agree.

**The middleware denies by default.** An operation carrying neither `role` nor `auth` gets `403`. "Requires a logged-in session" is viewer-equivalent, and viewer-by-default on a CA is fail-open. `Metadata["role"] = "viewer"` is written out explicitly on every viewer operation.

**`auth` is three-valued and exhaustive:** `public` (no session, no role), `optional` (session validated if present, never 401, exactly one operation, `getSession`), or absent (session required plus a role). The role golden table (Section 2 is authoritative for its columns) records `auth` alongside `role`, and the test fails on any operation whose `auth` is `optional` other than `getSession`.

**`huma.Context.Operation()` is available at request time**, so one middleware can read the matched operation's metadata.

**The role-matrix test reads the registered operation set, not the committed spec.** `huma.Operation.Metadata` is tagged `yaml:"-"` upstream and never reaches the serialised document, so a test parsing `openapi.json` would see nothing. The test builds the in-process API and walks `api.OpenAPI().Paths[path].<Method>.Metadata`, which is the same pointer that was registered. It asserts that every operation has explicit metadata, that the three role representations agree, and that the full set of triples equals the committed golden table, so adding an operation without a role fails the build.

**What `Security` does and does not buy.** OpenAPI 3.1, unlike 3.0, permits a non-OAuth scheme's requirement array to carry role names. So `{"session": {"admin"}}` against an `apiKey`-in-cookie scheme is legal and self-validates. But it is semantically inert: no generator, validator or runtime enforces it, and `@hey-api/openapi-ts` discards it. It documents. Enforcement is `Metadata["role"]` at runtime and verification is the in-process test.

**A role change is invisible to TypeScript.** The generated client carries no authorisation information, so tightening a role produces a green build and a runtime 403 on a page that still renders a link. Every role change lands with the corresponding `E2E-RBAC` spec in the same pull request.

**Rate limiting is scoped, not global.** The middleware runs only for operations carrying `Metadata["ratelimit"] = "auth"`: `createSession`, `submitMfa`, `requestPasswordReset`, `confirmPasswordReset`. Applied globally, five bad logins from a shared corporate egress IP would `429` every authenticated user, including an admin trying to revoke a compromised certificate. The MFA step must keep calling `RL.Register` on failure as `loginPost2FA` does (`handlers/auth.go:152`), or splitting login into two operations halves the effective attempt cost.

**The user must be reachable from `api/`.** `middleware.ctxKey` and `ctxKeyUser` are unexported with no accessor (`middleware/middleware.go:21-25`). Add `middleware.WithUser` and `middleware.UserFrom` and use them from both paths.

**Unwrapping the request and response.** Because a huma handler receives only a `context.Context` (D2, cost 2), the first middleware stashes the pair:

```go
api.UseMiddleware(func(ctx huma.Context, next func(huma.Context)) {
    r, w := humachi.Unwrap(ctx)
    next(huma.WithValue(ctx, ctxKeyHTTP, httpPair{r, w}))
})
```

Handlers recover `(r, w)` and call existing `handlers` methods unchanged, which is what keeps `gorilla/sessions` usable. Cookies must be set before the handler returns.

**Business invariants carried by the multiplexers are preserved and listed.** `DELETE /api/v1/users/{id}` and `PATCH /api/v1/users/{id}` reject a target equal to the acting user (`handlers/users.go:68,87,109`), or the last admin can demote or deactivate themselves and leave the installation with no admin. Profile operations derive the subject from the session and accept no id. A test asserts both.

**Reauthentication requirements are recorded in Section 6's Reauth column**, because they are invisible in a route table and a rewrite flattens them. `POST /api/v1/profile/mfa/disable` requires the current password **and** a TOTP code today (`handlers/totp.go:136-149`), and `POST /api/v1/profile/password` requires the current password (`handlers/users.go:253`). Both are modelled as `POST` with a body rather than `DELETE`, because a `DELETE` body is discouraged in OpenAPI 3.1 and dropped by some generated clients, and losing the body here silently removes the reauth.

### 5.6 Lists

```
?page=1&pageSize=25&q=<search>&sort=<field>&order=asc|desc
```

Every list response is `{ items, page, pageSize, total, totalPages }`.

- `pageSize` is bounded server-side at 200, expressed in the schema so the client cannot ask for more. The default is 25, replacing today's `const pageSize = 30`.
- **`total` is `integer | null`.** A null total means the server declined to count, and the client renders next and previous instead of page numbers. `totalPages` is null whenever `total` is. This is one word today and a spec break in a year: `db/authlog.go:49` and `db/history.go:40` already run a separate `COUNT(*)` with the same `WHERE`, and with a search term that `WHERE` is an `ILIKE '%x%'` sequential scan counted twice per request on an append-only table with no retention policy.
- `page * pageSize` is bounded server-side at 10 000 and exceeds it with `422`. Deep offsets are not a supported access pattern.
- **List queries order by a total order**, `created_at DESC, id DESC`. `ORDER BY created_at DESC` alone (`db/authlog.go:54`, `db/history.go:45`) permits rows to repeat or vanish across page boundaries when timestamps tie. Real bug today. The SPA makes it visible.
- Array-valued filters use repeated keys, `style: form, explode: true`, never comma-joined, because huma and hey-api must agree or filters silently collapse.

**Cursor paging is deliberately not used.** These are human-browsed admin logs with a page-number paginator in the existing CSS. Cursor paging kills "jump to page 7" and buys nothing at the sizes an internal CA UI produces. The nullable `total` is the escape hatch if that changes.

### 5.7 Binary responses

Certificate, key and CA chain downloads stay real HTTP downloads with `Content-Disposition`, declared with `content: application/octet-stream` and `format: binary`, which generated clients render as `Blob`-returning functions. The frontend saves from the blob rather than navigating, so the CSRF header and `401` handling apply uniformly. There is no `Referer`, `Origin` or `Sec-Fetch-*` check anywhere in the current code, so nothing is lost by the change.

**The blob-and-save convention applies only to responses bounded by construction**: certificates, keys, chains, and the TOTP PNG. `POST /api/v1/admin/backups` creates a backup and returns `{id, sizeBytes, downloadUrl}`, where `downloadUrl` is a **relative** path constrained in the schema by `pattern: ^/api/v1/admin/backups/[A-Za-z0-9_-]+/download$`. The constraint is load-bearing, not tidiness: a path outside a proxied prefix falls through nginx's `try_files` and the browser saves **the SPA shell, `200 text/html`**, named as a backup, which is a corrupt backup that looks like a successful one. Relative also keeps `credentials: 'same-origin'` and the `Strict` session cookie working on the navigation, and that navigation is same-site, which is the one top-level navigation to an API path the design has. Buffering a whole `pg_dump` into JS heap with no progress and no resumption is not acceptable.

**Filenames.** `filename*=UTF-8''<pct-encoded>` with a quoted `filename` fallback. `DownloadCert` and `DownloadKey` already pass names through `safeName` (`handlers/pathsafe.go:116`), but ACME downloads interpolate `cert.Domain` unquoted (`handlers/le.go:219,234`), so the domain is passed through the same validation at serve time rather than trusting the stored row.

**Two mechanics to prove in Phase 0, not discover in Phase 4.** Huma writes a `[]byte` body raw with no base64, but the octet-stream declaration needs a hand-written `Operation.Responses` entry because huma will not derive it from `[]byte`. And multipart needs `huma.MultipartFormFiles[T]`.

**Multipart limits.** `humachi.MultipartMaxMemory` is set to 1 MiB. It is **not** simply raised from its 8 KiB default without a ceiling: `ParseMultipartForm(10 << 20)` today caps memory and spills to temp files, so today's failure mode is disk exhaustion, and an unbounded in-memory limit converts that into heap exhaustion. The chi chain wraps `/api/v1` in `http.MaxBytesReader` at 5 MiB so the total body is bounded before any parser sees it, which is a bound today does not have. A test posts an over-limit body and asserts `413`.

### 5.8 Naming and shapes

- `operationId` is `camelCase` verb-noun. Paths are lowercase kebab. JSON fields are `camelCase`. Timestamps are RFC 3339 UTC strings. Durations are **integer seconds**.
- **Verbs never appear in paths except as `POST /{collection}/{id}/{verb}`.** That is why downloads are `/certificates/{id}/certificate` and `/ca/root`, not `/certificates/{id}/download/certificate` and `/ca/download/root`.
- **Collections are plural nouns, singletons are singular.** This is why the four log collections stop being named `history`, `security-log`, `activity` and `logs`.
- **Role never determines a path prefix.** `/admin/*` is for installation-level operations (console, backup, integrity, about, notifications). That is why the security log and IP blocks live under `/security/` and the audit log is `/audit-events`, all admin-only, none under `/admin/`.
- **`PATCH` bodies are merge-style.** Absent means unchanged. Explicit `null` is rejected. Every partial update in Section 6 depends on this.
- **Secret fields are `writeOnly`**, never present in any response, and absent on write means unchanged. Consequently every settings singleton uses `PATCH`, not `PUT`. `PUT` on a resource whose `GET` omits the secret is the single most reliably-shipped settings bug: either the secret leaks or the first save of an unrelated field clears it.
- **Last write wins on all settings resources.** No `ETag`, no `If-Match`. Optimistic concurrency is deliberately not implemented for a single-tenant internal UI.

### 5.9 Frontend conventions

Without these, five phases produce five answers. Each is one module.

| Concern | Convention |
|---|---|
| Invalidation | `src/api/invalidation.ts` owns a `tag → queryKey[]` map derived from the spec's `tags`. Every mutation invalidates by tag (`invalidate('certificates','dashboard')`). No call site assembles a query key by hand. Aggregates make this mandatory: every certificate mutation also invalidates `dashboard`, and that fact lives in one file. |
| QueryClient defaults | One client with `retry: (n, e) => n < 2 && !isClientError(e)`, `refetchOnWindowFocus: false` (5.3), `staleTime: 30_000`, `throwOnError: false` so query errors are values and error boundaries only catch render crashes. `getSession` overrides to `retry: false`. |
| List and table state | `useListParams()` reads `page`, `pageSize`, `q`, `sort`, `order` from the URL search string as the single source of truth, debounces `q` at 300 ms, and every list query uses `placeholderData: keepPreviousData` so paging does not flash empty. Table state is deep-linkable for the same reason routes are. |
| Server field errors | `problemToFieldErrors(problem)` maps `errors[].location` (`body.name`) to form field paths and is the only path from a `422` to a form. |
| Downloads | `downloadBlob(res, filename)` is the only place `URL.createObjectURL` appears. The filename is derived client-side from the resource name, so no RFC 6266 parsing exists in the frontend. |
| CSRF cookie name | The SPA cannot know whether `SESSION_SECURE` is on, and guessing wrong means no header, a `403 .../csrf` on every mutation, and **login being impossible with no diagnostic**. It therefore probes both names, prefixed first: `['__Host-step-ui-csrf', 'step-ui-csrf'].map(readCookie).find(Boolean)`. This is provably safe rather than merely convenient: under `SESSION_SECURE=true` a bare-named cookie cannot have been set by this server and the prefixed one wins the probe, so a sibling-host toss cannot shadow it, and under false the prefixed name cannot exist. Zero configuration surface. |
| Session expiry | The client reads `X-Session-Expires-At` off every response and keeps the latest value. It is the only source for any expiry countdown, because the value returned once at boot is stale a minute later (5.3). |
| Optimism | None. Every mutation awaits the server and invalidates. Certificate issuance, revocation and role changes are server-authoritative and rollback logic is pure liability. |
| Escaping | ESLint bans `dangerouslySetInnerHTML`, `eval` and `new Function`. The CSP has no `unsafe-eval` and React is now the only escaping layer. |

### 5.10 Two containers, two failure modes the SPA must handle

The split created two states that could not exist when one process served both, and neither has a branch in the design unless it is written here.

**The backend is down and the document is up.** D5 rule 9 deliberately keeps nginx healthy when the backend dies, which is correct. The consequence is that the SPA loads and every call fails. Without a branch, `GET /api/v1/session` returns nginx's HTML 502, the generated client's error union does not narrow it, `<AuthGate>` falls through to "not authenticated" and **renders the login form**, so a user types a password into a form that cannot work and the 401 interceptor never fires because there was no 401. Three parts to the fix: nginx answers upstream failure in the API's own error model (the `@apidown` block in D5); `<AuthGate>` gains a fourth branch, `unavailable`, rendering a retry rather than a login form; and 5.9's `retry: false` on `getSession` narrows to `retry: (n, e) => n < 3 && isNetworkOrServerError(e)`, still `false` for any 4xx. The blanket rule was written for 5.4's CSRF race, which is a 2xx-pairing problem and is untouched by retrying a 503. One limitation stays and is stated rather than fixed: on a cold start where the backend never goes healthy, `depends_on` means nginx never starts and the user gets a connection refusal instead of the screen. Dropping `depends_on` is not an option, since `ssl_certificate` is fatal at startup.

**The two images are at different commits.** The frontend image reads `backend/openapi/openapi.json` out of its own build context, so the SPA is generated from whatever spec that context held, while the backend implements whatever its binary was built with. Two tags, two pushes, one `docker compose pull` can fetch one. Old SPA against new API gives 404s on removed operations, 422s on newly required fields and `undefined` reads on reshaped aggregates, all of which `throwOnError: false` turns into an unattributable error state. **D10's page-shaped aggregates make this worse than a resource API would**, because reshaping `dashboard` is by design, so skew there is guaranteed rather than incidental.

Comparing `appVersion` or the git commit is the obvious answer and the wrong one: it fires on commits that did not touch the contract and stays silent on a rebuild that did. Compare the artefact that matters. The backend computes `sha256` over its embedded `openapi.json` at init and reports it as **`contractSha`** in `GET /api/v1/config` (named without the substring `hash`, which Section 2's property-name test bans). The frontend build stage computes the same digest and defines it into the bundle, empty under `vite dev` so the check is disabled there. On mismatch the SPA renders a persistent, **non-blocking** banner: a false positive that bricks the UI is worse than the skew it detects. Compose pins both services from one `${IMAGE_TAG}` so a partial upgrade is impossible at that layer. Roughly fifteen lines in total.

**A related failure with the same shape.** D12.7 makes admin routes a lazy chunk, nginx serves `/assets/*` immutable and content-hashed, and a frontend deploy replaces the image wholesale, so a user with a tab open who then navigates to an admin route triggers a dynamic import of a filename that now 404s. This could not happen with the server-rendered UI and it happens on frontend-only deploys, which are the common case. Vite emits `vite:preloadError` for exactly this: listen once per session and reload. That listener is also the recovery path for the skew banner above.

---

## 6. Endpoint map

Every route in `main.go:222-325` and what replaces it.

**The Role column is the source of truth** for `Metadata["role"]` and for the role golden table the role-matrix test asserts against. It records the tier the route sits in *today*, verified against the chi groups. Cells reading `public` or `optional` record the operation's `auth` value, which carries no role (5.5). `viewer` means any authenticated user. The Admin, ACME and Profile tables state their single tier in prose above the table rather than repeating it per row. **Reauth** marks operations that additionally require credentials in the body (5.5).

### Public

| Today | Role | Replacement | Notes |
|---|---|---|---|
| `GET /health` | public | `GET /health`, unversioned, not mirrored | `HEALTHCHECK` depends on it |
| `GET /ready` | public | `GET /ready`, unversioned, not mirrored | compose gates depend on it |
| `GET /login` | public | deleted | SPA route |
| `POST /login` | public | `POST /api/v1/session` and `POST /api/v1/session/mfa` | split, 5.3 |
| `GET /forgot-password` | public | deleted | SPA route |
| `POST /forgot-password` | public | `POST /api/v1/password-reset/requests` | always `202`, no state leak |
| `GET /reset-password` | public | `POST /api/v1/password-reset/validate` body `{token}` | **not** a path segment: a token in a path lands in `instance` of every problem document, in route-templated metrics and in trace labels. The SPA strips the token from the address bar with `history.replaceState` after reading it. Validates without consuming, as `handlers/password_reset.go:139-148` does today, so link scanners do not burn tokens |
| `POST /reset-password` | public | `POST /api/v1/password-reset/confirm` | |
| `GET /logout`, `POST /logout` | public | `DELETE /api/v1/session` | the state-changing GET disappears |
| `GET /auth/oidc/login` | public | unchanged, outside the spec | browser redirect |
| `GET /auth/oidc/callback` | public | unchanged, outside the spec | redirect to `/`, or `/login?error=<code>` |
| n/a | public | `GET /api/v1/config` | **new.** `{oidcEnabled, oidcButtonLabel, acmeEnabled, appVersion, roleLevels, sessionIdleTimeoutSeconds, expiringSoonDays}`. `templates/login.html:24` gates the OIDC button on a server-side flag today and the endpoint map otherwise exposes it nowhere, leaving "click and get a 404" as the only discovery mechanism. The SPA hides navigation and login affordances from this and never hardcodes a feature flag or a role ordering |

### Session and dashboard

| Today | Role | Replacement |
|---|---|---|
| n/a | optional | `GET /api/v1/session` (5.3) |
| `GET /` , `GET /dashboard` | viewer | `GET /api/v1/dashboard` (aggregate: certificate and ACME counts, expiring soon, CA status. Never audit or user data, per D10 rule 2) |
| `GET /api/status` | viewer | `GET /api/v1/status` |

### Certificates

| Today | Role | Replacement |
|---|---|---|
| `GET /certificates` | viewer | `GET /api/v1/certificates?page&pageSize&q&status` (the `tab` parameter is dropped: a UI tab in a resource query is exactly the leak D10 exists to prevent, and `status` covers it) |
| `GET /certificates/{id}` | viewer | `GET /api/v1/certificates/{id}` |
| `GET /issue` | manager | `GET /api/v1/certificates/options` (templates, key types, provisioners, defaults) |
| `POST /issue` | manager | `POST /api/v1/certificates` (the field is `name`, not `cert_name`, `handlers/certs.go:166`) |
| `GET /import` | manager | `GET /api/v1/certificates/import-candidates` (unregistered certificates found on disk, which `scanExistingCerts` already computes at `handlers/certs.go:393`) |
| `POST /import` | manager | **two operations by payload.** `POST /api/v1/certificates/import/upload` (multipart) and `POST /api/v1/certificates/import/paths` (JSON array of `{name, domain, certPath, keyPath}`), the latter serving both the manual case and the scan case. `import/scan` ceases to exist: today it re-scans and imports everything unconditionally, swallowing per-item failures into a count (`:477-487`), where the SPA can offer per-item selection for free |
| `POST /renew/{id}` | manager | `POST /api/v1/certificates/{id}/renew` |
| `GET /download/cert/{id}` | manager | `GET /api/v1/certificates/{id}/certificate` |
| `GET /download/key/{id}` | manager | `GET /api/v1/certificates/{id}/key` |
| `POST /revoke/{id}` | **admin** | `POST /api/v1/certificates/{id}/revoke` |
| `GET /download/ca` | **admin** | `GET /api/v1/ca/root` |
| `GET /download/intermediate-ca` | **admin** | `GET /api/v1/ca/intermediate` |
| `GET /download/full-chain` | **admin** | `GET /api/v1/ca/chain` |
| n/a | viewer | `GET /api/v1/ca` (**new**: subject, fingerprint, validity, reachability). The resource the three downloads hang off, and the source `dashboard` composes for CA status under D10 rule 1 |

The role split here is not visible from the page layout and is exactly what a rewrite flattens.

Certificate DTOs carry `expiresAt` **and** a server-computed `expiryStatus: ok | warning | expired`, which retires `templateFuncs.badgeClass`. `daysLeft` and time formatting are client-side derivations of `expiresAt`. The warning threshold is policy shared with the renewer and is exposed as `expiringSoonDays` in `GET /api/v1/config`.

### Security, audit and history

| Today | Role | Replacement |
|---|---|---|
| `GET /history` | viewer | `GET /api/v1/history?cert&action&page&pageSize` (`action` is repeated and multi-valued, which `handlers/history.go:15` supports today) |
| `GET /provisioners` | viewer | `GET /api/v1/provisioners` |
| `GET /admin/security` | admin | `GET /api/v1/security/events?q&outcome=success\|failure&page&pageSize` |
| `GET /admin/activity` | admin | `GET /api/v1/audit-events?page&pageSize` |
| (from `POST /admin/users` `unblock_ip`) | admin | `DELETE /api/v1/security/ip-blocks/{ip}` |

`{ip}` is the canonical form the server itself emitted (lowercase, compressed IPv6, no zone, no brackets), constrained by a `pattern` in the schema. Generated clients `encodeURIComponent` path parameters, so `::1` arrives as `%3A%3A1` and a zone identifier double-encodes, and `security.RL.Clear(ip)` receiving the wrong form is a silent no-op delete rather than an error. A Phase 6 test unblocks `::1` end to end through the generated client. `GET /api/v1/users/{id}` returns `lastIp` and `ipBlocked`, which is what this operation is driven from and which `handlers/users.go:181-190` computes today.

### Admin

Every row is **admin**.

| Today | Replacement |
|---|---|
| `GET /admin` | `GET /api/v1/admin/overview` (aggregate, D10 rule 2 applies) |
| `GET /admin/users` | `GET /api/v1/users?page&pageSize&q` |
| `POST /admin/users` | **five operations.** `handlers/users.go:36-161` switches on `action` with `create` (`:43`), `delete` (`:66`), `change_role` (`:84`), `toggle_active` (`:107`), `unblock_ip` (`:126`), `reset_password` (`:134`). They become `POST /api/v1/users`, `DELETE /api/v1/users/{id}`, `PATCH /api/v1/users/{id}` (covering role and active), `POST /api/v1/users/{id}/password-reset`, and `DELETE /api/v1/security/ip-blocks/{ip}` above. `unblock_ip` is a rate-limiter operation that happens to live on the users form, and modelling it as a user mutation would be wrong |
| `GET /admin/users/{id}` | `GET /api/v1/users/{id}` |
| `GET /admin/users-temp` | `GET /api/v1/temporary-users` |
| `POST /admin/users-temp` | `POST /api/v1/temporary-users` plus `DELETE /api/v1/temporary-users/{id}` |
| `GET /admin/console` | `GET /api/v1/admin/console/commands`. The DTO is `{id, label, description}` **only**: `adminConsoleCommands` embeds `pgIsReadyArgs(cfg.DatabaseURL)` (`handlers/admin_console.go:56-87,160`), so serialising the struct would ship the database host, port, user and name to the client. `Name` and `Args` never leave the server |
| `POST /admin/console` | `POST /api/v1/admin/console/run` body `{commandId}` |
| `GET /admin/about` | `GET /api/v1/admin/about` |
| `GET /admin/integrity` | `GET /api/v1/admin/integrity`. Phase 6 measures its cost and decides on caching before it can go on a poll |
| `GET /admin/backup` | `GET /api/v1/admin/backups` |
| `POST /admin/backup/download` | `POST /api/v1/admin/backups` returning `{id, sizeBytes, downloadUrl}`, then a direct navigation (5.7) |
| `GET /admin/notifications` | `GET /api/v1/admin/notifications` (secrets `writeOnly`, absent from the response) |
| `POST /admin/notifications` | `PATCH /api/v1/admin/notifications` (5.8: `PUT` would clear `SMTPPassword` on the first save of an unrelated field) |
| `POST /admin/notifications/test` | `POST /api/v1/admin/notifications/test` |

### ACME

Every row is **manager**.

| Today | Replacement |
|---|---|
| `GET /le` | `GET /api/v1/acme/certificates` |
| `GET /le/issue` | `GET /api/v1/acme/options` |
| `POST /le/issue` | `POST /api/v1/acme/certificates` |
| `POST /le/{id}/renew` | `POST /api/v1/acme/certificates/{id}/renew` |
| `POST /le/{id}/delete` | `DELETE /api/v1/acme/certificates/{id}` |
| `POST /le/{id}/autorenew` | `PATCH /api/v1/acme/certificates/{id}` body `{autoRenew}` (5.8: one way to model a toggle, and `PATCH /users/{id}` already covers `toggle_active`) |
| `GET /le/download/cert/{id}` | `GET /api/v1/acme/certificates/{id}/certificate` |
| `GET /le/download/key/{id}` | `GET /api/v1/acme/certificates/{id}/key` |
| `GET /le/settings` | `GET /api/v1/acme/settings` |
| `POST /le/settings` | `PATCH /api/v1/acme/settings` |
| `GET /le/logs` | `GET /api/v1/acme/events?page&pageSize&domain` |

**The `le` to `acme` rename covers API paths, `apitypes`, Go identifiers and file names** (`le.go` to `acme.go`, `LECertificate` to `ACMECertificate`, `handlers/le_renewer.go` to `handlers/acme_renewer.go`). It **stops** at the `le_*` database table names and the `LE_*` environment variables, which are operator-facing and migration-bearing. Half-doing the rename is worse than not doing it: a path that says `acme` over a tree that says `le` is a permanent translation layer, and `grep -r acme` on the Go tree would return the library import and nothing else. The boundary is stated here rather than left to be rediscovered.

### Profile

Every row is **viewer**, acting on the session's own account. No id parameter (5.5).

| Today | Replacement | Reauth |
|---|---|---|
| `GET /profile` | `GET /api/v1/profile` | |
| `POST /profile` | **three operations.** `handlers/users.go:204-260` switches on `action` with `theme` (`:212`), `update_info` (`:226`), `change_password` (`:247`). They become `PATCH /api/v1/profile` (name, email), `PATCH /api/v1/profile/preferences` (theme), `POST /api/v1/profile/password` | password: current password |
| `GET /profile/2fa` | `GET /api/v1/profile/mfa` | |
| `POST /profile/2fa/start` | `POST /api/v1/profile/mfa/enrollment` | |
| `GET /profile/2fa/qr` | `GET /api/v1/profile/mfa/enrollment/qr` (image/png, `handlers/totp.go:89`) | |
| `POST /profile/2fa/confirm` | `POST /api/v1/profile/mfa/enrollment/confirm` | |
| `POST /profile/2fa/disable` | `POST /api/v1/profile/mfa/disable` (**not** `DELETE`, 5.5) | current password **and** TOTP code |

**Count: 69 JSON operations**, 4 unversioned browser or probe routes, plus the SPA fallback and the asset handler. Every one of the 65 method-registered routes in `main.go` appears in exactly one row, and no row names a route that does not exist. The `/static/*` handler at `main.go:337` is deleted in Phase 9, not replaced: nginx serves the SPA's assets.

---

## 7. The contract pipeline

### 7.1 Spec generation

`backend/cmd/openapi/main.go`:

```go
func main() {
	out := flag.String("out", "openapi/openapi.json", "output path")
	flag.Parse()

	humaAPI := api.NewForSpec()
	compact, err := humaAPI.OpenAPI().MarshalJSON()
	if err != nil {
		log.Fatal(err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		log.Fatal(err)
	}
	pretty.WriteByte('\n')
	if err := os.WriteFile(*out, pretty.Bytes(), 0o644); err != nil {
		log.Fatal(err)
	}
}
```

Three details are corrections rather than style. `MarshalJSON` returns **compact** JSON, so `json.Indent` is required. The variable must not be named `api`, which would shadow the package. And `SetEscapeHTML(false)` is **not** required: achieving it means round-tripping through `map[string]any`, which is lossy for integers, whereas `json.Indent` operates on bytes and preserves them.

`api.NewForSpec()` builds the huma API against a zero-value `*handlers.Handler` (D3, no `Service` interface). OIDC-conditional routes (`main.go:232-235`) are always registered and gated at runtime, which is a small behaviour change worth stating: with OIDC disabled the operation exists in the spec and returns `404`.

**Huma configuration, identical in `cmd/openapi` and `main.go`.** `cfg.CreateHooks = nil`, because `DefaultConfig`'s `SchemaLinkTransformer` injects a `$schema` property into every struct response body and a `Link` header derived from the request `Host`, which pollutes the generated types and breaks exact-body assertions. `cfg.DocsPath = ""` and `cfg.OpenAPIPath = ""` per D9.

Determinism, and what actually threatens it:

- **Ordering is already safe.** `OpenAPI.Paths` and `Components.Schemas` are plain Go maps marshalled through `map[string]any`, which `encoding/json` key-sorts.
- **One residual ordering risk**: huma's numeric suffixing when disambiguating anonymous-type schema names depends on `Register` order, which is fixed as long as registration is explicit sequential code and never a range over a map. That is a rule.
- **The real threat is configuration leaking into schemas.** See D3 rule 1.
- Two-space indentation, trailing newline. No timestamps, no build-derived version strings, no environment-dependent `servers` entry.
- Phase 0 generates twice and byte-compares. Cheap, and it catches all of the above.

### 7.2 Drift gate

The gate lives in the **`client` job**, not in `build-test-lint`:

```yaml
- name: OpenAPI spec is current
  run: |
    go run ./cmd/openapi -out openapi/openapi.json
    git diff --exit-code openapi/openapi.json \
      || { echo "::error::openapi.json is stale. Run: make openapi (go $(go version))"; exit 1; }
```

Putting it in `build-test-lint` would let the `client` job generate a client from a stale committed spec while the gate fails in parallel. That is contained today only because both checks are required, and any path filter or non-required check reopens it. Generating the client immediately after the gate, in the same job, makes the client provably derived from the spec this commit's Go code produces.

**Line endings first.** `.gitattributes` today has rules for `*.sh`, `Dockerfile`, `*.yml` and `*.yaml` but **no blanket `* text=auto` and no JSON rule**. `cmd/openapi` writes `\n`, so a contributor with `core.autocrlf=true` gets CRLF on checkout, sees every line change when running `make openapi`, and CI on Linux cannot reproduce it. Add `backend/openapi/openapi.json text eol=lf` in Phase 0.

**`openapi.json` is never merged by hand.** It is a pure function of the Go source. `.gitattributes` registers `merge=openapi-regen`, and `make hooks` registers the driver as `cd backend && go run ./cmd/openapi -out openapi/openapi.json`, discarding all three inputs and regenerating from the merged source. Developers who have not run `make hooks` resolve with `git checkout --ours` then `make openapi`.

**The driver's real reach is narrower than it looks, and this must not be misread.** `.gitattributes` only names which driver to invoke. The driver command itself lives in git *config*, which `make hooks` sets per clone. **GitHub's own merge, whether the merge button, a squash, or a merge queue, never executes a developer's local git config**, so the driver is inert for the actual integration path this repository uses. It fires only for a developer's own local `git merge` or `git rebase` before pushing. The protection for `main` is not the driver, it is the required up-to-date-branch rule plus the drift gate as a required check on `push: main`.

**Two branches adding operations usually merge cleanly, and that is the dangerous case.** A clean textual merge of two independent edits to a shared schema such as `ErrorModel`, `Page`, `User` or `Certificate` produces a spec matching neither branch. `main` then carries a stale spec no pull-request gate caught. Mitigation: branch protection requires branches to be up to date before merging, or a merge queue, **and the drift gate is a required check on `push: main` as well as on pull requests**.

**Two things that would otherwise make the gate annoying enough to disable.** D3 rule 2 keeps third-party types out of DTOs, so no dependency bump other than huma's can move the spec. And `huma/v2` plus `humachi` are excluded from dependabot's `go-minor-patch` group so a huma bump always arrives as its own pull request with the spec diff reviewable in isolation, the same treatment R2 gives the TypeScript generator.

**A second gate on breaking changes.** `oasdiff breaking` between the base's `openapi.json` and the pull request's, failing if a breaking change lands without a MINOR bump. This is the only mechanism that turns "remember to bump" into something enforced, and it gives reviewers a far better signal than the raw JSON diff. Three mechanics to specify rather than assume: `oasdiff` is not preinstalled on runners and must be **pinned to a release**, not `@latest`, since every `uses:` in this repository pins to a commit SHA and an unpinned tool install would be the only exception. Obtaining the base document needs `fetch-depth: 0`. And the base ref differs by trigger: on `pull_request` it is the PR's actual base ref, which is not necessarily `main`, while on `push: main` there is no base branch and the comparison is against `github.event.before`.

The pre-commit mirror is scoped to `files: ^backend/(api|apitypes)/`, because otherwise every commit touching any `.go` file, including `db/`, triggers a full module compile.

### 7.3 Client generation

`clients/ts/openapi-ts.config.ts`:

```ts
import { defineConfig } from '@hey-api/openapi-ts'

export default defineConfig({
  input: '../../backend/openapi/openapi.json',
  output: { path: './src', format: 'prettier', lint: 'eslint' },
  plugins: [
    '@hey-api/client-fetch',
    '@hey-api/typescript',
    '@hey-api/sdk',
    '@tanstack/react-query',
  ],
})
```

`clients/ts/src/` and `dist/` are gitignored. `clients/ts/package.json` and `package-lock.json` are committed, holding the package name, the `exports` map, the `tsc` build script, the exact generator pin, and the prettier and eslint the generator config invokes.

### 7.4 Packaging

```
@andremmfaria/step-ca-ui-client
├── dist/index.js        ESM
├── dist/index.d.ts
└── package.json         "type": "module", "sideEffects": false
```

No runtime dependencies. `@tanstack/react-query` and the fetch client are peers. The version is stamped by CI per D8.

### 7.5 Publication

The `client` job packs on every run and uploads the tarball as an artifact. **Publication happens only on `v*` tags**, with `permissions: {contents: read, packages: write}` scoped to that job and `.npmrc` pointing `@andremmfaria:registry=https://npm.pkg.github.com`. The tag workflow re-runs the drift gate and the version derivation first, so a tag can never carry a stale spec.

The publish step is `npm view <pkg>@<version> version || npm publish` so that re-running a tag build is idempotent. Never `npm unpublish`: GitHub Packages restricts deletion and downstream tooling caches aggressively. A bad published version is superseded and `npm deprecate`d.

**Known trap.** A package first created with a personal access token permanently denies `GITHUB_TOKEN`, and the only fix is a manual "Manage Actions access" grant in the package settings UI. Never publish by hand. Let the first publish come from the tag workflow. See R3.

### 7.6 Consumption

Per D8: the client is not in `frontend`'s `package.json` and not in its lockfile. `npm ci` runs first, then `npm install --no-save ../clients/ts/dist/client.tgz`, then the provenance assertion. `eslint`'s `import/no-extraneous-dependencies` is disabled for this one package.

`frontend/src/api/client.ts` configures the generated client once:

```ts
import { client } from '@andremmfaria/step-ca-ui-client'

client.setConfig({ baseUrl: '', credentials: 'same-origin' })

client.interceptors.request.use((req) => {
  const token = readCsrf()                   // probes both names, see 5.9
  if (token && req.method !== 'GET') req.headers.set('X-CSRF-Token', token)
  return req
})

client.interceptors.response.use((res, req) => {
  if (res.status === 401 && !isAuthEndpoint(req.url)) {
    queryClient.clear()
    redirectToLogin()
  }
  return res
})
```

`isAuthEndpoint` covers `POST /api/v1/session`, `POST /api/v1/session/mfa` and the password-reset operations (5.4).

---

## 8. Phases

**There is no long-lived feature branch.** Phases 0 to 8 are additive by construction: huma mounts alongside chi and every template route keeps working. Nothing in Phases 0 to 8 breaks `main`, so each phase is a pull request straight to `main` and **Phase 9 is the only removal, and the only irreversible, pull request.**

**Rejected: a long-lived `feat/spa` branch.** It fails for the same reason D11 puts the rename first: a months-long branch taking nine phases while `main` receives e2e work, dependabot across three new npm ecosystems, and any hotfix, produces exactly the rename-versus-content conflict class the rename ordering exists to avoid, across 46 handler files, `go.mod`, `go.sum` and six workflows. See R20. The coexistence state (two UIs, two authorisation paths) is designed to be indefinitely stable, which is what makes landing on `main` safe and what makes a stall expensive rather than broken.

Each phase PR names the D-numbers and acceptance criteria it discharges, and carries its `Parity` table where it replaces templates.

**Rough size, and the cheapest useful stopping point.** No estimate here is a commitment and Phase 0 recalibrates all of them, but the shape is decision-relevant now. Phases 0 to 3 are contract and scaffolding: high risk, low volume, and they produce **no user-visible change at all**. Phases 4 to 8 are the UI rewrite: low risk, high volume, scaling with 33 templates rather than with anything in this document. Phase 9 is small. Expect roughly a third of the calendar in Phases 0 to 3 and the rest in 4 to 8, with Phase 4 costing more per operation than 5 to 8 because R23's handler-splitting work is front-loaded and unpriced. **There is no deadline**, which the owner has confirmed. That removes schedule pressure as a risk and replaces it with a different one: with nothing forcing a finish, a half-migrated tree can persist indefinitely and quietly become the permanent state (R18). The mitigation is not a date, it is that **completing Phase 4 is an explicitly legitimate end state** rather than a stall: a proven contract, a typed client, one migrated domain, and a coexistence state that is stable by design. Deciding to stop there is a decision. Drifting there is not.

### Phase 0. Rename, then a vertical slice spike

0. **The D11 rename, as its own pull request**, with every file in the blast-radius table. Nothing else in that commit. Close open Dependabot pull requests first, since `dependabot.yml` is read from the default branch only. Fix `test/e2e/helpers/routes.ts` and `specs/config/static-01.api.spec.ts` in the same commit, and fix the pre-existing `make cover` bug while the Makefile is open (`cover` omits the `cd $(GO_DIR)` every other Go target has, so it resolves a repo-root `scripts/coverage-gate.sh` that does not exist) so the implementer does not mistake it for a rename regression.
1. Enable branch protection: require branches up to date before merge (or a merge queue), and make the drift gate a required check on `push: main` as well as on pull requests. This is 7.2's mitigation for the clean-merge-of-a-shared-schema failure and it must exist before two contract PRs are ever open at once.
2. Add huma pinned at `v2.30.0` or newer and `humachi`. Mount a `huma.API` under chi at `/api/v1` alongside every existing route, with `cfg.CreateHooks = nil`, `cfg.DocsPath = ""` and `cfg.OpenAPIPath = ""`.
3. **Add the unwrap middleware from 5.5 and prove a session write works from inside an operation handler.** Highest-risk mechanic in the plan. If it does not work, nothing downstream does.
4. **Do the registration split here, not in Phase 1**: `api.Register(huma.API, *handlers.Handler)` separate from construction, plus `handlers.NewForSpec()`. `cmd/openapi` cannot exist without it (D3), so it cannot be later work.
5. Implement `GET /api/v1/config`, `GET /api/v1/status` and `GET /api/v1/session` with the `state` discriminator and the CSRF cookie on every response. Session validation is written **inline** here, without the shared middleware chain. Phase 1 extracts it into the wrapped-twice implementation of 5.3.
6. Implement one binary download and one multipart upload against a **placeholder resource unrelated to certificates**, purely to prove the hand-written response declaration, the 1 MiB `MultipartMaxMemory` and the `MaxBytesReader` ceiling. Phase 4 reuses the proven pattern for the real endpoints.
7. Add `cmd/openapi`, commit `openapi.json`, add the generate-twice byte-compare test and the two-environment comparison, and add `openapi.json text eol=lf` to `.gitattributes`.
8. Add `clients/ts` with a committed lockfile, and generate a client locally.
9. Add a throwaway `frontend/` that boots against `vite dev` and its proxy, calls `getSession` and renders the username. `GET /api/v1/config` reports `contractSha`, the sha256 of the embedded spec, since 5.10's skew detection needs a producer before it can have a consumer.
10. **A minimal proxy spike**, because Phase 0's exit criterion otherwise de-risks D2 and nothing about D1 or D5, which carry the plan's two most serious findings. Front the throwaway app with nginx implementing only D5 rules 1, 2 and 7, and prove two things: a forged `X-Real-IP` does not move the rate limiter, and a Go-container restart is picked up without an nginx restart. Everything else about the origin, the certificates and the compose topology stays in Phase 3b, which is additive complexity rather than a go-or-no-go question.

**Exit criterion.** Committed as a file, not left in a review comment. `go build ./...` succeeds with no Node run. The spec is byte-identical across two runs, two machines and two environments. The generated client typechecks. The CSRF interceptor works. A protected route returns `application/problem+json` on `401`. A validation failure returns `422` with no submitted value echoed. An unmatched `/api/v1/*` path returns a 404 problem document. If huma's ergonomics, the `Unwrap` mechanic or the spec output are wrong, this is the cheap moment to switch to `oapi-codegen`, and D2 is revisited before Phase 4.

### Phase 1. API foundation

1. `apitypes/` with the shared envelope types, and **`/api/v1` as a single Go constant** every scope derives from (5.1): the CSRF middleware's scope, `MaxBytesReader`'s wrap, the CSP scoping, `X-Session-Expires-At`'s emission and the 404 handler would otherwise each carry the literal, which is a correctness defect independent of versioning. The `depguard` allowlist rules and `gomodguard`'s CORS ban added to `.golangci.yml` (9.2), with the negative fixture the lint run must reject. The property-name spec test, `openapi/secret-allowlist.txt`, and the CODEOWNERS entries covering that file and the `client` job block.
2. Huma middleware, in order: unwrap, session validation (fail closed, `auth: public` and `auth: optional` the only exemptions, `optional` still fully validating a present session), role enforcement (denying on absent metadata), CSRF, scoped rate limiting, problem-emitting recoverer. Extracts Phase 0 step 5's inline logic.
3. `middleware.WithUser` and `middleware.UserFrom`. Adapt `mw.RequireLogin` to the problem contract without breaking the live template routes. One implementation, wrapped twice.
4. `completeLogin` sets the CSRF cookie. `__Host-` prefixes under `SESSION_SECURE`. Session cookie to `SameSite=Strict`. `step-ui-oidc` at `Lax`, `MaxAge=300`, carrying state, nonce **and** verifier. `DELETE /api/v1/session` expires both cookies. `X-Session-Expires-At` on every `/api/v1` response. **Update `test/e2e/helpers/session.ts` for the new cookie name and `SameSite` behaviour in the same PR**, since the store is shared with the still-live template routes.
5. The `roleOp` helper, the role golden table, the route golden file `testdata/routes.golden`, the `x-required-role` base-versus-head diff check from R16, and the role-matrix test asserting all three role representations agree, plus the CORS, CSRF-replay and 401-replay table tests from Section 2.
6. The error transformer that strips `value` from `errors[]`.
6b. **Normalise the recorded client address, in this phase rather than per domain**, because both UIs write the audit log simultaneously for six phases. Seven call sites pass `r.RemoteAddr` where the rest of the codebase passes `clientIP(r)`: `handlers/auth.go:203` and `:225`, `handlers/totp.go:128` and `:162`, `handlers/audit.go:20`, `handlers/oidc.go:85`, `handlers/notifications.go:178`. `LogAuth` writes that value into `users.last_ip` (`db/users.go:240`), so successful logins store `host:port` while the rate limiter keys on bare hosts. The consequence is live today and invisible: `handlers/users.go:183`'s `IPBlocked` badge never fires and the `unblock_ip` button is a no-op. It becomes a delivery problem in Phase 6, where `DELETE /api/v1/security/ip-blocks/{ip}` carries a canonical-IP `pattern` that `422`s on every legacy value, so a one-line `UPDATE ... split_part(ip, ':', 1)` migration or a normalisation on read ships with it.
7. `r.NotFound` and `r.MethodNotAllowed` emit `404` and `405` as `application/problem+json`. There is no Go-side SPA fallback and no Go-side asset handler: nginx owns both (D5).
8. `.gitattributes` merge driver, `make openapi`, `make hooks`, and a **standalone** drift gate plus `oasdiff` gate, which Phase 2 relocates into the `client` job. 7.2's same-job requirement becomes true when the `client` job exists. Until then a standalone gate is strictly better than none.

**Exit criterion.** The role-matrix test passes against the golden table for every registered operation. The drift and `oasdiff` gates are required checks and pass on a no-op PR. A `session_epoch` bump returns `401 application/problem+json` on an `/api/v1` route. A CSRF-mismatched mutation returns `403`. All existing AUTH, CSRF and RBAC e2e specs still pass unmodified against the live template routes.

### Phase 2. Client package pipeline

1. `clients/ts` generator config, package manifest, build script, committed lockfile, and the `publint` plus `attw --pack` checks from Section 2.
2. The `client` job with `fetch-depth: 0`: gate (relocated from Phase 1), `oasdiff`, generate, build, pack, upload artifact. Tag-only publication. Add the `ci-gate` aggregator job.
3. The derived version scheme, the in-image version fallback from D8, and the provenance assertion in the `frontend` job.
4. **Prove the consumption mechanism with a real two-commit test**, committed as `docs/contract-proof.md`, plus the `contract-negative` job that re-runs it.

**Exit criterion.** The two-commit proof is committed with one red and one green run URL. A test tag publishes to GitHub Packages idempotently (re-running the tag build does not fail). `ci-gate` correctly fails when any one dependency fails.

### Phase 3a. Frontend app scaffold

No nginx, no Docker. Runs against Phase 0's `vite dev` proxy straight to the Go container, so nothing here depends on the origin existing.

1. **Tooling:** Vite, React 19, TS strict, ESLint (the `fetch` ban from Section 2 and the `dangerouslySetInnerHTML`/`eval` ban from 5.9), Prettier, Vitest, the dead-SDK-surface script with `clients/ts/unused-allowlist.txt`, and **a second stylelint step in `lint-meta.yml` targeting `frontend/src/styles/`**, added beside the existing `backend/static/css` one rather than replacing it: the copy is the file being actively edited for six phases and would otherwise have no lint coverage at all.
2. **D12 primitives, as real code:** `src/api/client.ts` (the CSRF request interceptor with the two-name cookie probe from 5.9, and the scoped 401 response interceptor), form library, hand-rolled toast, hashed theme bootstrap, error boundaries, the three state primitives, `useListParams`, `invalidation.ts`, `downloadBlob`, `problemToFieldErrors`, the `vite:preloadError` listener and the contract-skew banner from 5.10.
3. **Copy**, do not move, `static/css/*` to `frontend/src/styles/`, with `build.assetsInlineLimit` set so no font inlines to a data URI under `font-src 'self'`, and `build.sourcemap: false` for production. The copy is deliberate: `//go:embed templates static` stays until Phase 9, the live templates reference 39 `/static/...` paths, and moving the files breaks the old UI for six phases. The duplication dies in Phase 9.
4. App shell: the two layouts, navigation from `navigation.ts` filtered by `roleLevels` from `GET /api/v1/config`.
5. Router with the full route table, every leaf a placeholder, exporting the committed route list that Section 2's smoke criteria are table-driven from.
6. `<AuthGate>` on the `state` discriminator with its fourth `unavailable` branch (5.10), plus the login view including two-step MFA and refresh-resumes-on-code-step.

**Exit criterion.** Log in, see an empty dashboard, navigate, hard-refresh a deep link, refresh mid-MFA, log out, all through the Vite proxy. Unit tests pass for the CSRF cookie probe under both `SESSION_SECURE` values, for the scoped 401 interceptor, and for the skew banner. **A side-by-side of the dashboard and one admin page against the current UI**: if matching it needs restructured selectors rather than renamed ones, decide then whether to keep the CSS port or adopt a utility layer, once rather than six times (R21).

### Phase 3b. The origin and the TLS perimeter

The highest-risk phase in the plan. It owns R13, R25 and R26 in full.

1. `frontend/nginx.conf.template` with every block and every rule from D5, `conf.d/proxy-headers.conf`, `conf.d/doc-headers.conf`, and the shared source that `vite.config.ts` and the nginx prefix list both derive from.
2. `frontend/entrypoint.sh`: the certificate wait, the `envsubst` substitution, nginx started in the background with a `trap`/`wait` so `docker stop` drains rather than kills, and the reload watcher. **Two mechanics that will not work if left to the obvious reading.** The stock `nginx:alpine` entrypoint is what normally runs `envsubst` over `/etc/nginx/templates/*.template`, and replacing `ENTRYPOINT` bypasses it, so either the custom script calls `envsubst` itself or the cert-wait and watcher logic ships as a `/docker-entrypoint.d/*.sh` script and the stock entrypoint stays. Pick one in this phase and write it down, because D5's prose currently implies both. And `nginx:alpine`'s busybox `wget` is built **without SSL**, with no `curl` in the image, so every healthcheck against `https://localhost:8443/nginx-health` fails until the runtime stage does `apk add --no-cache curl`.
3. `frontend/Dockerfile`, with the narrow ordered COPYs from D5 and a pinned nginx minor.
4. The second certificate: `WEB_TLS_MODE`, `WEB_HOSTNAME` with no fallback, `web.crt`/`web.key` into `step-ui-web-ssl`, the root CA copied where nginx can read it, and the `.fallback` marker plus its `/ready` reporting.
5. **The renewer restructure**, which is six call-site signature changes rather than an addition: `issueUICert`, `renewUICertOnce` and the four `generateSelfSignedCert` fallback sites parameterised on `(certPath, keyPath, hostname)`, one loop renewing both leaves and sleeping on the minimum so there is one reload event and one backoff. The bootstrap context at `main.go:361-364` goes from `2*` to `3*`, comment included, or the third loop gets an already-cancelled context and self-signs on attempt one. `os.WriteFile` becomes write-to-`.tmp`-then-`os.Rename` for both leaves.
6. **The Go side's proxy adaptation**, which is more than an earlier draft's "and nothing else": `cfg.TrustProxy` plus `TRUSTED_PROXY_CIDRS`, **the deletion of `middleware.forwardedHeaders`** (`realip.go:12`), and the `mw.SecurityHeaders` split from D5 (HSTS removed entirely, CSP scoped off `/api/v1`). `X-Request-Id` is read into the request-scoped log context, so the header nginx sets has a consumer.
7. `make nginx-lint` and its fixture image, built before Phase 4 rather than after, since it is the only fast rung under a config that is now the security perimeter.

**Exit criterion.** All four forgery headers fail to move the recorded address, through the origin and directly against the backend. `docker compose restart step-ui` and the next proxied request succeeds. A renewal with a low `UI_CERT_DURATION` produces a new served certificate, exactly one reload per cycle, and no restart. A verified proxy hop on a real stack. With step-ca unreachable, the backend self-signs, writes `.fallback`, `/ready` goes non-200 and nginx never starts. `make nginx-lint` green.

### Phase 3c. Compose, CI and the migration window

1. **The migration-window routing.** The naive answer, a legacy prefix list beside `location /`, does not work: **every template route is also an SPA route name** under D12.8, so `/certificates` cannot both proxy to Go and fall back to `index.html`, and whichever wins breaks one of this phase's own exit criteria. Instead the SPA is served under `/app/` (Vite `base`, router `basename`), `location /` proxies everything else to the backend, and Phase 9 flips the base to `/`. `__Host-` cookies are `Path=/` and unaffected. Every temporary block carries a `PHASE9-DELETE` marker comment and includes `conf.d/proxy-headers.conf`, without which `POST /login` takes raw client headers for six phases. **Two paths do not move to `/app/`**: `/reset-password` and the OIDC callback landing, because the reset link is emailed and the redirect URI is registered with an IdP this repository cannot edit, so a flip at Phase 9 would break links already in the wild (R31).
2. **Compose:** the fourth container with a static address on a pinned, env-var-defaulted subnet; `step-ui-web-ssl` read-only into nginx and read-write into Go, with `step-ui-ssl` **not** mounted into nginx at all so an RCE there cannot read `server.key`; `init: true`; `depends_on: service_healthy`; the backend healthcheck corrected to `curl -fsk .../health`, which today lacks `-f` and so passes on any status; `UI_TLS_MODE` defaulting to `stepca`; the new `WEB_TLS_MODE`, `WEB_HOSTNAME`, `TRUST_PROXY`, `TRUSTED_PROXY_CIDRS`, `PUBLIC_BASE_URL` and `OIDC_REDIRECT_URL` keys, noting the base file passes **no** OIDC keys today; `image:` tags on both services; the host port moving to `step-ui-web` **on 8443 internally** so only the hostname changes in the harness; `step-ui-web-ssl` added to `make backup`'s volume list; `.dockerignore`; and the four `.gitignore` additions the `.dockerignore` implies but does not cause.
3. **The eight overrides.** `-config`, `-mail` and `-le` are unaffected. `-image` needs a second `image:` pin, which is the one edit that silently defeats the whole override if missed. `-oidc` needs three: `TRUST_PROXY` defaults to false there, `TRUSTED_PROXY_CIDRS` defaults empty (which with the first flipped is now a `log.Fatalf`), and `OIDC_REDIRECT_URL` points at the backend on a path that does not match D9's route. `-fatals`, `-nodeps` and `-fingerprint` need **no edits**, because the new `compose.e2e-noweb.yml` gives `step-ui-web` a never-enabled profile and `scenario.sh` composes it into every scenario, so nginx never exists for the `infra` project. Section 10's table lists those three as breaks and predates that fix.
4. **CI:** `e2e.yml` builds two application images on separate cache scopes and ships both in **one** `docker save | gzip` artifact rather than two, since the per-artifact overhead dominates; `BASE_URL` moves to `step-ui-web` and a new `BACKEND_URL` names the backend directly for the tests that must legitimately bypass the origin (the `infra` tier, the health tier, and the direct half of the forgery table). `docker-build.yml`, `security.yml` (two scans, the nginx base being a new third-party image), `codeql.yml`, `hadolint` as a matrix over two Dockerfiles, `dependabot.yml` gaining a **docker** ecosystem for `/frontend` as well as the npm ones, and `scripts/test_deploy.sh`.
5. **The harness fixes Section 10's table enumerates**, which land here and not per domain: the three `BASE_URL` definitions, `helpers/session.ts`'s four scraping assumptions, `helpers/routes.ts`, `helpers/compose.ts`'s hardcoded service, `collect.sh`'s service list, and the four spec files asserting container-specific facts.
6. `make dev` with `--wait`, plus `make preview`, `up-backend`, `up-web`, `logs-ui`, `logs-web`, `nginx-test`, `web-reload`, and the corrected `e2e-reset-ssl` and `clean`. **`clean` is a correction, not an addition**: adding a second `clean:` recipe silently replaces the first in GNU Make, so `go clean` and the backup removal would stop running with only a warning.

**Exit criterion.** Log in through nginx at `/app/`, navigate, hard-refresh a deep link, log out. With `step-ui` stopped, `/api/v1/session` returns `application/problem+json` and the SPA renders the unavailable state rather than a login form. **Every template route and every `/static/*` asset still reachable through the published port.** The existing `api` e2e specs pass against the new topology. `make preview` green. The CSP smoke spec passes against nginx's header, read off the response rather than the config file.

### Phase 4. Certificates

List, detail, options, issue, import candidates, the two import operations, renew, revoke, the five downloads (two certificate, three CA chain) and `GET /api/v1/ca`. Reuses Phase 0's proven multipart and octet-stream mechanics.

**Do the first three operations before scoping the rest**, and record the observed cost of separating each handler's logic from its response writing, because that split is invisible in the endpoint map and is the unpriced work in R23.

**Exit criterion.** Every certificate operation reachable from the SPA and matching Section 6. Multipart upload and octet-stream download work end to end through the generated client. The rewritten certificate `api` e2e specs pass. The golden table's certificate rows match Section 6's admin/manager/viewer split. `Parity` table signed off. `plans/e2e-tests.md` appendix updated.

### Phase 5. Dashboard, history, provisioners, security and audit

Includes the list envelope, the total-order fix, the nullable `total`, and D10's two aggregate rules applied to `dashboard` with the `source:` struct tags.

**The total-order fix touches `db/authlog.go` and `db/history.go`, which the live template routes still use**, so it can silently change results the existing HIST and SEC specs assert on. Confirm those still pass after the change, or rewrite them in the same PR.

**Exit criterion.** The tied-timestamp pagination test passes on `history`, `audit-events` and `security/events`. `acme/events` joins the same table in Phase 7. The aggregate `source:` test passes. Rewritten history, security, audit and dashboard specs pass. `Parity` table signed off.

### Phase 6. Admin

Users including the five-way split and the self-target invariants, temporary users, console with the narrowed DTO, about, integrity, backups with create-then-navigate, notifications as `PATCH` with `writeOnly` secrets, and the IPv6 round-trip for `DELETE /api/v1/security/ip-blocks/{ip}`.

Also closes the `/admin/integrity` cost question (Section 12) by measurement rather than by asking: benchmark `/admin/integrity` against a seeded install, and if it walks every certificate on disk, cache it for 60 seconds. Do not ship it uncached with an unknown cost.

**Exit criterion.** Self-target invariants enforced. The console response contains no substring of the database DSN. A notifications `PATCH` omitting `SMTPPassword` leaves it unchanged. `::1` unblocks end to end through the generated client. Rewritten admin specs pass. `Parity` table signed off.

### Phase 7. ACME

The `le` to `acme` rename at Section 6's stated scope, settings as `PATCH`, events with pagination, auto-renew folded into `PATCH` on the parent.

**Exit criterion.** `rg 'LECertificate|handlers/le' backend/` returns nothing, while `le_*` table names and `LE_*` environment variables are untouched. Settings `PATCH` preserves account material. Rewritten ACME specs pass. `Parity` table signed off.

### Phase 8. Profile and MFA

Profile, preferences, password change with reauth, and the full TOTP enrolment flow including the PNG QR operation, recovery codes, and `POST /api/v1/profile/mfa/disable` with its two-factor reauth.

**The QR renders as `<img src="blob:...">`, which the current CSP blocks.** Either pull the `img-src 'self' data: blob:` change forward from Phase 9 into this phase, which is the recommended option since it is one directive and nothing depends on it staying absent, or scope this phase's exit bar to API-response verification and defer the live render check. Choose explicitly rather than discovering it.

**Exit criterion.** Enrolment, confirm and disable all work including both reauth requirements. The QR renders in a browser (given the CSP change). Rewritten profile specs pass. `Parity` table signed off.

### Phase 9. Removal and hardening

**The only irreversible phase.**

1. Delete `templates/`, `static/js/`, `h.render`, `h.flash`, `h.base`, and `Handler.tmpls`. `templateFuncs` is retired rather than deleted: `badgeClass` became `expiryStatus`, `daysLeft` and time formatting became client-side, `hasRole` became `roleLevels` in `GET /api/v1/config`. Confirm each has a home first.
2. Delete, in one commit because Go will not compile otherwise: the template-route registrations; `//go:embed templates static` (`main.go:40-41`); `staticHandlerFromFS` (`:70-100`) and its `/static/*` registration (`:337`); `mimeByExt` (`:91-96`); the `init()` MIME block (`:101-116`); the `fs.Sub` block (`:332-336`); the five imports that exist only for those (`io`, `io/fs`, `mime`, `path/filepath`, `strings`); the duplicated CSS under `backend/static/`; the `COPY templates/` and `COPY static/` lines in the backend Dockerfile; nginx's legacy passthrough blocks from Phase 3 step 8; and the now-false comment at `middleware/middleware.go:62-64` explaining a `Cache-Control` override that no longer exists. Unused imports are a compile error, unused package-level identifiers redden `golangci-lint`, so this is one commit rather than a cleanup pass. The role golden table's count of operations whose `templateRouteRetired` is false must reach zero (R22).
3. Delete the three `backend` tool globs from `lint-meta.yml`'s `style` job, replaced by a `frontend/` lint step.
4. CSP in `nginx.conf` against the real built bundle: `connect-src 'self'`, `frame-src 'none'`, `worker-src 'none'`, `manifest-src 'self'`, and `img-src 'self' data: blob:` if not already pulled forward by Phase 8. Confirm `@vitejs/plugin-legacy` is not in use, since it emits inline scripts.
5. Re-floor the coverage gate, which the two-sided ratchet has been doing per phase already.
6. Retire `E2E-STATIC-01` rather than repathing it: it asserts `GET /static/css/pages.css` returns 200 with an enforced MIME type against a Go handler that no longer exists. D11's blast-radius row for `specs/config/static-01.api.spec.ts` covers the rename during the migration window only.
7. Update `README.md`, `Makefile` help, `scripts/test_deploy.sh` (which curls `/login` and two `/static/*` paths), and finalise the `plans/e2e-tests.md` appendix.

**Exit criterion.** Section 10's pass level met. Every acceptance criterion in Section 2 checked.

## 9. CI and CD

### 9.1 Job graph

```
ci.yml (push:main, pull_request)
├── build-test-lint        gofmt, vet, build, test -race, coverage gate, golangci-lint
├── db-integration         postgres service, ./db/...                    [parallel]
├── client                 [parallel, no needs; fetch-depth: 0]
│     setup-go   -> go run ./cmd/openapi -> git diff --exit-code         (the drift gate lives HERE)
│                -> oasdiff breaking vs the base branch
│     setup-node -> npm ci (clients/ts) -> generate -> tsc -> npm pack
│                -> upload artifact ts-client
│                -> publish   [ONLY on refs/tags/v*]
├── frontend               needs: client
│     npm ci (frontend)
│     -> download ts-client -> npm i --no-save -> ASSERT version contains the short sha
│     -> lint, typecheck, test, build
├── contract-negative      workflow_dispatch, and on changes to ci.yml / clients/ts/** / cmd/openapi/**
│                          re-runs the two-commit stale-client proof. NOT a required check.
└── ci-gate                needs: [build-test-lint, db-integration, client, frontend]
                           if: always(); asserts every result == success

e2e.yml
├── image                  two builds, backend and frontend
│                          docker save -> upload artifact
├── e2e-main               needs: image; docker load (not rebuild-from-cache)
└── e2e-gate               needs: e2e-main
```

`client` deliberately does not need `build-test-lint`, because it runs its own gate. It is one job doing both `setup-go` and `setup-node` on purpose: 7.2's whole argument requires the gate and the generation to be inseparable. It therefore has no `defaults.run.working-directory`, since Go steps run in `backend/` and Node steps in `clients/ts/`, and every step names its own. The two toolchain caches key independently and do not collide.

`ci-gate` cannot be a copy of the existing `e2e-gate`, which has one dependency. With four, and `if: always()`, it needs an explicit loop:

```yaml
ci-gate:
  needs: [build-test-lint, db-integration, client, frontend]
  if: always()
  runs-on: ubuntu-latest
  steps:
    - run: |
        for r in "${{ needs.build-test-lint.result }}" "${{ needs.db-integration.result }}" \
                 "${{ needs.client.result }}" "${{ needs.frontend.result }}"; do
          [ "$r" = success ] || { echo "::error::a required job did not succeed ($r)"; exit 1; }
        done
```

**Do not add `paths:` filters to `ci.yml` for cost.** A Go-only pull request would skip `frontend`, `needs: client` would leave it skipped, and GitHub reports a skipped required check as neutral-passing. `ci-gate` exists for that reason and mirrors the `e2e-gate` pattern already in the repository.

**Wasted wall clock, in order of size.**

1. **The same image is built three times per pull request today**, and there are now two images: `e2e.yml`'s `image` job, `docker-build.yml` (unscoped `type=gha`, a separate cache), and `security.yml`'s `trivy-image` (no cache, `continue-on-error`). Point the latter two at the `e2e` scopes, or on pull requests have `trivy-image` scan the images `e2e.yml` already produced. Two images is a net win here rather than a cost: a frontend-only change no longer touches the Go image at all, and vice versa, which the single-image design could not express.
2. **`e2e-main` re-materialises images from the layer cache** rather than receiving them. `docker save | gzip` plus `docker load` is deterministic and immune to a cache miss silently rebuilding a different image than the one tested. **Scope this to the two application images only.** The harness image is a Playwright base with a baked Chromium and `node_modules`, routinely over a gigabyte, and it changes rarely, so saving and uploading it every run is the actual waste. Leave it on `cache-from`/`cache-to`, which handles large stable layers well. Both application images stay small, since neither runtime stage contains Node.
3. **Cache scope splitting now works, because there are two invocations.** Separate `cache-from`/`cache-to` scopes cannot be expressed per stage within one `docker/build-push-action` call, which is why the earlier single-image design could not do it. With `frontend` and `backend` built separately they take `scope=e2e-frontend` and `scope=e2e-backend`, and a change to one does not evict the other's cache.

**The SPA build stays inside the frontend image build**, and is additionally run in the `frontend` CI job as a bundle-validity check. Moving it out and feeding an artifact only works inside CI, would need plumbing into three workflows, and would make the released image's SPA come from a toolchain invocation nobody can reproduce locally.

### 9.2 Workflow changes

| Workflow | Change |
|---|---|
| `ci.yml` | add the `client`, `frontend` and `ci-gate` jobs per 9.1. `build-test-lint` gains nothing except D11's paths |
| `e2e.yml` | two application builds instead of one: `backend` at `context: ./backend`, `frontend` at `context: .`. The harness build at `context: ./test/e2e` is unaffected. `docker save`/`load` for both. **`BASE_URL` moves to the nginx service**, and every helper that assumed the UI and the API share a container needs checking |
| `docker-build.yml` | two images to build, tag and push, on `scope=e2e-frontend` and `scope=e2e-backend` |
| `codeql.yml` | D11 paths |
| `security.yml` | D11 paths at four sites, **two** `trivy-image` scans (the nginx base is a new third-party image to keep patched), and `npm audit` or equivalent for `frontend` and `clients/ts` |
| `lint-meta.yml` | no workflow-level path filters exist. The `style` job's stylelint, eslint and djlint globs and the `hadolint` job's `dockerfile:` path are hardcoded. The three style globs are deleted in Phase 9 and replaced by a `frontend/` lint step |
| `dependabot.yml` | currently three ecosystems and **no npm**. Add `/frontend`, `/clients/ts` and `/test/e2e` (uncovered today). Update the Go and Docker directories for D11. Exclude `huma/v2` and `humachi` from the `go-minor-patch` group. Read from the default branch only, so it takes effect after the rename merges |
| `.pre-commit-config.yaml` | D11 paths, the drift check scoped to `api/` and `apitypes/`, a frontend lint hook, and `hadolint` gaining the second Dockerfile |
| `.golangci.yml` | **`depguard` is not in the `enable` list today.** Add it, plus a `settings.depguard.rules` block with `files: ["**/api/**", "**/apitypes/**"]` and the allowlist form from Section 2. `gomodguard` likewise, for the CORS ban. `gofumpt`'s `module-path: step-ui` is correct as-is, since D11 leaves the module path alone |

### 9.3 Rollback and recovery

- **A bad client package.** With tag-only publication no consumer resolves by version, so the blast radius is zero and the fix is forward. If a bad version did reach the registry: never unpublish, publish a higher version, `npm deprecate` the bad one.
- **Spec and code diverged on `main`.** Only reachable through 7.2's clean-merge-of-a-shared-schema path or a branch-protection bypass. There is no state to migrate, so recovery is `make openapi` on a fix-forward pull request. Prevention is the required up-to-date-branch rule.
- **A bad image on `main`.** No live deployments exist, so recovery is reverting the merge commit. That is the reason this section is short.

---

## 10. Impact on the e2e suite

**The e2e agent was stopped on 2026-08-13.** The suite is frozen at the state below and no further specs are being written against the interface this plan deletes.

**Current state** (`plans/e2e-implementation-status.md` at `a549750`): harness phases 0 to 2 complete. Phase 3 has landed **17 spec files covering 34 of the 78 indexed test IDs**, all in the `api` project: AUTH-01, -11, -12, -14, -15, RBAC-01/02, CSRF-01/05, CERT-01 to -04 and -09, PROV-01/02, HIST-01 to -03, SEC-01/02, ADM-01 to -05, BAK-01, HLTH-01 to -06, STATIC-01. **138 passing, 8 skipped.**

**What survives**

| Suite | Fate |
|---|---|
| `infra` project (E2E-BOOT-01 to -09) | **Untouched.** Bootstrap, `UI_TLS_MODE`, root provisioning, deliberate fatals and the renewal goroutine are below the HTTP layer. Not yet written |
| Health (E2E-HLTH-01 to -06) | **Untouched.** `/health` and `/ready` keep their paths and payloads. Already written. They live in the `api` project despite testing infrastructure |
| `api` project, the other 28 written IDs | **Rework.** Path, method, encoding, status code and assertion shape all change. Intent survives, body does not |
| `ui` project (4 browser companions) | **Rewrite.** Not yet written, which is the good case |
| Helpers | `compose.ts` (which also provides `psql` and `psqlRows` at `:75,81`), `env.ts`, `openssl.ts`, `poll.ts`, `qr.ts`, `totp.ts`, `envfile.ts` survive. `flash.ts` is deleted. `session.ts`, `routes.ts` and `certs.ts` are rewritten, `certs.ts` because it builds issuance payloads against the form contract |

6 of the 34 written IDs survive untouched, 28 need their bodies rewritten, and the largest untouched block, the 9 `infra` IDs, is not written yet. The rewrite is a body rewrite, not a re-derivation: `plans/e2e-tests.md` already states what each test asserts.

**What the two-container topology breaks in the harness, beyond the contract rewrite.** This is a separate axis from the 28 contract-bound specs and it lands in Phase 3, not per domain.

| Site | Breaks because |
|---|---|
| `helpers/env.ts:8`, `playwright.config.ts:6`, `e2e.yml:131` | three independent `BASE_URL` definitions, all defaulting to `https://step-ui:8443`, which now bypasses the origin under test |
| `helpers/env.ts:11` | `HOST_URL` builds on `UI_HTTPS_PORT`, now published by `step-ui-web` |
| `playwright.config.ts:38-42` | the `ignoreHTTPSErrors` comment about SAN mismatch now applies to nginx's listener as well as to `proxy_ssl_verify` |
| `test/e2e/Dockerfile:1-4` | states the harness runs in-network so rate limiters see distinct client IPs. nginx recreates the single-address condition unless D5 rules 1 to 3 hold |
| `helpers/session.ts:21-33,36-54,75-79` | scrapes `csrf_token` from HTML, posts form-encoded to `/login`, expects `302`, scrapes `/dashboard`. All four assumptions go |
| `helpers/session.ts:82` | `SESSION_COOKIE_NAME = "step-ui"` becomes `__Host-step-ui` under `SESSION_SECURE`, which is the compose default |
| `helpers/flash.ts` | flash text and the plain-text `403 Forbidden` body both disappear |
| `helpers/routes.ts:17-25` | reads `step-ui-go/main.go` by path and regexes `r.Post(...)`. Post-Phase-1 the routes are `huma.Register`, so it derives zero and throws |
| `helpers/compose.ts:113,116,123,184-195` | log capture and `restartUI` are hardcoded to the `step-ui` service. A backend restart now also disturbs nginx |
| `collect.sh:61` | the service list omits `step-ui-web`, so nginx logs are never collected on failure |
| `specs/config/static-01.api.spec.ts` | retired in Phase 9, not repathed. It tests a Go handler that ceases to exist |
| `specs/health/hlth.api.spec.ts:63,176` | `waitHealthy("step-ui")` never returns while the compose healthcheck curls `/login` |
| `specs/provisioners/prov.api.spec.ts:37,65` | asserts on `services["step-ui"].environment` in the merged compose config, which changes |
| `specs/auth/auth-sessions.api.spec.ts:48,77,104` | asserts `Set-Cookie` contains `step-ui=` |
| `compose.e2e-image.yml:7` | pins the prebuilt image on `step-ui` only, with no equivalent for the nginx image |
| `compose.e2e-fatals.yml:7` | E2E-BOOT-07 deliberately kills `step-ui`; `step-ui-web` then crash-loops against a dead upstream unless its healthcheck follows D5 rule 7 |
| `compose.e2e-nodeps.yml:7` | exists to remove `step-ui`'s `depends_on`; `step-ui-web`'s new dependency reintroduces one |
| `compose.e2e-fingerprint.yml:13-19` | `!override`s the volume list and relocates the root CA, so nginx's trust anchor has no home in that scenario |

Not e2e but the same class, and absent from an earlier draft's blast radius: `scripts/test_deploy.sh:119,129,132,135,138`, which curls `/login`, greps for a server-rendered `<form>` and fetches two `/static/*` assets.

**Resumption order**

1. **Resume on work immune to this plan**: the `infra` project, the nightly `renew` leg, the harness scripts. None touches the HTTP contract, and it can run in parallel with Phase 0 **on `main`**, which is why D11's rename lands first, ahead of every other phase. Note the rename breaks `test/e2e/helpers/routes.ts` and `specs/config/static-01.api.spec.ts`, both loudly, and both are fixed in the rename commit.
2. **New `api` specs stay frozen** for the domains this plan touches until Phase 1 lands. The 28 contract-bound IDs are rewritten in the phase that migrates their domain, not speculatively.
3. **After Phase 2 the suite gains a better tool than it has today.** The generated client can be installed into `test/e2e` and used to drive `api` specs, giving compile-time protection against contract drift. Two mechanics to solve when that happens: the harness runs from `/e2e` inside `step-ca-ui-harness:e2e` with baked `node_modules`, so the branch's tarball must reach that image's build context and the `e2e-harness` cache scope must key on it, or the baked client goes stale silently. And that consumer needs *this branch's* client, never a published one, which is a second reason D8 publishes on tags only.
4. **`ui` specs are written last**, after Phase 8, once the markup is stable.
5. `plans/e2e-tests.md` gains an **appendix**, not a rewrite, recording which test IDs changed contract and their new shape. Append-only, following this repository's convention, updated by each of Phases 4 to 8 rather than once at the end.

**Pass level, which Section 2's final criterion cites.** Phase 9 does not merge until all of the following hold. The 6 untouched IDs (HLTH-01 to -06) pass unmodified. All 28 contract-bound IDs are rewritten and passing. The 9 `infra` IDs are written and passing. The 4 `ui` browser companions and the CSP smoke spec are written and passing. Skips number 8 or fewer and each carries a linked issue. And the whole run executes against the two images built in the same workflow run.

---

## 11. Risk register

Each risk names a **trigger**, an **owner phase** and a **status**. Status is the thing that keeps a register readable across a ten-phase migration: `discharged at Phase N` entries are struck from the reading list rather than re-read forever, and a reader at Phase 5 should only need the `live` ones.

**Discharged at Phase 0's exit** and not worth re-reading afterwards: R4 and R5 (spec purity and determinism), R6 (the CSRF bootstrap, whose residual is 5.4's concurrency rule and already has a test), R9 (binary downloads, proven against a placeholder resource), R12 (the huma handler signature). **Parked, not live:** R3 (no tag is planned), R20 (withdrawn by removing the branch), R24 (a statement of scope, not a risk). **The eight that are the register**, and that a reader short of time should read instead of all of it: R13, R15, R16, R17, R18, R22, R25, R27.

**R1. Collision with the e2e work.** Stopped with 34 of 78 IDs written across 17 files, not the six an earlier count claimed. Six survive untouched, 28 need rewriting, and Phases 4 to 8 each now carry an explicit rewrite step, which revision 1 left assigned to nobody. *Trigger:* any new file under `test/e2e/specs/` outside `infra/` before Phase 1 lands. *Owner:* Phases 4 to 8.

**R2. `@hey-api/openapi-ts` is pre-1.0, and its escape hatch expires.** Pin exactly, never a range. Generator bumps arrive as their own pull request with the generated diff reviewed. The `openapi-typescript` plus `openapi-fetch` fallback is cheap only until Phase 4, because it removes the generated query layer and every call site built on it. **After Phase 4 the fallback is a frontend rewrite, and the real response to a broken generator is to pin, patch and fix forward.** *Trigger:* a dependabot bump whose generated diff is not reviewable in one sitting. *Owner:* Phase 2.

**R3. GitHub Packages token trap.** A package first created with a personal access token permanently denies `GITHUB_TOKEN`. Mitigated by tag-only publication from CI. *Trigger:* the first `v*` tag. **Not on this project's critical path**, since no tag is planned in Phases 0 to 9 and Q1 has yet to surface an external consumer.

**R4. Spec determinism. Marshalling discharged, configuration not.** Huma's maps are key-sorted by `encoding/json`, so the canonicalising-re-marshal contingency stays withdrawn, and anonymous-schema suffixing is fixed by explicit sequential registration. **The live half is D3 rule 1, which in revision 1 was a sentence with no enforcement.** A provisioner list that becomes an `enum`, a duration bound that becomes a `maximum`, a template list that becomes a `default`, and the spec is environment-dependent and the gate fails per machine. Mitigation is the two-environment double generation, which turns the rule into a test. *Trigger:* the drift gate failing locally and passing in CI, or the reverse. *Owner:* Phase 0 step 7.

**R5. `cmd/openapi` reaching a dependency at runtime. Downgraded after tracing, now guarded rather than trusted.** The blocker set is empty today (D3), and revision 1's `Service` interface is withdrawn as an invented solution to a non-problem. The residual is a future `init()` or side-effecting package-level var in `handlers` or anything it imports, which would appear as a hang or a silent success rather than an error. Mitigation: the gate runs with `DATABASE_URL` and the CA URL pointed at `127.0.0.1:1`. *Trigger:* the drift gate taking longer than a second, or failing only on a machine with a database running. *Owner:* Phase 0.

**R6. CSRF bootstrap. Largely dissolved.** Making `GET /api/v1/session` return `200` with a discriminator removed the anonymous-401-that-must-still-set-a-cookie sequence, whose failure mode was permanent lockout. What remains is 5.4's concurrency rule, enforced by a `retry: false` unit assertion and a cold-jar e2e. *Trigger:* an intermittent 403 on login. *Owner:* Phase 0 step 5, Phase 3 step 6.

**R7. `SameSite=Strict` and OIDC.** Handled by the separate `Lax` cookie carrying state, nonce and verifier, and by the SPA rescuing `Strict` on the callback landing. Note 4.4: OIDC does not traverse the Vite proxy, so this is only ever exercised under compose and no e2e ID currently covers it. Reverting to `Lax` on the session cookie is the status quo and costs nothing else. *Trigger:* the first OIDC login attempt after Phase 1. *Owner:* Phase 1 step 4.

**R8. Coverage, and the silent case.** The visible failure is a red gate. **The failure that matters is invisible: coverage falls from 60 to 20 and the gate stays green because the floor is 15.** Mitigation is Section 2's two-sided gate, which fails when measured coverage exceeds the floor by more than 5 points, so the floor ratchets without a human deciding to raise it. *Trigger:* any phase PR whose coverage step passes with no floor change. *Owner:* every phase.

**R9. Binary downloads through a generated client.** Blob handling, filename derivation and the backup flow are the awkward corner of every generated client. Proven in Phase 0 against a placeholder resource, not discovered in Phase 4. A hand-written download wrapper in `src/api/` is expected, and lives there as one reviewed exception rather than a pattern. *Trigger:* Phase 0 step 6. *Owner:* Phase 0, Phase 4.

**R10. Bundle size and the CSP.** `style-src 'self'` is a hard functional constraint on any library injecting a `<style>` tag, not merely a size concern. *Trigger:* the CSP smoke spec, which is what converts this from "someone notices a broken page" into a failing check. *Owner:* Phase 3.

**R11. Docker build context widening.** Without `.dockerignore`, five `node_modules` trees, `backups/`, `secrets/` and `.env` enter the context: the first destroys caching and the last three would bake credentials into an immutable image layer. *Trigger:* a build-context size assertion in CI, so it is not "CI feels slow". *Owner:* Phase 3 step 8.

**R12. The huma handler signature versus `gorilla/sessions`.** A handler receives only a `context.Context` while every cookie and session write needs `(r, w)`. All of 5.3 and 5.4 depends on the unwrap middleware. *Trigger:* Phase 0 step 3, before anything is built on it. If it fails, `oapi-codegen` is the better answer and Phase 0's exit criterion allows it. *Owner:* Phase 0.

**R13. The certificate lifecycle now crosses a container boundary, in three ways rather than one.** nginx reads its certificate once at start, so a renewal is invisible until reload. The reload cannot be issued from the Go container without the Docker socket, which is host root held by the container that runs an allowlisted shell console, so it is a prohibition rather than an inconvenience. The write is two non-atomic `os.WriteFile` calls, so a naive watcher can catch a torn PEM. And the self-signed fallback, which exists to keep the UI up when the CA is down, now guarantees the opposite under `proxy_ssl_verify on`, with both healthchecks green and every API call 502ing. Stale-after-renewal was the only part of this an earlier draft saw. Mitigation: D5 rules 6 and 9 in full, plus atomic rename at the source. *Trigger:* the first renewal after Phase 3, any expiry alarm, and any `docker compose ps` that is all green with a dead UI. *Owner:* Phase 3 steps 7 and 10.

**R14. The consumption mechanism is the whole design and is unproven.** If the frontend job can install a stale client and go green, every guarantee here is decorative. D8's three defences each close one path. Residual is decay: the proof is one-off and the `npm ci` ordering can be innocently refactored away, so the workflow block carries a CODEOWNERS entry and the `contract-negative` job re-runs the proof. *Trigger:* the `contract-negative` job going green when it should be red. *Owner:* Phase 2 step 4.

**R15. Response DTOs leaking model fields.** `models/models.go` has no `json` tags and carries `PasswordHash`, `TOTPSecret`, `TOTPPendingSecret`, `RecoveryCodes` and `SMTPPassword`. Huma reflects over whatever struct a handler returns, so `Body *models.User` would publish a password hash and a TOTP secret to any caller who can read a user. Today only templates naming fields one at a time prevents that, and that protection is being deleted. Three defences: the `depguard` **allowlist** (a denylist is defeated by an aliasing package), the widened property-name test, and the drift gate making any new property a reviewable diff. **This remains the single most likely way this migration ships a real vulnerability.** *Trigger:* the property-name test, and any spec diff adding a property to a response schema. *Owner:* Phase 1 step 1.

**R16. Fail-open authorisation, and invisible role changes.** The middleware denies outright, the `auth` vocabulary is exhaustive, and the role golden table fails the build. Residual: a role change is invisible to TypeScript, producing a green build, a runtime 403 and a page still rendering the link. **`x-required-role` reaches the spec, so the residual is detectable**: the `client` job diffs the `(path, method, x-required-role)` map between base and head and fails when it changed unless the same pull request also touches `test/e2e/specs/rbac/`. *Trigger:* that diff being non-empty. *Owner:* Phase 1 step 5.

**R17. The plan has one holder.** Every rationale in D1 to D12 is written down, but the sequencing judgement and the meaning of "done" per phase are not. A three-week absence at Phase 5 leaves a half-migrated tree, two authorisation paths, and a document nobody else has read end to end. Mitigation: every phase PR names the D-numbers and criteria it discharges, Phase 0's exit checklist is a committed file rather than a review comment, and each phase's `Parity` table is the handover artifact. *Trigger:* any phase with no commit for ten days. *Owner:* every phase.

**R18. The migration stops half-finished and stays there.** There is no deadline, which the owner has confirmed, so schedule pressure is not the risk. The risk is its opposite: with nothing forcing a finish and a coexistence state that works, a partly-migrated tree can become the permanent state by default rather than by decision. **That state is stable but not free**: two UIs, two authorisation implementations, two session-validation call sites, and a suite covering both, indefinitely. Mitigation: name Phase 4 as a legitimate end state so stopping is a decision (Section 8), give each phase a rough size at its start, record the actual at its merge, and treat R22's `templateRouteRetired` count failing to fall as the signal. *Trigger:* any phase open longer than the two before it combined, or two consecutive phases with no change in the retired count. *Owner:* every phase.

**R19. The generated client's ergonomics are rejected in practice.** If hey-api is awkward around downloads, multipart or the `Session` discriminator, the cheapest local fix is a hand-written `fetch`, and the first one is invisible in review. Every such call silently deletes a contract guarantee, and R14's whole mechanism becomes decorative for exactly the operations that were hard enough to matter. Mitigation: the ESLint `fetch` ban, landed in Phase 3 **before any domain phase writes a call site**. *Trigger:* a pull request adding an eslint-disable for that rule. *Owner:* Phase 3 step 1.

**R20. The branch model. Mitigated by removal.** A long-lived `feat/spa` would have taken nine phases of divergence against a `main` receiving e2e work, dependabot across three new npm ecosystems, and hotfixes. D11 moved the rename to `main` for exactly that reason, which is evidence the mechanism is real, not evidence it stops at the rename. Section 8 now lands Phases 0 to 8 on `main`, which is safe because they are additive by construction. *Trigger:* not applicable while no long-lived branch exists. Reinstate this risk the moment one is proposed.

**R21. The CSS port is a rewrite, and D7 and D12.8 already disagree about it.** D7 says the CSS moves "mostly unchanged". D12.8 says `routes/` does not mirror templates and the shared primitives are *extracted* from `base.html` and `admin_base.html` rather than reproduced. CSS written against a Go template's DOM does not survive a component tree that deliberately differs, and the cost lands not in Phase 3 but in Phases 4 to 8, one page at a time. Mitigation: Phase 3's side-by-side exit criterion, and taking the keep-or-replace decision once. *Trigger:* Phase 3 step 3 producing more selector edits than moved files. *Owner:* Phase 3.

**R22. Two authorisation implementations coexist for eight phases.** `mw.RequireRole` on chi groups and the huma role middleware both live until Phase 9. A capability migrated to `/api/v1` while its template route stays registered is a second, older path to the same capability under the older rules, and Section 6's role corrections (revocation and the three CA downloads becoming admin) mean the two paths **deliberately disagree**. Mitigation: the golden table's `templateRouteRetired` column, whose false count the role-matrix test reports on every run, reaching zero as a Phase 9 criterion. *Trigger:* that count rising, or failing to fall across a phase. *Owner:* Phases 4 to 9.

**R23. `handlers/` becoming a service layer is unpriced.** 4.1 says `handlers/` is kept and becomes the service layer, and 5.5 says handlers recover `(r, w)` and call existing methods unchanged. Both are true for session and cookie work. Neither is true for the handlers that render, which is most of them: today's functions compute and write in the same body, so every migrated operation needs its logic separated from its response writing first. That work is invisible in an endpoint map, which counts routes rather than function bodies, and it is concentrated in the largest phases. Mitigation: Phase 4's first-three-operations rule, with the observed cost applied to Phases 5 to 8. *Trigger:* the first handler whose logic cannot be extracted without touching `h.render`'s data map. *Owner:* Phase 4.

**R24. Scope.** 33 templates, 65 routes, 46 handler files, two container images, six workflows plus `dependabot.yml`, and the entire test suite. Listed for honesty rather than as a risk with a mitigation: it is a fact about the work, and the controls for it are R17, R18 and Phase 0's off-ramp.

**R25. The proxy layer is a second place to get identity wrong, and the first attempt at it was wrong.** The CRITICAL finding: with `X-Forwarded-For` set to `$remote_addr`, `clientFromHeaders` always exhausts its XFF loop and **falls through to `X-Real-IP` and `True-Client-IP`** (`middleware/realip.go:12,71-74`), so an unauthenticated caller controls the login rate limiter, `auth_log.ip`, `users.last_ip` and the security log on every request. Three further silent failures in the same layer: `TrustProxy` left off collapses every client to one address; `proxy_set_header` inherits all-or-nothing per level, so a legacy block without the include takes raw client headers; and a `location` falling through `try_files` for an API path returns `200 text/html` where the client expects a problem document. Mitigation: D5 rules 1 to 4, the shared include file, deleting the Go-side fallback, and Section 2's four-header forgery table test, which tests behaviour through the origin rather than configuration text. *Trigger:* any edit to `nginx.conf` or to `realip.go`. *Owner:* Phase 3 steps 7 to 10.

**R26. The routing table is written twice.** `nginx.conf`'s `location` blocks and `vite.config.ts`'s proxy rules are the same list of prefixes, and a divergence is a bug that reproduces in only one environment. Adding an API prefix and forgetting the dev proxy means it works in compose and 404s locally, which reads as a broken dev setup rather than a missing route. Mitigation: 4.4's single source with both generated or linted from it. *Trigger:* any new top-level path prefix. *Owner:* Phase 3 step 7.

**R27. The migration window is the dangerous part of the two-container split, not the end state.** From the moment nginx takes the published port until Phase 9 the SPA lives under `/app/`, nginx proxies everything else to the backend, `backend/static/` holds a duplicate of the CSS that also lives in `frontend/src/styles/`, the legacy routes are served with Go's CSP and no HSTS at all, and every temporary proxy block must carry the header include or `POST /login` takes raw client headers. All of it is scaffolding a reviewer can mistake for the design. Mitigation: every temporary block comments Phase 9 as its deletion point, Phase 9 step 2 deletes all of it in one commit, and the base-path flip is a single Vite and router setting rather than a routing-table rewrite. *Trigger:* a temporary block surviving a phase, the duplicated CSS diverging, or a proxy block appearing without the include. *Owner:* Phase 3 step 8, Phase 9 step 2.

**R28. The e2e harness is coupled to the container topology in more places than the routing.** `BASE_URL` has three independent definitions (`helpers/env.ts:8`, `playwright.config.ts:6`, `e2e.yml:131`). `helpers/session.ts` scrapes `name="csrf_token"` out of rendered HTML and expects a `302` from `/login`, which the SPA shell breaks in a way that reads as a fixture bug. `collect.sh:61` enumerates services by name and would silently never collect nginx logs, leaving the new origin as the one component with no diagnostics on failure. And `test/e2e/Dockerfile:1-4` documents that the harness runs inside the network precisely because "both rate limiters key on the client IP, so a host harness is one gateway address and per-test rate-limit isolation becomes impossible" — nginx recreates that condition *inside* the network, so per-test isolation survives only if D5's rules 1 to 3 all hold. Mitigation: Section 10's coupling table, worked through in Phase 3 rather than discovered in Phase 4. *Trigger:* any e2e failure that looks like a fixture bug rather than an assertion failure. *Owner:* Phase 3.

**R30. nginx becomes a policy layer by accretion.** The config already hand-writes an RFC 9457 document, sets a rate policy, decides by regex which paths are the API, and maps upstream failure to a status the SPA branches on. All of that is behaviour, in a file with no type checker, no unit-test reflex and two likely readers. The next request that is easier in nginx than in Go (a redirect, a header for one caller, an allowlist, a maintenance page) lands there, and the security perimeter becomes the business-logic layer without anyone deciding it should. *Mitigation:* `make nginx-lint` exists before Phase 4 rather than after, and the config carries a rule at the top naming what may live there (routing, TLS, headers, caching, limits) and what may not (any decision depending on a user, a role or a resource). *Trigger:* any diff adding a `map`, an `if`, a `set` beyond `$backend`, or a second `return` with a body. *Owner:* Phase 3b. *Status:* live from Phase 3b.

**R31. The base-path flip is a URL-surface change, not a Vite setting.** While the SPA lives under `/app/`, the reset link `handlers/password_reset.go:112` emits, the OIDC redirect URI registered with the issuer, and every bookmark point at one path shape, and Phase 9 moves it. An email in flight and a bookmark are recoverable. **A redirect URI registered with a corporate IdP is not fixable from this repository.** *Mitigation:* decided in Phase 3c, not at Phase 9: `/reset-password` and the OIDC callback landing stay at the root for the whole window, so the flip never moves a URL that left the building. *Trigger:* any phase PR setting `PUBLIC_BASE_URL` or `OIDC_REDIRECT_URL` to a value containing `/app`. *Owner:* Phase 3c. *Status:* live from Phase 3c to Phase 9.

**R32. The web leaf fails silently where the backend leaf fails loudly.** A backend leaf that cannot renew is visible: the healthcheck fails, `.fallback` appears, `depends_on` holds nginx off. A web leaf that cannot renew is invisible: nginx already loaded the file, keeps serving it, and the watcher's own failure path is to log and re-arm. Nothing polls the expiry. The first symptom is every browser refusing the origin on a day nobody deployed, with two healthy containers. The asymmetry exists only because D5 rule 6 was written for the backend leaf and the web leaf inherited the happy path. *Mitigation:* `/ready` reports `notAfter` for both and goes non-200 inside the renewal window, and the watcher logs a distinct line on `nginx -t` failure that `collect.sh` picks up. *Trigger:* a renewal cycle with no reload line, or a `notAfter` that did not move across two cycles. *Owner:* Phase 3b. *Status:* live.

**R33. The backend port gets published for debugging and stays published.** `step-ui` stops publishing a host port in Phase 3c, and the first time someone needs to curl the API without the origin in the way, `ports:` goes back into a compose file. It then survives, because nothing breaks. What breaks is invisible: a second reachable path with no `limit_req`, no HSTS, no body ceiling, and a peer that is not in `TRUSTED_PROXY_CIDRS`, so the same request records a different client identity depending which way it arrived. *Mitigation:* a `yq` assertion over every compose file, in CI and in `make preview`. Debugging goes through `docker compose exec`. *Trigger:* that assertion. *Owner:* Phase 3c. *Status:* live.

**R34. "Stop after Phase 4" and R27 contradict each other, and nobody has noticed.** 1.5 and Section 8 name completing Phase 4 a legitimate end state. R27 calls the `/app/` base path, the passthrough blocks and the duplicated CSS scaffolding a reviewer can mistake for the design. Both cannot hold: stopping at Phase 4 makes the scaffolding permanent, with two UIs, a SPA at a path that is now in bookmarks, and two authorisation implementations that Section 6 makes **deliberately disagree** on revocation and the CA downloads. *Mitigation:* if Phase 4 is a real stopping point then it must also retire the template routes for the migrated domain and flip the base path, which makes it Phase 4 plus a partial Phase 9 rather than a clean stop. Say which, before anyone reaches Phase 4 needing the answer. *Trigger:* any decision to stop before Phase 9, or two consecutive phases with no change in the `templateRouteRetired` count. *Owner:* the repository owner. *Status:* live.

**R29. Enabling upstream keepalive reopens request smuggling.** Desync between nginx and Go is currently unreachable, and for a reason that is an accident of D5 rule 7 rather than a control: a variable `proxy_pass` cannot use a named `upstream`, so there is no connection reuse, and reuse is the precondition for poisoning a subsequent victim request. `proxy_http_version` also defaults to 1.0, so no `Transfer-Encoding` reaches Go, and `proxy_request_buffering` is on. Anyone who later adds an `upstream` block with `keepalive` for performance removes the accident without noticing. Mitigation: a comment in `nginx.conf` saying so, next to the `resolver`. *Trigger:* any pull request adding `upstream` or `proxy_http_version 1.1`. *Owner:* Phase 3 step 7.

---

## 12. Open questions

**All three questions this plan carried were answered on 2026-08-13.** Nothing blocks Phase 0. They are recorded here with what the answer changed, because each one moved a decision.

**Answered: what did "not very scalable" mean, and is a second API consumer in scope?** No second consumer for now, **but the API is to be versioned anyway**. That is the right call and it is not the same as "no consumer, so no version": versioning now costs a path segment and a policy, while retrofitting one after a consumer exists costs a migration. 5.1 turns the segment from a hedge into a policy with an enforcement mechanism (`oasdiff` in the `client` job) and states what would have to be built if `v2` ever ships. 1.3's non-goal about a public API surface stands for now, and is the one most likely to be revisited.

**Answered: is one deployable acceptable?** No. **Separate means two containers.** D1 and D5 are rewritten around that: a Go image with no static assets, an nginx image carrying the SPA, and nginx as the single public origin doing path-based routing. The critical consequence, spelled out in D1, is that a *deployment* split is not an *origin* split. Because nginx proxies `/api/`, `/auth/`, `/health`, `/ready` and `/openapi.json` to the backend, the browser still sees one origin, so `SameSite=Strict`, the `__Host-` prefixes, the session-bound CSRF and the no-CORS rule all survive unchanged. Splitting to two real origins instead would have cost all four, and this plan treats that as a non-goal rather than an option. The split also makes the plan **smaller** in one place, since `//go:embed static`, the asset handler and the MIME table all leave the Go side, and **larger** in three, which are R13, R25 and R26.

**Answered: appetite and deadline?** None. It is ready when it is ready. That removes schedule pressure and replaces it with the drift risk R18 now describes: with nothing forcing a finish, the coexistence state can become permanent by default. Section 8 answers that by naming Phase 4 completion an explicit end state rather than a stall, so stopping is a decision and not an outcome.

**Reopened by the two-container answer.** Three of these did not exist before the split and one is a contradiction the plan created about itself.

**Q3. What is the deployment target beyond `docker compose`?** Every runtime mechanism this revision introduced is compose-specific: `depends_on: service_healthy`, static addresses on a pinned subnet, `TRUSTED_PROXY_CIDRS` as a single `/32`, `resolver 127.0.0.11`, a read-write and read-only shared volume carrying a certificate between two containers, and an entrypoint polling the filesystem for a file another container writes. **Not one of those survives a move to Kubernetes, ECS or Nomad**, where the peer is a node or an ingress, `depends_on` does not exist, cross-pod read-write volumes are a smell, and certificates arrive from a secret store. Combined with 1.1's statement that there are no live deployments, the plan is building an elaborate compose-coupled certificate-distribution mechanism for a system nobody currently runs. If the answer is "compose on one host, forever", D5 is right and this closes. If it is "an orchestrator eventually", three decisions are being made in a form that has to be redone, and the cheaper shape today is one certificate mounted from outside and nginx trusting a name rather than an address. **Gates D5, not Phase 0, so it can be answered by Phase 3b.**

**Q4. If the version segment can never bump, what is the MINOR bump for?** 5.1's trigger is a consumer this repository does not build in the same run, and 1.3, D8 and D1 together make that a new architectural decision rather than a drift. So `v1` is permanent by construction, and `oasdiff breaking`'s only remedy is editing an integer no resolver reads, which is the exact failure class D8 removed from PATCH. Should the gate instead demand something a human sees, a pull-request label or a line in a `docs/contract-changes.md` naming the operation and the reason? And does the role golden table's `version` column earn its place today, given its stated purpose is letting a `v1` and a `v2` operation carry different roles?

**Q5. Is there an operator, and who?** 1.1 says there are none. The two-container design creates the role anyway: `WEB_TLS_MODE`, `WEB_HOSTNAME`, `TRUSTED_PROXY_CIDRS`, `IMAGE_TAG` and per-half rollback, HSTS enablement, and a subnet that must not collide with a corporate VPN. Every one is an operational decision with a failure mode this document describes and no owner. If the answer is "the repository owner, on a laptop", several D5 rules are over-built. If it is "someone else, later", the plan owes an operating document that no phase currently produces, and R30's triage table is where it starts.

**Q6. Is Phase 4 a real stopping point, or not?** R34 states the contradiction: 1.5 and Section 8 offer it as a legitimate end state while R27 calls everything that makes it possible temporary scaffolding. Answering this changes what Phase 4 contains, so it is needed before Phase 4 rather than at it.

**Closed earlier, recorded so nobody reopens them.**

- **E2E sequencing.** The agent was stopped. Section 10 states the resumption order and the pass level.
- **Reset-token placement.** `POST /api/v1/password-reset/validate` with the token in the body, not a path segment.
- **Duration encoding.** Integer seconds (5.8). `UI_CERT_DURATION` is a Go duration string in config and becomes an integer in `GET /api/v1/certificates/options`.
- **`/admin/integrity` cost.** A Phase 6 task with a decision rule, because it is answerable by measurement rather than by asking someone to read the same code.
- **Visual scope.** A non-goal (1.3), with one live sub-question deferred to Phase 3's exit with an evidence rule: if the CSS port needs restructured selectors rather than renamed ones, decide then whether a utility layer is in scope (R21).

---

## 13. Review log

Three revisions came from a four-stage review, each stage running three reviewers in parallel: technical accuracy (external tooling, repository facts, Go feasibility), architecture (security, API design, contract pipeline), executability (phase ordering, build and CI mechanics, criteria and risk quality), and cohesion (internal consistency, house style, whether the document answers what was asked). What changed, so a reader of the first draft knows what not to trust.

**Facts corrected.** Route count 65 not ~70. JS files 16 not 14. Handler files 46 not 45. Operation count 69 not 55. Three routes already return JSON, not one. The e2e suite has 34 of 78 IDs written, not 6. Fourteen file:line references were off by between one and eleven lines. `dependabot.yml` has no npm coverage at all. `lint-meta.yml` has no path filters, it has hardcoded globs. `.golangci.yml` does not enable `depguard`, which an acceptance criterion depended on. `codeql.yml` and four sites in `security.yml` were missing from the rename blast radius. `make cover` is already broken independently of this plan.

**Designs that were wrong and are now different.** A huma handler has no `ResponseWriter`, so every session write needed the unwrap middleware and a huma version floor. `//go:embed dist` does not compile on a clean checkout. The SPA fallback would have answered `/api/v1/typo` with `200 text/html`. `huma.Operation.Metadata` never reaches the spec, so the role-matrix test could not have been spec-driven. Huma returns 422, not 400. `DefaultConfig` injects `$schema` into every response and registers three unauthenticated routes below the middleware. The `Service` interface was solving a problem that did not exist. The Dockerfile could not have built the SPA at all, and could not run its own drift check or version derivation, because `.git` is not in the build context. A hand-edited version file reddens `main` on a clean merge. The drift gate was in the wrong job, and the registration split was scheduled a phase after the command that depends on it. A custom merge driver is inert for GitHub's own merge. The long-lived feature branch was removed entirely.

**Things the first draft simply did not consider.** Response DTOs inheriting untagged model fields including `PasswordHash` and `SMTPPassword` (R15). Fail-open role defaults (R16). Missing `__Host-` cookie prefixes. The OIDC callback needing nonce and verifier, not just state. Reauthentication on MFA disable and password change. Self-target invariants in the users multiplexer. Rate limiting applied globally. `unblock_ip` and the `theme` action having no home in the endpoint map. No capability endpoint, so the SPA could not know whether OIDC is enabled. Sliding sessions plus `refetchOnWindowFocus` making the idle timeout decorative. The `401` interceptor eating the login flow. A refresh mid-MFA losing the pending state. `blob:` missing from the CSP, blocking Phase 8's own deliverable. Settings `PUT` clearing secrets. Pagination without a total order. The `le` to `acme` rename applying to paths only. `secrets/`, `.env` and `backups/` entering a widened Docker context. And no rollback section.

**What the owner's answers changed, after the fourth stage.** Three decisions were settled and two of them reshaped the architecture. The API is versioned despite having no second consumer, which turned 5.1 from a hedge into a policy with an enforcement mechanism. "Separate" was confirmed to mean **two containers with nginx serving the SPA**, which rewrote D1 and D5 completely: the Go binary no longer serves assets, and `//go:embed static`, `staticHandlerFromFS` and the MIME table all move out of it, and nginx becomes the origin, the TLS terminator and the router. The critical property preserved is that a deployment split is not an origin split, so D6's entire cookie and CSRF design survives untouched. Three risks were added for the new surface (R13 certificate reload, R25 the proxy as a second place to get identity wrong, R26 the routing table written twice) and one was deleted, since the `//go:embed` placeholder problem no longer exists. And there is no deadline, which flipped R18 from schedule pressure to drift.

**What the fourth stage changed about the framing rather than the engineering.** The document evaluated how to build the split and never evaluated whether to. It now opens with 1.0, which examines the stated motive and finds no load reading supports it, and 1.5, which sets out the four alternatives to the whole approach that were previously never named. 1.1 leads with the fact that this is a rewrite of the UI rather than a separation of one, because the gap between the request's wording and the actual work was the largest unstated risk in the document. Sections 1.4 (what it buys, what gets worse) and the sizing paragraph in Section 8 were added because a reader could previously decide *how* to proceed but not *whether*. D1 gained an explicit list of what rests on it, D2 gained the price of its own off-ramp, and the two open questions were replaced by the three that actually gate the work.

**What the third stage changed about the plan's instruments rather than its content.** Roughly a third of revision 1's acceptance criteria were uncheckable as written ("describes every operation the SPA calls", "functionally equivalent to the template it replaces", "the e2e suite passes at the level agreed in Section 10", which was circular), and are now either mechanical with a named command or explicitly marked as judgement with a named owner. Fifteen criteria were missing entirely for claims the plan makes strongly elsewhere, including every one of D10's aggregate rules and D9's exposure decision. The risk register gained triggers and owner phases, lost two entries that were not risks, and gained seven that are: the plan having one holder, the branch outliving its ability to merge, a phase stalling the project half-migrated, the generated client being routed around, the CSS port being a rewrite, two authorisation paths coexisting for eight phases, and `handlers/` becoming a service layer being unpriced work. Revision 1 had eighteen risks and not one of them was about the project failing as a project.
