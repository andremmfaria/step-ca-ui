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

// notFound answers an unmatched /api/v1/* path with a problem document
// instead of chi's plain-text 404, matching what every huma-routed 404
// already looks like. Paths outside /api/v1 keep the pre-existing behaviour.
func notFound(w http.ResponseWriter, r *http.Request) {
	if strings.HasPrefix(r.URL.Path, BasePath) {
		writeProblem(w, http.StatusNotFound, "Not Found", "no operation matches this path", r.URL.Path)
		return
	}
	http.NotFound(w, r)
}
