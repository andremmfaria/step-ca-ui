// Package apitypes holds the shapes and constants shared by the huma
// operation layer, the middleware chain and cmd/openapi. It imports only the
// standard library and other apitypes packages, enforced by a depguard
// allowlist (Section 2), so that neither the router nor the handlers can pull
// a database or a CA client into anything the spec is generated from.
package apitypes

// BasePath is the one spelling of the versioned API prefix (5.1). Every scope
// that needs to know where the JSON API starts derives it from here: the CSRF
// middleware's scope, the request-body ceiling, the document-CSP exclusion,
// X-Session-Expires-At's emission scope and the 404 handler. Each of those
// carrying its own literal is a correctness defect independent of versioning,
// because they then drift one at a time.
const BasePath = "/api/v1"

// Pagination bounds shared by every list operation (5.6). They live here
// rather than in each query struct so the schema, the validation and the
// deep-offset check cannot disagree.
const (
	// DefaultPageSize replaces the old hardcoded const pageSize = 30.
	DefaultPageSize = 25
	// MaxPageSize is expressed in the schema, so a client cannot ask for more.
	MaxPageSize = 200
	// MaxOffset bounds page*pageSize. Deep offsets are not a supported access
	// pattern and exceeding this is a 422, not a slow query.
	MaxOffset = 10000
)

// Page is the envelope every list response uses (5.6).
//
// Total is a pointer because a null total means the server declined to count,
// and the client renders next/previous instead of page numbers. TotalPages is
// null exactly when Total is.
type Page[T any] struct {
	Items      []T  `json:"items"`
	Page       int  `json:"page" doc:"1-based page number"`
	PageSize   int  `json:"pageSize"`
	Total      *int `json:"total" doc:"null when the server declined to count"`
	TotalPages *int `json:"totalPages" doc:"null whenever total is null"`
}

// NewPage builds an envelope with a counted total.
func NewPage[T any](items []T, page, pageSize, total int) Page[T] {
	if items == nil {
		items = []T{}
	}
	pages := 0
	if pageSize > 0 {
		pages = (total + pageSize - 1) / pageSize
	}
	return Page[T]{Items: items, Page: page, PageSize: pageSize, Total: &total, TotalPages: &pages}
}

// NewUncountedPage builds an envelope for a query that declined to count.
func NewUncountedPage[T any](items []T, page, pageSize int) Page[T] {
	if items == nil {
		items = []T{}
	}
	return Page[T]{Items: items, Page: page, PageSize: pageSize}
}

// BulkResult is one entry of a bulk operation's results array (5.2). A bulk
// operation returns 200 with these and never a problem document for partial
// failure: a problem document means the whole operation was rejected.
type BulkResult struct {
	Ref    string `json:"ref"`
	Status string `json:"status" enum:"ok,error"`
	Error  string `json:"error,omitempty"`
}

// BulkResponse is the body of every bulk operation.
type BulkResponse struct {
	Results []BulkResult `json:"results"`
}
