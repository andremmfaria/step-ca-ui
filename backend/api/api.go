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

	"step-ui/apitypes"
	"step-ui/handlers"
	appmw "step-ui/middleware"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
)

// BasePath re-exports apitypes.BasePath so operation paths in this package
// read naturally. apitypes owns the constant because middleware/ derives its
// scopes from it too and cannot import api/ without a cycle (5.1).
const BasePath = apitypes.BasePath

// maxBodyBytes bounds every BasePath request body before any parser sees it
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
// DocsPath, OpenAPIPath and SchemasPath are cleared because huma wires those
// routes below api.UseMiddleware, so they bypass every authorisation check
// (D9). SchemasPath has its own register site independent of CreateHooks.
func config() huma.Config {
	cfg := huma.DefaultConfig("step-ca-ui", handlers.Version)
	cfg.CreateHooks = nil
	cfg.DocsPath = ""
	cfg.OpenAPIPath = ""
	cfg.SchemasPath = ""
	// The scheme roleOp attaches role names to. It documents the posture; the
	// runtime enforcement is Metadata["role"] in roleMiddleware (5.5).
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		securitySchemeName: {
			Type:        "apiKey",
			In:          "cookie",
			Name:        handlers.SessionCookieName,
			Description: "Encrypted session cookie. Role names in a requirement array are documentation: enforcement is server-side.",
		},
	}
	// Installed here rather than on the mounted API so cmd/openapi's
	// spec-only API behaves identically (5.2).
	cfg.Transformers = append(cfg.Transformers, stripSubmittedValues)
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
func Mount(r chi.Router, h *handlers.Handler, loadUser appmw.UserLoader) huma.API {
	// Default is 8 KiB, far too small for a cert/key upload (5.7).
	humachi.MultipartMaxMemory = 1 << 20 // 1 MiB

	humaAPI := humachi.New(r, config())
	// Order matters and is 5.5's, with one correction: the recoverer is second
	// rather than last, so that a panic in any of the four gates below it is
	// still answered with a problem document. unwrap must precede it because
	// it is what makes the ResponseWriter reachable.
	humaAPI.UseMiddleware(
		unwrapMiddleware,
		recoverMiddleware,
		sessionMiddleware(h, loadUser),
		roleMiddleware,
		csrfMiddleware(h),
		rateLimitMiddleware,
	)
	Register(humaAPI, h)

	r.NotFound(notFound)
	r.MethodNotAllowed(methodNotAllowed)

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
