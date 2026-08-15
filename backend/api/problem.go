package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
)

// writeProblem writes an RFC 9457 body matching what a huma-registered
// operation would emit, for the two places that answer a request huma never
// routes to a handler: the 413 short-circuit in unwrapMiddleware and
// notFound below.
func writeProblem(w http.ResponseWriter, status int, title, detail, instance string) {
	model := huma.NewError(status, detail).(*huma.ErrorModel) //nolint:errcheck // huma.NewError's default implementation always returns *huma.ErrorModel
	model.Title = title
	model.Instance = instance
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(model)
}

// notFound answers an unmatched BasePath path with a problem document instead
// of chi's plain-text 404, matching what every huma-routed 404 already looks
// like. Paths outside BasePath keep the pre-existing behaviour: there is no
// Go-side SPA fallback and no Go-side asset handler, because nginx owns both
// (D5).
func notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, BasePath) {
		writeProblem(w, http.StatusNotFound, "Not Found", "no operation matches this path", r.URL.Path)
		return
	}
	http.NotFound(w, r)
}

// methodNotAllowed does the same for a path that exists under BasePath at a
// method it does not serve. chi writes a bare 405 with no body, which the
// generated client surfaces as an unparseable error.
func methodNotAllowed(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, BasePath) {
		writeProblem(w, http.StatusMethodNotAllowed, "Method Not Allowed", "this path does not serve that method", r.URL.Path)
		return
	}
	w.WriteHeader(http.StatusMethodNotAllowed)
}

// problemTypeBase namespaces the machine-readable type URIs (5.2). The SPA
// switches on these, so they are part of the contract rather than decoration.
const problemTypeBase = "https://step-ca-ui/errors/"

// writeCSRFProblem answers a CSRF mismatch with 403 and a distinguishable
// type, so the SPA can re-fetch a token and retry rather than logging the user
// out as it would on a generic 403 (5.2).
func writeCSRFProblem(w http.ResponseWriter, instance string) {
	model := huma.NewError(http.StatusForbidden, "CSRF token missing or invalid").(*huma.ErrorModel) //nolint:errcheck // huma.NewError's default implementation always returns *huma.ErrorModel
	model.Title = "Forbidden"
	model.Type = problemTypeBase + "csrf"
	model.Instance = instance
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusForbidden)
	_ = json.NewEncoder(w).Encode(model)
}

// writeRateLimitProblem answers a blocked address with 429. attemptsLeft is
// the same disclosure today's rendered login failure already makes, so it
// tells an attacker nothing new and tells a locked-out operator something
// useful (5.2).
func writeRateLimitProblem(w http.ResponseWriter, instance string, attemptsLeft int) {
	model := huma.NewError(http.StatusTooManyRequests, "too many attempts, try again later").(*huma.ErrorModel) //nolint:errcheck // huma.NewError's default implementation always returns *huma.ErrorModel
	model.Title = "Too Many Requests"
	model.Type = problemTypeBase + "rate-limit"
	model.Instance = instance
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(http.StatusTooManyRequests)
	// huma.ErrorModel carries no extension map, so the member is added by
	// embedding: an anonymous pointer field with no json tag is flattened, so
	// the body is still exactly an ErrorModel plus one documented member.
	_ = json.NewEncoder(w).Encode(struct {
		*huma.ErrorModel
		AttemptsLeft int `json:"attemptsLeft"`
	}{model, attemptsLeft})
}
