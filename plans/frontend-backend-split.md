# Plan: Split step-ca-ui into a React SPA and an OpenAPI-described Go backend-for-frontend

Status: Draft. Written 2026-08-13 against `main` at `57f73e3`.

## 0. Using this document

| Section | |
|---|---|
| [1. Objective](#1-objective) | 1.1 What exists today · 1.2 What changes · 1.3 Non-goals |
| [2. Acceptance criteria](#2-acceptance-criteria) | |
| [3. Decisions and alternatives](#3-decisions-and-alternatives) | D1 to D10, each with the rejected options and why |
| [4. Target architecture](#4-target-architecture) | 4.1 Repository layout · 4.2 Runtime topology · 4.3 Request lifecycle · 4.4 Development loop |
| [5. API conventions](#5-api-conventions) | 5.1 Base path and versioning · 5.2 Error model · 5.3 Authentication · 5.4 CSRF · 5.5 Authorisation · 5.6 Pagination and filtering · 5.7 Binary responses · 5.8 Naming |
| [6. Endpoint map](#6-endpoint-map) | every current route to its replacement |
| [7. The contract pipeline](#7-the-contract-pipeline) | 7.1 Spec generation · 7.2 Drift gate · 7.3 Client generation · 7.4 Packaging · 7.5 Publication · 7.6 Consumption |
| [8. Phases](#8-phases) | Phase 0 to Phase 9 |
| [9. CI and CD changes](#9-ci-and-cd-changes) | |
| [10. Impact on the in-flight e2e work](#10-impact-on-the-in-flight-e2e-work) | |
| [11. Risk register](#11-risk-register) | R1 to R13 |
| [12. Open questions](#12-open-questions) | needs an answer before Phase 1 starts |

---

## 1. Objective

Replace the server-rendered `html/template` UI in `step-ui-go/` with a React single-page application that talks to the same Go process over a versioned JSON API. The Go side becomes a backend-for-frontend (BFF): it owns sessions, authorisation, step-ca access, the database and Let's Encrypt orchestration, and it publishes a machine-readable OpenAPI 3.1 description of everything the browser is allowed to call. That description, and nothing else, is the contract. The frontend never hand-writes a request. It imports a TypeScript client package that the BFF's own CI generates from the spec and publishes, and its build fails if the package it resolves does not typecheck against the code that uses it.

### 1.1 What exists today

A correction to the premise this plan was requested under. There is no TypeScript frontend. The UI is Go `html/template` server-side rendering:

- **33 templates** in `step-ui-go/templates/`, composed from `base.html` and `admin_base.html`.
- **~70 chi routes** registered inline in `step-ui-go/main.go:222-326`, all of them returning HTML, a redirect, or a file download. The single exception is `GET /api/status` (`h.APIStatus`).
- **14 plain-JavaScript files** in `step-ui-go/static/js/` (no build step, no modules, no types), doing progressive enhancement on top of the rendered HTML.
- **Forms post `application/x-www-form-urlencoded`** and the server answers `302` plus a one-shot flash message stored in the session.
- The only TypeScript in the repo is the Playwright harness under `test/e2e/`, which is a separate Node project.

Everything else that matters to this plan:

- Sessions are `gorilla/sessions` cookie sessions (`main.go:191-200`), cookie name `step-ui`, `HttpOnly`, `SameSite=Lax`, 8 hour `MaxAge`, AES-encrypted with a key derived from `cfg.SecretKey`.
- CSRF is a random token held in the session and echoed in a hidden form field, checked by `h.csrfOK` / `h.requireCSRF` (`handlers/handler.go:301`) on every mutating route.
- Authorisation is chi-group-scoped: `mw.RequireLogin` wraps the authenticated group, `mw.RequireRole("manager")` and `mw.RequireRole("admin")` wrap sub-groups (`main.go:239-317`).
- Login is two-step when TOTP is on: `POST /login` parks `pending_2fa_user_id` in the session and redirects back to `/login`, which then renders the code form (`handlers/auth.go:78-127`).
- CSP is already strict and has no `unsafe-inline` (`middleware/middleware.go:56-61`). `Cache-Control: no-store` is global.
- Templates and static assets are embedded with `//go:embed templates static` (`main.go:38-39`), so the binary is self-contained.
- **The project has no live deployments, operators, or existing volumes.** This is stated in `plans/step-cli-to-ca-lib-swap.md` and remains true. There is no backward compatibility to preserve, no staged rollout to design, and no need for a strangler pattern that keeps both UIs alive.

### 1.2 What changes

1. Every route that the browser needs becomes a JSON operation under `/api/v1`, defined once as a Go type pair and registered with an OpenAPI-aware router layer.
2. The Go binary emits `openapi.json` deterministically, from a command that needs no database and no CA.
3. BFF CI generates a typed TypeScript SDK from that spec, packs it as an npm tarball, and publishes it.
4. A new `frontend/` Vite + React application consumes that package. Its CI resolves the package produced by the same commit, so a spec change that breaks the frontend fails the frontend job in the same run.
5. The Go binary embeds the built SPA and serves it, so the deployable stays one container on one origin.
6. `templates/`, `static/js/`, and the flash-message mechanism are deleted.

### 1.3 Non-goals

- Splitting into two repositories or two deployables. See D1.
- Changing the database schema, the `stepca` package, the Let's Encrypt renewer, or `tlsbootstrap.go`.
- Replacing cookie sessions with tokens. See D6.
- Redesigning the visual language. The existing CSS is ported as-is in the first pass. Visual work is a separate effort.
- Making the API a public, externally supported product surface. It is a BFF, shaped for this frontend, and it is free to expose page-shaped aggregate endpoints.

---

## 2. Acceptance criteria

**Contract**

- [ ] `step-ui-go/openapi/openapi.json` exists, is committed, is OpenAPI 3.1, and describes every operation the SPA calls.
- [ ] `go run ./cmd/openapi -out openapi/openapi.json` regenerates that file byte-identically on a machine with no database, no step-ca and no network. Running it twice in a row produces no diff.
- [ ] A CI job regenerates the spec and fails the build on any diff against the committed copy, with the failure message naming the command that fixes it.
- [ ] The spec contains zero `additionalProperties: true` schemas and zero operations without a declared error response.
- [ ] Every operation carries an `operationId` in `camelCase`, a `summary`, and a `tags` entry drawn from a fixed list (`session`, `certificates`, `acme`, `provisioners`, `history`, `admin`, `users`, `profile`, `system`).

**Client package**

- [ ] BFF CI produces `@andremmfaria/step-ca-ui-client` as an npm tarball artifact on every pull request, and publishes it to GitHub Packages on every push to `main`.
- [ ] The package ships ESM plus `.d.ts`, has no runtime dependency other than the fetch client it is generated against, and its version encodes the commit it was generated from.
- [ ] The generated sources are **not** committed. The only committed contract artifact is `openapi.json`.
- [ ] `npm run typecheck` in `frontend/` fails if an operation the frontend calls was removed or had a required field added.

**Backend**

- [ ] `main.go` registers no route that returns HTML except the SPA fallback.
- [ ] `step-ui-go/templates/` and `step-ui-go/static/js/` are deleted. `//go:embed` covers the SPA build output instead.
- [ ] `h.render`, `h.flash`, `h.base` and the whole template-data-map convention are deleted from `handlers/`.
- [ ] Every mutating operation still enforces CSRF, and every operation still enforces the same role it enforces today. A test asserts the role matrix operation by operation, driven off the spec rather than a hand-written list.
- [ ] `go build ./...`, `go vet ./...`, `golangci-lint run` and `go test -race ./...` pass. The coverage gate threshold is at or above its current 15 and is re-floored to the new measured baseline in the PR that lands each phase.
- [ ] Unauthenticated access to an `/api/v1` route returns `401` with `application/problem+json` and never a `302`.

**Frontend**

- [ ] `frontend/` builds with `npm ci && npm run build` and produces hashed assets that satisfy the existing CSP with no `unsafe-inline` and no `unsafe-eval`.
- [ ] `npm run lint`, `npm run typecheck` and `npm run test` are wired into CI.
- [ ] Every page listed in Section 6 is reachable and functionally equivalent to the template it replaces, verified against a live `docker compose up` stack.
- [ ] A hard refresh on any deep link (for example `/certificates/12`) serves the SPA and lands on the right view.
- [ ] Session expiry mid-session surfaces as a redirect to the login view, not a blank page or an unhandled rejection.

**Integration**

- [ ] `docker compose up` from empty volumes yields a working UI with no extra services and no second container.
- [ ] The image contains no Node runtime. The SPA is built in a builder stage and copied in as static files.
- [ ] `make dev` runs the Vite dev server against a locally running BFF with hot reload and no CORS configuration.
- [ ] The e2e suite passes at the coverage level agreed in Section 10.

---

## 3. Decisions and alternatives

### D1. One repository, two build units, one deployable

**Decision.** Keep everything in `andremmfaria/step-ca-ui`. Add `frontend/` beside `step-ui-go/` and `test/e2e/`. Ship one container image in which the Go binary embeds the SPA build output and serves it on the same origin as the API.

**Rejected: two repositories.** The contract is the coupling, and the contract changes on nearly every feature. Two repositories turn a one-PR change into a three-PR dance (spec, publish, consume) with a broken intermediate state on every one of them. The published package still exists in the monorepo design, so nothing is lost.

**Rejected: two containers plus a reverse proxy.** It buys independent scaling that a single-tenant internal CA UI does not need, and it costs an origin boundary, which costs CORS, which costs `SameSite=None` cookies, which costs the CSRF posture that currently comes for free.

**Consequence.** "Frontend CI consumes a package generated by BFF CI" is honoured, but on a pull request the package is consumed as a workflow artifact from the same run rather than from the registry. Registry publication happens on `main` and serves external consumers and local development. See D8.

### D2. Code-first OpenAPI with `huma` v2

**Decision.** Adopt `github.com/danielgtaylor/huma/v2` with the `humachi` adapter over the existing chi router. Operations are declared as `huma.Register(api, huma.Operation{...}, func(ctx, *In) (*Out, error))` with Go structs for input and output. Huma derives OpenAPI 3.1 from those structs, validates requests against the derived schema before the handler runs, and returns RFC 9457 `application/problem+json` on failure.

**Why.** The handlers already exist as Go code with Go types. Huma keeps the type as the single source of truth, so a struct field that gains a `json` tag cannot silently fail to appear in the spec. It also removes an entire class of hand-written code: request binding, required-field checks, enum checks, and the error envelope.

**Rejected: spec-first with `oapi-codegen`.** Hand-writing an OpenAPI document for ~55 operations and then generating server interfaces gives a reviewable YAML diff and a compile-time guarantee that handlers match. It also means the source of truth for a Go service is a YAML file that no Go tool checks, every field is written twice, and the team maintains a schema language alongside the language they are actually working in. The reviewable-diff benefit is recovered for free by D3.

**Rejected: `swaggo/swag`.** Comment-driven annotations are a third representation that drifts from both the types and the runtime. Its OpenAPI 3.1 support lags.

**Rejected: writing the spec by hand and generating nothing.** Guarantees drift. Not worth discussing further.

**Cost to accept.** Huma owns the request lifecycle for the operations it registers. Existing chi middleware still runs (huma mounts under chi), but per-operation concerns such as role checks move from `r.Group(func(r chi.Router){ r.Use(mw.RequireRole("admin")) })` into either huma middleware or a chi sub-router that huma is mounted onto. Section 5.5 fixes which.

### D3. The spec is committed, and CI gates the diff

**Decision.** `step-ui-go/openapi/openapi.json` is a committed, generated file. A `cmd/openapi` binary writes it. CI regenerates and fails on any difference.

**Why.** This recovers the one real advantage of spec-first. Every API change shows up as a reviewable diff in the pull request, next to the Go change that caused it, and an unintended breaking change (a field going required, an enum losing a value, an operation disappearing) is visible to a human reviewer rather than discovered by the frontend three commits later.

**Requirement.** Generation must be deterministic and side-effect free. `cmd/openapi` constructs the router and the operation registrations against zero-value or fake dependencies, never opens a database connection, never dials step-ca, and never reads the environment. Any handler constructor that today panics or `log.Fatalf`s on a missing dependency (`initOIDC` does, `handlers/handler.go:82`) must be reachable in a registration-only mode. This is a real refactor, not a formality, and it is Phase 1 work.

### D4. TypeScript client generated with `@hey-api/openapi-ts`

**Decision.** Generate the client with `@hey-api/openapi-ts`, configured with the fetch client plugin and the TanStack Query plugin. Output is one package: typed operation functions, request and response types, and ready-made query and mutation options for each operation.

**Why.** It reads OpenAPI 3.1, which huma emits. It generates both a plain SDK and query hooks, so the frontend gets caching, in-flight deduplication and invalidation without hand-writing a hook per endpoint. It is the actively maintained successor to `openapi-typescript-codegen`.

**Rejected: `openapi-typescript` plus `openapi-fetch`.** Smaller and more stable, but it emits types only, so every call site writes the path and the method as literals. The types catch a wrong shape but the discoverability of a real SDK is lost, and there is no generated query layer.

**Rejected: `orval`.** Comparable feature set. `@hey-api/openapi-ts` has the cleaner 3.1 story and the simpler configuration.

**Rejected: `openapi-generator` (`typescript-fetch`).** A Java toolchain in a Node build, verbose output, and awkward 3.1 support.

**Cost to accept.** `@hey-api/openapi-ts` is pre-1.0 and its generated output has changed shape between minor versions. Pin it exactly, and treat a generator bump as its own pull request with a reviewed diff of the generated package. See R2.

### D5. Same origin, SPA embedded in the Go binary

**Decision.** `frontend/dist` is copied into the Go build context and embedded with `//go:embed`. Go serves `/assets/*` from it with long-lived immutable caching, and serves `index.html` for any GET that is not `/api/*`, not `/assets/*`, not `/health`, `/ready`, `/openapi.json` and not `/auth/oidc/*`.

**Why.** One artifact, one origin, no CORS, no `SameSite=None`, no separate web server to configure or patch, and the existing `staticHandlerFromFS` path-traversal protection is reused. Deployment ergonomics are unchanged from today.

**Note.** The global `Cache-Control: no-store` in `mw.SecurityHeaders` must be overridden for `/assets/*`, exactly as the current static handler already overrides it (`middleware/middleware.go:62-65` documents that this is intended). `index.html` itself stays `no-store` so a new deploy is picked up immediately.

### D6. Keep cookie sessions, add double-submit CSRF

**Decision.** The `step-ui` cookie session stays exactly as it is. Tighten `SameSite` from `Lax` to `Strict` for the session cookie (the only thing that needed `Lax` was the OIDC callback landing, which is handled by keeping a separate short-lived `Lax` OIDC state cookie). CSRF moves from a hidden form field to a double-submit pair: the server sets a non-`HttpOnly` `step-ui-csrf` cookie holding the same token it stores in the session, and the client sends it back in an `X-CSRF-Token` header on every mutating request. `h.csrfOK` compares header to session value with `subtle.ConstantTimeCompare`, which is what it already does against the form field.

**Why.** No token in `localStorage`, no XSS-exfiltratable credential, no refresh-token machinery, no change to `mw.RequireLogin` or the session epoch logic. The generated client sets the header once in a global request interceptor, so no call site knows CSRF exists.

**Rejected: JWT in `localStorage`.** Strictly worse security for zero benefit in a same-origin BFF.

**Rejected: header-only CSRF with no token (relying on `SameSite=Strict` plus a custom-header requirement).** Defensible, and arguably sufficient, but it discards a working implementation and a tested property (`E2E-CSRF-01`, `E2E-CSRF-05`) in exchange for deleting twenty lines.

### D7. React 19, Vite, TanStack Query, React Router, existing CSS

**Decision.** Vite + React 19 + TypeScript strict. TanStack Query for all server state (fed by the generated query options). React Router for routing. No global client state library. The existing CSS files move to `frontend/src/styles/` mostly unchanged and are imported by the app.

**Why.** Vite is the default for a non-SSR React app and needs no configuration to satisfy the CSP. TanStack Query is what the generated client targets. There is no client state worth a store: everything on screen is server state plus form state.

**Rejected: Next.js.** SSR and a Node runtime in the image, to replace the SSR that is being removed. Absurd here.

**Rejected: a component library (MUI, Chakra, shadcn).** Not in the first pass. The existing CSS is complete, themed (`themes.css`), and already passes the CSP. Adopting a library is a visual redesign wearing a technical hat, and it would triple the diff of every migration phase. Revisit after the split lands.

**Rejected: TanStack Router.** Better types, but React Router is what most contributors and most generated examples assume, and the routing here is trivial.

### D8. Versioning and the pull-request chicken-and-egg

**Decision.**

- Package version is `0.<MINOR>.<PATCH>-sha.<short-sha>` on pull requests and `0.<MINOR>.<PATCH>` on `main`, where MINOR and PATCH come from a `version` field in `step-ui-go/openapi/package-version.txt` that a human bumps when the API changes shape.
- On a **pull request**: the `client` job builds the package and uploads `step-ca-ui-client-<sha>.tgz` as a workflow artifact. The `frontend` job downloads that artifact and installs it with `npm install ./step-ca-ui-client-<sha>.tgz` before `npm ci` of the rest. Nothing is published.
- On **`main`**: the same job additionally publishes to GitHub Packages, and a follow-up commit is **not** made. `frontend/package.json` pins the client with a `file:` reference resolved at CI time in both cases, so the committed lockfile never churns on every merge.

**Why the artifact hop rather than publishing prereleases from pull requests.** Publishing from an untrusted pull-request context needs write credentials on a fork-reachable workflow, and it litters the registry with one version per push. The artifact gives the exact same guarantee (the frontend is validated against the spec produced by this commit) with none of that.

**Why publish at all, then.** So that a developer can `npm install @andremmfaria/step-ca-ui-client` without building Go, so that the package is a real, inspectable, versioned artifact, and because the request asks for it.

### D9. Big-bang cutover, phased for review

**Decision.** There is no dual-stack period in which both the templates and the SPA serve the same page. `main` keeps the old UI working until the branch that removes it lands. Migration is phased by domain purely so that each phase is a reviewable pull request against a long-lived `feat/spa` branch, not so that partial states are shippable.

**Why.** No live deployments means no rollback pressure and no user-visible intermediate state to protect. A strangler pattern here would cost a routing shim, two sets of session assumptions, and duplicated navigation, to hedge a risk that does not exist.

**Caveat.** `main` still needs to stay green. Each phase pull request must leave `go test`, `golangci-lint` and the e2e suite passing on the branch. Section 10 covers how the e2e suite is kept honest across the transition.

### D10. The API is a BFF, not a REST purity exercise

**Decision.** Where a page needs four resources, the API gets one page-shaped aggregate operation that returns all four, rather than making the SPA fire four requests and stitch them. `GET /api/v1/dashboard` is the canonical example. Resource-shaped operations are used where a resource is genuinely being manipulated.

**Why.** This is the point of a BFF. The alternative is a chatty client with a waterfall on every navigation, and aggregation logic duplicated in TypeScript that currently lives correctly in Go.

**Boundary.** Aggregates are read-only. Mutations are always resource-shaped and single-purpose. In particular, today's `POST /admin/users` is an action multiplexer that switches on a form field, and it is split into `POST /api/v1/users`, `PATCH /api/v1/users/{id}` and `DELETE /api/v1/users/{id}`.

---

## 4. Target architecture

### 4.1 Repository layout

```
step-ca-ui/
├── frontend/                        NEW. Vite + React + TS. Its own package.json.
│   ├── src/
│   │   ├── api/                     generated-client wiring: base URL, CSRF interceptor, 401 handling
│   │   ├── routes/                  one directory per page, mirroring today's templates
│   │   ├── components/              shared UI primitives extracted from base.html and admin_base.html
│   │   ├── styles/                  ported from step-ui-go/static/css
│   │   └── main.tsx
│   ├── index.html
│   ├── vite.config.ts
│   └── package.json
├── step-ui-go/
│   ├── api/                         NEW. huma operation registration, split by domain
│   │   ├── api.go                   huma.API construction, middleware, error mapping
│   │   ├── session.go
│   │   ├── certificates.go
│   │   ├── acme.go
│   │   ├── admin.go
│   │   ├── users.go
│   │   ├── profile.go
│   │   └── system.go
│   ├── apitypes/                    NEW. request and response DTOs. No database types leak here.
│   ├── cmd/openapi/main.go          NEW. deterministic spec dump. No DB, no CA, no env.
│   ├── openapi/
│   │   ├── openapi.json             generated, committed, gated
│   │   └── package-version.txt      the client package's MINOR.PATCH, bumped by hand
│   ├── handlers/                    KEPT. becomes the service layer the api/ package calls.
│   ├── webui/                       NEW. //go:embed of the SPA build output plus the fallback handler
│   ├── templates/                   DELETED in Phase 9
│   └── static/                      DELETED in Phase 9 (css is moved, not deleted, in Phase 3)
├── clients/ts/                      NEW. generator config + package.json template. Output is gitignored.
├── test/e2e/                        KEPT. see Section 10.
└── plans/
```

### 4.2 Runtime topology

Unchanged from today: three containers (`postgres`, `step-ca`, `step-ui`). The `step-ui` image gains a Node builder stage that produces `frontend/dist`, and the runtime stage still contains no Node.

```
Dockerfile stages:
  1. node:22-alpine        npm ci && npm run build      -> /frontend/dist
  2. golang:1.26.5-alpine  COPY --from=1 dist -> webui/dist ; go build   -> /step-ui
  3. alpine:3.23           COPY --from=2 /step-ui        (no templates/, no static/)
```

The build context must widen from `./step-ui-go` to the repository root, because it now needs `frontend/` as well. `docker-compose.yml` and `.github/workflows/e2e.yml` both pin `context: ./step-ui-go` today and both change.

### 4.3 Request lifecycle

```
browser
  └─ GET /certificates/12                  -> chi -> SPA fallback -> index.html
  └─ GET /assets/index-a1b2c3.js           -> chi -> webui embed  -> immutable cache
  └─ GET /api/v1/certificates/12           -> chi -> Recoverer, RealIP, SecurityHeaders
                                              -> huma adapter
                                              -> session middleware  (401 problem+json if absent)
                                              -> role middleware     (403 problem+json if wrong role)
                                              -> huma request validation from the derived schema
                                              -> api.getCertificate(ctx, *GetCertificateInput)
                                              -> handlers service call
                                              -> *GetCertificateOutput -> JSON
```

### 4.4 Development loop

`make dev` runs two processes:

- The Go binary on `:8443` as today.
- `vite dev` on `:5173` with `server.proxy` forwarding `/api`, `/auth`, `/health`, `/ready` and `/openapi.json` to `https://localhost:8443` with `secure: false`.

Same origin from the browser's point of view, so cookies and CSRF behave exactly as in production. The Vite dev server needs `connect-src 'self' ws:` in the CSP, which is why the dev CSP is a documented, dev-only relaxation applied by the Vite dev server itself and never by Go.

Regenerating the client locally is `make client`, which runs `go run ./cmd/openapi`, then the generator, then `npm install ./clients/ts/dist/*.tgz` in `frontend/`.

---

## 5. API conventions

### 5.1 Base path and versioning

All JSON operations live under `/api/v1`. The version is in the path and is bumped only for a breaking change to an operation that survives. Additive changes do not bump it. Since there is exactly one consumer and it is built from the same commit, `v1` is expected to be the only version that ever exists, and the segment is there so that the eventual external consumer has somewhere to stand.

`GET /api/status` (the one existing JSON route) becomes `GET /api/v1/status`. `GET /health` and `GET /ready` stay at their current unversioned paths because the Docker `HEALTHCHECK` and compose health gates depend on them, and they are additionally exposed under `/api/v1` for the SPA.

### 5.2 Error model

RFC 9457 `application/problem+json`, which is huma's default. Every error response is:

```json
{
  "type": "https://step-ca-ui/errors/validation",
  "title": "Bad Request",
  "status": 400,
  "detail": "field name is required",
  "instance": "/api/v1/certificates",
  "errors": [{ "location": "body.name", "message": "expected string, got null" }]
}
```

Rules:

- Never return a `302` from `/api/v1`. Session expiry is `401`. Wrong role is `403`. Missing entity is `404`. CSRF mismatch is `403` with `type: .../csrf`.
- Login failure is `401` with a deliberately non-specific `detail`, matching today's behaviour of not distinguishing unknown user from wrong password.
- Rate-limit block is `429` with `Retry-After`, replacing today's rendered `Blocked` page. `security.RL.Left(ip)` becomes a field in the problem extension.
- The flash mechanism disappears entirely. Success feedback is the response body plus client-side toast. Failure feedback is the problem document.

### 5.3 Authentication

| | |
|---|---|
| `POST /api/v1/session` | body `{username, password}`. On success with no TOTP: `200 {status: "authenticated", user: {...}}` and the session cookie is set. On success with TOTP: `200 {status: "mfa_required"}` and only the `pending_2fa_user_id` half of the session is set, exactly as `handlers/auth.go:118-125` does today. |
| `POST /api/v1/session/mfa` | body `{totpCode?, recoveryCode?}`. Consumes the pending 2FA session. `200` with the same authenticated payload, or `401`. |
| `GET /api/v1/session` | `200` with the current user, role, `mfaEnabled`, `sessionExpiresAt`, and the CSRF token. `401` if there is no session. This is the SPA's boot call and the source of truth for the auth guard. |
| `DELETE /api/v1/session` | logout. Requires CSRF. |

OIDC keeps its browser redirect flow at `/auth/oidc/login` and `/auth/oidc/callback`, outside `/api/v1` and outside the spec, because a `302` chain to a third-party issuer is not an API operation. The callback completes the session and then redirects to `/` , where the SPA boots and calls `GET /api/v1/session` like any other load. The state cookie for the OIDC round trip is a separate short-lived `SameSite=Lax` cookie so that the main session cookie can be `Strict`.

### 5.4 CSRF

Per D6. Concretely:

- `completeLogin` already mints `s.Values["csrf_token"]` (`handlers/auth.go:201`). It additionally sets a `step-ui-csrf` cookie with the same value, `Secure`, `SameSite=Strict`, **not** `HttpOnly`, same `MaxAge` as the session.
- A huma middleware rejects any `POST`, `PUT`, `PATCH` or `DELETE` under `/api/v1` whose `X-CSRF-Token` header does not constant-time-match the session value. `POST /api/v1/session` and the password-reset operations are included, matching today's behaviour, and the token for those comes from an unauthenticated `GET /api/v1/session` that returns `401` but still sets the cookie. That last detail is easy to get wrong and needs an explicit test.
- The generated client is configured once with a request interceptor that reads the cookie and sets the header. No call site touches it.

### 5.5 Authorisation

Role requirements move into the operation declaration, not the router:

```go
huma.Register(api, huma.Operation{
    OperationID: "revokeCertificate",
    Method:      http.MethodPost,
    Path:        "/api/v1/certificates/{id}/revoke",
    Tags:        []string{"certificates"},
    Metadata:    map[string]any{"role": "admin"},
    Security:    []map[string][]string{{"session": {"admin"}}},
}, a.revokeCertificate)
```

A single huma middleware reads `Metadata["role"]` and enforces it, and the same metadata drives the `Security` requirement that appears in the spec, so the role matrix is machine-readable. A test walks the registered operations and asserts each one's required role against a table, which replaces the current implicit "it is in the right chi group" guarantee with an explicit one. This directly strengthens the property that `MC-5421`-style authorisation gaps are caught by construction.

`mw.RequireLogin` keeps its current job (loading the user, checking `session_epoch`, checking `last_activity`) but is adapted to write a problem document instead of redirecting, and to store the user in the huma context.

### 5.6 Pagination and filtering

Today's pagination is ad-hoc (`handlers/history.go:26`, `handlers/seclog.go:14` both read a bare `page` query parameter). Standardise on:

```
?page=1&pageSize=25&q=<search>&sort=<field>&order=asc|desc
```

Every list response is `{ items: [...], page, pageSize, total, totalPages }`. `pageSize` is bounded server-side (max 200) and the bound is expressed in the schema so the client cannot even ask for more.

### 5.7 Binary responses

Certificate, key, CA chain and backup downloads stay as real HTTP downloads with `Content-Disposition`. Declared in the spec with `content: application/octet-stream` and `format: binary`, which generated clients render as a `Blob`-returning function. The frontend triggers a save from the blob rather than navigating, so that the `X-CSRF-Token` header and the `401` handling apply uniformly.

`POST /admin/backup/download` is the one mutating download. It stays a `POST` (it triggers `pg_dump`) and returns the stream directly.

`GET /profile/2fa/qr` returns `image/png` and is declared the same way.

### 5.8 Naming

`operationId` is `camelCase` verb-noun (`listCertificates`, `issueCertificate`, `revokeCertificate`). Paths are lowercase kebab. JSON fields are `camelCase`, set once via huma's field-name derivation and never per-struct. Timestamps are RFC 3339 strings in UTC. Durations are ISO 8601 strings or integer seconds, chosen once and stated here: **integer seconds**, because `UI_CERT_DURATION` and the certificate duration fields are already Go `time.Duration` and the round trip is lossless.

---

## 6. Endpoint map

Every route in `main.go:222-326`, and what replaces it. "Aggregate" marks a BFF-shaped read that has no one-to-one predecessor because the template did the joining.

### Public

| Today | Replacement | Notes |
|---|---|---|
| `GET /health` | `GET /health` plus `GET /api/v1/health` | path kept for `HEALTHCHECK` |
| `GET /ready` | `GET /ready` plus `GET /api/v1/ready` | path kept for compose gates |
| `GET /login` | deleted | SPA route, no server round trip |
| `POST /login` | `POST /api/v1/session` and `POST /api/v1/session/mfa` | split, see 5.3 |
| `GET /forgot-password` | deleted | SPA route |
| `POST /forgot-password` | `POST /api/v1/password-reset/request` | always `202`, no state leak, matching today |
| `GET /reset-password` | `GET /api/v1/password-reset/{token}` | validates the token so the SPA can show the form or an error |
| `POST /reset-password` | `POST /api/v1/password-reset/confirm` | |
| `GET /logout`, `POST /logout` | `DELETE /api/v1/session` | the `GET` variant disappears, which is a security improvement |
| `GET /auth/oidc/login` | unchanged, outside the spec | browser redirect |
| `GET /auth/oidc/callback` | unchanged, outside the spec | browser redirect, then `/` |

### Session and dashboard

| Today | Replacement |
|---|---|
| `GET /` , `GET /dashboard` | `GET /api/v1/dashboard` (aggregate: counts, expiring soon, CA status, recent activity) |
| `GET /api/status` | `GET /api/v1/status` |
| — | `GET /api/v1/session` (new, SPA boot call) |

### Certificates

| Today | Replacement |
|---|---|
| `GET /certificates` | `GET /api/v1/certificates?page&pageSize&q&status&tab` |
| `GET /certificates/{id}` | `GET /api/v1/certificates/{id}` |
| `GET /issue` | `GET /api/v1/certificates/issue-options` (aggregate: templates, key types, provisioners, defaults) |
| `POST /issue` | `POST /api/v1/certificates` (field is `name`, not `cert_name`, per `plans/e2e-implementation-status.md`) |
| `POST /renew/{id}` | `POST /api/v1/certificates/{id}/renew` |
| `POST /revoke/{id}` | `POST /api/v1/certificates/{id}/revoke` |
| `GET /import` | `GET /api/v1/certificates/import-options` |
| `POST /import` | `POST /api/v1/certificates/import` (multipart stays multipart) |
| `GET /download/cert/{id}` | `GET /api/v1/certificates/{id}/download/certificate` |
| `GET /download/key/{id}` | `GET /api/v1/certificates/{id}/download/key` |
| `GET /download/ca` | `GET /api/v1/ca/download/root` |
| `GET /download/intermediate-ca` | `GET /api/v1/ca/download/intermediate` |
| `GET /download/full-chain` | `GET /api/v1/ca/download/chain` |

### History, provisioners, security log

| Today | Replacement |
|---|---|
| `GET /history` | `GET /api/v1/history?cert&page&pageSize` |
| `GET /provisioners` | `GET /api/v1/provisioners` |
| `GET /admin/security` | `GET /api/v1/security-log?q&filter&page&pageSize` |
| `GET /admin/activity` | `GET /api/v1/admin/activity?page&pageSize` |

### Admin

| Today | Replacement |
|---|---|
| `GET /admin` | `GET /api/v1/admin/overview` (aggregate) |
| `GET /admin/users` | `GET /api/v1/users?page&pageSize&q` |
| `POST /admin/users` | split into `POST /api/v1/users`, `PATCH /api/v1/users/{id}`, `DELETE /api/v1/users/{id}`, `POST /api/v1/users/{id}/password-reset` |
| `GET /admin/users/{id}` | `GET /api/v1/users/{id}` |
| `GET /admin/users-temp` | `GET /api/v1/admin/temporary-users` |
| `POST /admin/users-temp` | `POST /api/v1/admin/temporary-users` plus `DELETE /api/v1/admin/temporary-users/{id}` |
| `GET /admin/console` | `GET /api/v1/admin/console/commands` (the allowlist, so the SPA renders it rather than hardcoding) |
| `POST /admin/console` | `POST /api/v1/admin/console/run` body `{commandId}` |
| `GET /admin/about` | `GET /api/v1/admin/about` |
| `GET /admin/integrity` | `GET /api/v1/admin/integrity` |
| `GET /admin/backup` | `GET /api/v1/admin/backup` |
| `POST /admin/backup/download` | `POST /api/v1/admin/backup/download` (octet-stream) |
| `GET /admin/notifications` | `GET /api/v1/admin/notifications` |
| `POST /admin/notifications` | `PUT /api/v1/admin/notifications` |
| `POST /admin/notifications/test` | `POST /api/v1/admin/notifications/test` |

### Let's Encrypt

| Today | Replacement |
|---|---|
| `GET /le` | `GET /api/v1/acme/certificates` |
| `GET /le/issue` | `GET /api/v1/acme/issue-options` |
| `POST /le/issue` | `POST /api/v1/acme/certificates` |
| `POST /le/{id}/renew` | `POST /api/v1/acme/certificates/{id}/renew` |
| `POST /le/{id}/delete` | `DELETE /api/v1/acme/certificates/{id}` |
| `POST /le/{id}/autorenew` | `PUT /api/v1/acme/certificates/{id}/auto-renew` body `{enabled}` |
| `GET /le/download/cert/{id}` | `GET /api/v1/acme/certificates/{id}/download/certificate` |
| `GET /le/download/key/{id}` | `GET /api/v1/acme/certificates/{id}/download/key` |
| `GET /le/settings` | `GET /api/v1/acme/settings` |
| `POST /le/settings` | `PUT /api/v1/acme/settings` |
| `GET /le/logs` | `GET /api/v1/acme/logs?page&pageSize&domain` |

The `le` to `acme` rename in the path is deliberate. The feature is ACME, `LE_ACME_DIRECTORY_URL` already points anywhere, and the e2e suite runs it against Pebble.

### Profile

| Today | Replacement |
|---|---|
| `GET /profile` | `GET /api/v1/profile` |
| `POST /profile` | `PUT /api/v1/profile` plus `POST /api/v1/profile/password` |
| `GET /profile/2fa` | `GET /api/v1/profile/mfa` |
| `POST /profile/2fa/start` | `POST /api/v1/profile/mfa/enrollment` |
| `GET /profile/2fa/qr` | `GET /api/v1/profile/mfa/enrollment/qr` (image/png) |
| `POST /profile/2fa/confirm` | `POST /api/v1/profile/mfa/enrollment/confirm` |
| `POST /profile/2fa/disable` | `DELETE /api/v1/profile/mfa` |

Count: 55 JSON operations, 4 unversioned browser or probe routes, plus the SPA fallback and asset handler.

---

## 7. The contract pipeline

### 7.1 Spec generation

`step-ui-go/cmd/openapi/main.go`:

```go
func main() {
	out := flag.String("out", "openapi/openapi.json", "output path")
	check := flag.Bool("check", false, "exit non-zero if out differs from generated")
	flag.Parse()

	_, api := api.NewForSpec()
	doc, err := api.OpenAPI().MarshalJSON()
	...
}
```

`api.NewForSpec()` builds the huma API with every operation registered against a zero-dependency service implementation. It must not construct a database handle, must not call `stepca.New`, and must not call `initOIDC`. OIDC-conditional routes (`main.go:232-236`) are always registered in the spec and gated at runtime, which is a small behaviour change worth stating: with OIDC disabled the operation exists in the spec and returns `404` with a problem document.

Determinism requirements, each of which needs an explicit test:

- JSON marshalling of Go maps is key-sorted, so path and schema ordering is stable. Verify that huma does not use an insertion-ordered map anywhere in the document, and if it does, post-process through a canonicalising re-marshal.
- Two-space indentation, trailing newline, `SetEscapeHTML(false)`.
- No timestamps, no version strings derived from build metadata, and no host or server URL that varies by environment. `servers` is either omitted or a fixed relative `/`.

### 7.2 Drift gate

A CI step in the existing `build-test-lint` job:

```yaml
- name: OpenAPI spec is current
  run: |
    go run ./cmd/openapi -out /tmp/openapi.json
    if ! diff -u openapi/openapi.json /tmp/openapi.json; then
      echo "::error::openapi/openapi.json is stale. Run: make openapi"
      exit 1
    fi
```

A pre-commit hook mirrors it, since `.pre-commit-config.yaml` already exists.

### 7.3 Client generation

`clients/ts/openapi-ts.config.ts`:

```ts
import { defineConfig } from '@hey-api/openapi-ts'

export default defineConfig({
  input: '../../step-ui-go/openapi/openapi.json',
  output: { path: './src', format: 'prettier', lint: 'eslint' },
  plugins: [
    '@hey-api/client-fetch',
    '@hey-api/typescript',
    '@hey-api/sdk',
    '@tanstack/react-query',
  ],
})
```

`clients/ts/src/` is gitignored. `clients/ts/package.json` is committed and holds the package name, the `exports` map, the build script (`tsc` to ESM plus declarations) and the exact generator pin.

### 7.4 Packaging

```
@andremmfaria/step-ca-ui-client
├── dist/index.js        ESM
├── dist/index.d.ts
└── package.json         "type": "module", "sideEffects": false
```

No runtime dependencies. `@tanstack/react-query` and the fetch client are peer dependencies. The version is stamped by the CI job from `openapi/package-version.txt` plus the short sha, per D8.

### 7.5 Publication

A `client` job in a new `contract.yml` workflow, or a new job in `ci.yml`:

```yaml
- run: npm pack --pack-destination ../../dist
  working-directory: clients/ts
- uses: actions/upload-artifact@...
  with: { name: ts-client, path: dist/*.tgz }
- name: Publish
  if: github.ref == 'refs/heads/main'
  run: npm publish --access restricted
  env:
    NODE_AUTH_TOKEN: ${{ secrets.GITHUB_TOKEN }}
```

with `permissions: { contents: read, packages: write }` on that job only, and `.npmrc` pointing `@andremmfaria:registry=https://npm.pkg.github.com`.

**Known trap.** If the package is first created manually with a personal access token, GitHub denies `GITHUB_TOKEN` write access to it forever, and the only fix is a manual "Actions access" grant in the package settings UI. Create the package from CI the first time, or budget for the UI step. See R3.

### 7.6 Consumption

`frontend/package.json` declares the dependency as `"@andremmfaria/step-ca-ui-client": "file:../clients/ts/dist/client.tgz"` for local development, and CI overwrites that path with the downloaded artifact before install. The lockfile therefore records a stable `file:` specifier and does not churn per commit.

A single `frontend/src/api/client.ts` configures the generated client once:

```ts
import { client } from '@andremmfaria/step-ca-ui-client'

client.setConfig({
  baseUrl: '',
  credentials: 'same-origin',
})

client.interceptors.request.use((req) => {
  const token = readCookie('step-ui-csrf')
  if (token && req.method !== 'GET') req.headers.set('X-CSRF-Token', token)
  return req
})

client.interceptors.response.use((res) => {
  if (res.status === 401) queryClient.clear(), redirectToLogin()
  return res
})
```

---

## 8. Phases

Each phase is one pull request against a long-lived `feat/spa` branch, except Phase 9 which is the merge to `main`.

### Phase 0. Vertical slice spike

Prove the whole pipeline on two operations before committing to it.

1. Add huma and `humachi`. Mount a `huma.API` under chi at `/api/v1` alongside every existing route.
2. Implement `GET /api/v1/status` and `GET /api/v1/session` as huma operations.
3. Add `cmd/openapi` and commit the resulting `openapi.json`.
4. Add `clients/ts` and generate a client from that two-operation spec locally.
5. Add a throwaway `frontend/` that boots, calls `getSession`, and renders the username.
6. Confirm: spec is deterministic across two runs and two machines, the generated client typechecks, the CSRF interceptor works, and a `401` produces `application/problem+json` rather than a redirect.

**Exit criterion.** The spike runs end to end. If huma's ergonomics or spec output turn out to be wrong for this codebase, this is the cheap moment to switch to `oapi-codegen`, and D2 is revisited before any of Phases 4 to 8 is written.

### Phase 1. API foundation

1. `apitypes/` with the shared envelope types: `Page[T]`, `Problem`, `User`, `Certificate`, `Provisioner`, `AuditEntry`.
2. Huma middleware: session loading, role enforcement from `Operation.Metadata["role"]`, CSRF header check, rate-limit surfacing as `429`.
3. Adapt `mw.RequireLogin` to the problem-document contract without breaking the template routes still using it. The cleanest form is a small wrapper rather than a fork.
4. `completeLogin` sets the `step-ui-csrf` cookie. Session cookie moves to `SameSite=Strict`, and a separate `Lax` cookie carries the OIDC state.
5. The role-matrix test that walks registered operations.
6. The spec drift gate in `ci.yml`, and `make openapi`.

### Phase 2. Client package pipeline

1. `clients/ts` with the generator config, the package manifest, and the build script.
2. The `client` job: generate, build, pack, upload artifact, publish on `main`.
3. `openapi/package-version.txt` and the version-stamping step.
4. Document the local loop in the README and in `make client`.

### Phase 3. Frontend scaffold

1. Vite + React 19 + TS strict, ESLint, Prettier, Vitest.
2. Move `step-ui-go/static/css/*` to `frontend/src/styles/`. No rewrites, only whatever import-path fixes are needed.
3. App shell: the layout, top navigation and admin navigation currently in `base.html` and `admin_base.html`, as components.
4. Router with the full route table from Section 6, every leaf a placeholder.
5. Auth guard driven by `GET /api/v1/session`, plus the login view against Phase 1's session operations, including the two-step MFA flow.
6. `webui/` in Go: `//go:embed dist`, the asset handler with immutable caching, the SPA fallback, and the `Cache-Control` override.
7. Dockerfile gains the Node builder stage. Build context widens to the repository root. `docker-compose.yml` and `e2e.yml` updated.
8. `make dev` with the Vite proxy.

**Exit criterion.** Log in, see an empty dashboard, navigate, hard-refresh a deep link, log out. Old template routes still work and are still reachable.

### Phase 4. Certificates

Operations, views and tests for the certificate list, detail, issue, import, renew, revoke and all six download endpoints. This is the largest single phase and the one that proves the pattern for everything after it, including multipart upload and octet-stream download through the generated client.

### Phase 5. History, provisioners, security log, dashboard

Includes the standardised pagination from 5.6, which replaces three different ad-hoc implementations, and the `GET /api/v1/dashboard` aggregate.

### Phase 6. Admin

Users (including the `POST /admin/users` multiplexer split), temporary users, activity, console, about, integrity, backup, notifications. The console operation keeps its allowlist server-side and merely exposes the allowlist for rendering.

### Phase 7. Let's Encrypt

The `le` to `acme` path rename, settings as `PUT`, logs with pagination, auto-renew as a `PUT` on a subresource.

### Phase 8. Profile and MFA

Profile, password change, and the full TOTP enrolment flow including the PNG QR endpoint and recovery codes.

### Phase 9. Removal and hardening

1. Delete `templates/`, `static/js/`, `h.render`, `h.flash`, `h.base`, `templateFuncs`, and the `tmpls` field on `Handler`.
2. Delete the template-route registrations from `main.go`.
3. Re-check the CSP against the built bundle. Confirm no `unsafe-inline` is needed. Add `connect-src 'self'` and `frame-src 'none'` explicitly rather than relying on `default-src`.
4. Re-floor the coverage gate to the new measured baseline.
5. Update `README.md`, `Makefile` help text, and `plans/e2e-tests.md` cross-references.
6. Merge `feat/spa` to `main`.

---

## 9. CI and CD changes

| Workflow | Change |
|---|---|
| `ci.yml` | add the OpenAPI drift gate to `build-test-lint`. Add a `frontend` job: Node setup, download the client artifact, `npm ci`, `lint`, `typecheck`, `test`, `build`. Add a `client` job that the `frontend` job depends on. |
| new `contract.yml` or a job in `ci.yml` | generate, build, pack and upload the client. Publish to GitHub Packages on `main` with `packages: write` scoped to that job. |
| `e2e.yml` | build context changes from `./step-ui-go` to `.`. The image build now includes the Node stage, so the `cache-from`/`cache-to` scope should be split so a frontend-only change does not invalidate the Go layer cache. |
| `docker-build.yml` | same context change. |
| `security.yml` | add `npm audit` or the equivalent for `frontend/` and `clients/ts`. Existing Go scanning is unchanged. |
| `lint-meta.yml` | add `frontend/` to whatever path filters it carries. |
| `dependabot.yml` | add two `npm` ecosystems (`/frontend`, `/clients/ts`). Note the existing thundering-herd caution recorded for the infra repos. |
| `.pre-commit-config.yaml` | add the spec drift check and a `frontend` lint hook. |

`Makefile` gains: `openapi`, `client`, `dev`, `frontend-install`, `frontend-build`, `frontend-lint`, `frontend-test`.

---

## 10. Impact on the in-flight e2e work

This is the highest-value section of this plan, because there is an agent building the e2e suite right now against the interface this plan deletes.

**Current state** (`plans/e2e-implementation-status.md`, 2026-08-13): Phases 0 to 2 complete. Phase 3 has landed 6 of ~78 specs, all in the `api` project. 95 passing, 8 skipped. Remaining work is the bulk of the PR tier, the whole `infra` project, the four `ui` companions and the nightly legs.

**What survives the split**

| Suite | Fate |
|---|---|
| `infra` project (E2E-BOOT-01 to -09) | **Untouched.** Bootstrap, `UI_TLS_MODE`, root provisioning, deliberate fatals and the renewal goroutine are all below the HTTP layer. Zero rework. |
| Health (E2E-HLTH-01 to -06) | **Untouched.** `/health` and `/ready` keep their paths and payloads. |
| `api` project, all other specs | **Rework.** Path, method, request encoding, status code and assertion shape all change. The test's intent survives, its body does not. |
| `ui` project (4 browser companions) | **Rewrite.** Selectors, navigation and flash assertions all change. |
| Helpers | `compose.ts`, `psql`, `openssl.ts`, `poll.ts`, `qr.ts`, `totp.ts`, `envfile.ts` survive. `flash.ts` is deleted. `session.ts` and `routes.ts` are rewritten. |

Rough cut: the `infra` and health tiers, about a quarter of the suite by test count and more than that by effort, are unaffected. Most of the rest needs its body rewritten against the new contract.

**Recommendation**

1. **Do not pause the e2e agent, and do not let it keep writing `api` specs against form-encoded routes either.** Redirect it. The remaining high-value work that is immune to this plan is the `infra` project (E2E-BOOT-01 to -09), the nightly `renew` leg, and finishing the harness scripts. Point it there first.
2. **Freeze new `api` specs** for the domains this plan touches until Phase 1 lands. Six already-written specs is a cheap loss. Sixty would not be.
3. **After Phase 2, the suite gains a better tool than it has today.** The generated TypeScript client can be installed into `test/e2e` and used to drive `api` specs. That gets the suite compile-time protection against contract drift, which is exactly the class of breakage e2e tests are currently catching by hand. The `routes.ts` helper, which today derives the POST route list from the router, is replaced by iterating the spec.
4. **The `ui` project specs should be written last**, after Phase 8, once the SPA's markup is stable. Writing browser specs against the templates now is work with a known expiry date.
5. `plans/e2e-tests.md` needs an appendix, not a rewrite, recording which test IDs changed contract and what their new shape is. Follow the append-only convention used elsewhere in this repo's plan documents.

---

## 11. Risk register

**R1. Collision with the in-flight e2e work.** The single largest cost in this plan is not the migration, it is the test suite written against the interface being removed. Mitigated entirely by Section 10, and only if that redirection happens before the next tranche of `api` specs is written. **This is the one item that is urgent rather than important.**

**R2. `@hey-api/openapi-ts` is pre-1.0.** Generated output shape has changed between minor versions. Mitigation: pin the exact version, never a range. Treat generator upgrades as their own pull request with the generated diff reviewed. If it becomes unstable, the escape hatch is `openapi-typescript` plus `openapi-fetch`, which is types-only and far more stable, at the cost of losing the generated SDK and query layer. That swap is contained to `clients/ts` and the call sites.

**R3. GitHub Packages token trap.** A package first created with a personal access token permanently denies `GITHUB_TOKEN`, and the fix is a manual "Actions access" grant in the package settings UI. Mitigation: never publish the package by hand. Let the first publish come from CI on `main`. If it is already broken, budget the manual UI step.

**R4. Spec generation is not actually deterministic.** If huma uses an insertion-ordered structure anywhere, the drift gate produces spurious failures and gets disabled, which loses the whole benefit. Mitigation: a test in Phase 0 that generates twice and byte-compares, plus a canonicalising re-marshal if needed. Prove it in the spike, not in Phase 6.

**R5. `cmd/openapi` cannot be made dependency-free.** `initOIDC` calls `log.Fatalf` on discovery failure (`handlers/handler.go:82`) and `NewWithFS` loads templates eagerly. If registration cannot be separated from construction, the spec command needs a live OIDC issuer in CI, which is unacceptable. Mitigation: this is the first real refactor of Phase 1 and it is not optional. Its shape is a `Service` interface that `api/` registers against, with a nil-safe zero implementation for spec generation.

**R6. CSRF regression during the cookie move.** The `step-ui-csrf` cookie has to be set on a `401` response from `GET /api/v1/session` so that an unauthenticated login POST has a token. That is a non-obvious requirement and it is easy to write the middleware in an order where it does not happen. Mitigation: an explicit test, and keep `E2E-CSRF-01` and `E2E-CSRF-05` green throughout by rewriting them in the same pull request that changes the mechanism.

**R7. Session cookie `SameSite=Strict` breaks the OIDC landing.** A `Strict` cookie is not sent on the top-level navigation that returns from the issuer, so the callback cannot read the session it needs. Mitigation: the separate `Lax` state cookie described in 5.3. If that proves awkward, `Lax` on the session cookie is the status quo and is acceptable, and the decision reverts with no other consequence.

**R8. Coverage gate.** Moving logic out of `handlers/` and into `api/` will move covered and uncovered lines around unpredictably. The gate is currently 15 and the convention is that it never regresses. Mitigation: measure per phase and re-floor deliberately. Do not let a phase land that drops the number without a stated reason.

**R9. Binary downloads through a generated client.** Blob handling, `Content-Disposition` filename extraction and the CSRF header on `POST /admin/backup/download` are the awkward corner of every generated client. Mitigation: prove one download end to end in Phase 4 before writing the other five, and be willing to hand-write a thin wrapper for downloads if the generated shape is bad.

**R10. Bundle size and the CSP.** No CDN is reachable under `default-src 'self'`, so every dependency ships in the bundle. Fonts, icon sets and any library that injects a `<style>` tag at runtime are all constraints. Mitigation: self-host everything, keep the dependency list short, and verify the built bundle against the real CSP in Phase 3 rather than at the end.

**R11. The admin console becomes trivially scriptable.** `POST /api/v1/admin/console/run` is a documented, typed, discoverable operation that executes an allowlisted shell command. It was equally powerful before, but it was buried in a form. Mitigation: the allowlist stays server-side and is the only thing that matters, the `admin` role requirement is asserted by the role-matrix test, and CSRF still applies. Consider whether the operation should be excluded from the published client package.

**R12. Docker build context widening.** Moving from `context: ./step-ui-go` to `.` pulls the whole repository, including `test/e2e/node_modules` and `frontend/node_modules`, into the build context. Without a `.dockerignore` this is slow and may break layer caching entirely. Mitigation: write `.dockerignore` in Phase 3, in the same commit as the context change.

**R13. Scope.** This plan touches 33 templates, ~70 routes, 45 handler files, every CI workflow and the entire test suite. There is no version of it that is small. The mitigation is the phase boundary discipline in Section 8 and the Phase 0 exit criterion, which is a genuine off-ramp rather than a formality.

---

## 12. Open questions

These need an answer before Phase 1 starts. Phase 0 can proceed without them.

**Q1. Deployable shape.** D5 assumes one container serving both the API and the SPA from one origin. Is there any requirement, current or foreseen, for the frontend to be served from a CDN or a separate host? If yes, D5, D6 and the CORS and `SameSite` posture all change.

**Q2. Publication.** Should the client package be published to GitHub Packages under `@andremmfaria` at all, given there is exactly one consumer and it lives in the same repository? The artifact-per-run mechanism satisfies the stated requirement on its own. Publishing is additional surface (a registry, a token, a scope, R3) for the benefit of external consumers who do not currently exist.

**Q3. E2E sequencing.** Section 10 recommends redirecting the e2e agent to the `infra` and health tiers immediately and freezing new `api` specs. Confirm, because the cost of not deciding rises with every spec written.

**Q4. Visual scope.** D7 ports the existing CSS unchanged, so the SPA looks identical to today. Is a visual refresh in scope, and if so, should it be a separate effort after the split or folded into the per-domain phases? Folding it in roughly doubles each phase and makes every review argue about two things at once.

**Q5. Duration encoding.** 5.8 fixes durations as integer seconds. Confirm, since it appears in certificate issuance requests, `UI_CERT_DURATION` reporting and the ACME settings, and changing it later is a breaking spec change.
