// Package api registers this backend's huma-described JSON operations under
// /api/v1, mounted onto the existing chi router alongside every legacy
// template route (Phase 0 of plans/frontend-backend-split.md). Registration
// (Register) is kept separate from construction (Mount, NewForSpec) per D3:
// cmd/openapi needs a huma.API built with no database, no CA client and no
// environment.
package api

import (
	"context"
	"net/http"

	"step-ui/handlers"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// BasePath is the one spelling of the /api/v1 prefix (5.1); every operation
// path below is written out starting with it.
const BasePath = "/api/v1"

// maxBodyBytes bounds every /api/v1 request body before any parser sees it
// (5.7). Phase 0 proves the mechanism; the real per-endpoint ceilings are a
// later-phase decision.
const maxBodyBytes = 5 << 20 // 5 MiB

type ctxKey int

const ctxKeyHTTP ctxKey = iota

// httpPair is what the unwrap middleware stashes on the context so operation
// handlers — which huma hands only a context.Context — can reach
// gorilla/sessions, which needs both the *http.Request and the
// http.ResponseWriter (D2 cost 2, 5.5).
type httpPair struct {
	r *http.Request
	w http.ResponseWriter
}

// httpFrom recovers the pair the unwrap middleware below stashed.
func httpFrom(ctx context.Context) (*http.Request, http.ResponseWriter) {
	p := ctx.Value(ctxKeyHTTP).(httpPair) //nolint:errcheck // programmer error if ever absent: every operation is registered through Register, which runs after UseMiddleware
	return p.r, p.w
}

// config is shared by Mount (the running server) and NewForSpec (cmd/openapi,
// the drift-gate test) so the two can never disagree (7.1). CreateHooks is
// nilled because DefaultConfig's SchemaLinkTransformer injects a $schema
// property into every response body and a request-Host-derived Link header.
// DocsPath and OpenAPIPath are cleared because huma's auto-registered /docs
// and /openapi.json are wired below api.UseMiddleware and so bypass every
// authorisation check (D9).
func config() huma.Config {
	cfg := huma.DefaultConfig("step-ca-ui", handlers.Version)
	cfg.CreateHooks = nil
	cfg.DocsPath = ""
	cfg.OpenAPIPath = ""
	return cfg
}

// unwrapMiddleware is the first huma middleware in the chain (5.5): it is
// what makes gorilla/sessions reachable from an operation handler at all.
// A declared Content-Length over the ceiling is rejected before any parser
// runs (5.7); http.MaxBytesReader is the backstop for a chunked body with no
// declared length.
func unwrapMiddleware(ctx huma.Context, next func(huma.Context)) {
	r, w := humachi.Unwrap(ctx)
	if r.ContentLength > maxBodyBytes {
		writeProblem(w, http.StatusRequestEntityTooLarge, "Request Entity Too Large", "request body exceeds the size limit", r.URL.Path)
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxBodyBytes)
	next(huma.WithValue(ctx, ctxKeyHTTP, httpPair{r, w}))
}

// Mount wires a huma.API onto r at BasePath, alongside every existing chi
// route, and registers Phase 0's operations against h. r must be the router
// the legacy routes are already registered on: huma operation paths carry
// the /api/v1 prefix themselves (5.1) rather than being registered under a
// chi sub-router, so a request never gets prefix-stripped twice.
func Mount(r chi.Router, h *handlers.Handler) huma.API {
	// Default is 8 KiB, far too small for a cert/key upload (5.7).
	humachi.MultipartMaxMemory = 1 << 20 // 1 MiB

	humaAPI := humachi.New(r, config())
	humaAPI.UseMiddleware(unwrapMiddleware)
	Register(humaAPI, h)

	r.NotFound(notFound)

	return humaAPI
}

// NewForSpec builds a huma.API against a spec-only Handler (D3): no chi
// router ever serves it, no database, no CA client, no environment. Used by
// backend/cmd/openapi and the generate-twice drift test.
func NewForSpec() huma.API {
	humaAPI := humachi.New(chi.NewRouter(), config())
	Register(humaAPI, handlers.NewForSpec())
	return humaAPI
}
