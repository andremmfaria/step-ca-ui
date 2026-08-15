package api

import (
	appmw "step-ui/middleware"

	"github.com/danielgtaylor/huma/v2"
)

// Metadata keys the chain in middleware.go reads off the matched operation.
// They are unexported strings rather than a typed key because
// huma.Operation.Metadata is a map[string]any defined upstream.
const (
	metaRole      = "role"
	metaAuth      = "auth"
	metaRateLimit = "ratelimit"
	metaCSRF      = "csrf"
)

// csrfWhenSession relaxes the CSRF requirement to "enforce only when the
// session actually carries a token".
//
// It exists for exactly one shape of operation: one whose only effect is
// clearing the caller's own state. Logging out has to work from a session the
// server has already rejected, and a session with no decodable token has
// nothing to forge a mutation against, so refusing it with a 403 would leave
// the caller stuck sending a cookie the server rejects on every request. Any
// operation carrying this that has another effect is a bug, which is why the
// golden table records it as its own column value rather than hiding it.
const csrfOptional = "optional"

// The three values of metaAuth (5.5). Absent means "session required plus a
// role", which is the default and is never written out.
const (
	authPublic   = "public"
	authOptional = "optional"
)

// rateLimitAuth is the only rate-limit scope. Rate limiting is scoped, not
// global (5.5): applied globally, five bad logins from a shared corporate
// egress would 429 every authenticated user on the installation.
const rateLimitAuth = "auth"

// securitySchemeName is the scheme roleOp attaches role names to. It is
// documentation only: OpenAPI 3.1 permits a non-OAuth scheme's requirement
// array to carry role names and it self-validates, but nothing enforces it
// and hey-api discards it. Enforcement is metaRole at request time (5.5).
const securitySchemeName = "session"

// roleOp is the only way a role is written. It derives all three
// representations from one argument: the runtime metadata the chain enforces,
// the x-required-role extension a reader sees, and the Security entry the
// document carries. Writing them by hand invites a change that updates one
// and leaves the spec documenting an authorisation posture the runtime does
// not enforce, which is a spec that lies in exactly the place worth checking.
// The role-matrix test asserts all three agree.
func roleOp(role string, op huma.Operation) huma.Operation { //nolint:gocritic // hugeParam: huma.Register takes huma.Operation by value, so a decorator returning one composes at the call site exactly as 5.5 spells it
	if _, ok := appmw.RoleLevels[role]; !ok {
		// A typo here would otherwise register an operation no role can ever
		// satisfy, which reads as a permissions bug rather than a build error.
		panic("api: roleOp called with unknown role " + role)
	}
	op.Metadata = withMeta(op.Metadata, metaRole, role)
	if op.Extensions == nil {
		op.Extensions = map[string]any{}
	}
	op.Extensions["x-required-role"] = role
	op.Security = []map[string][]string{{securitySchemeName: {role}}}
	return op
}

// publicOp marks an operation as needing no session and no role. The chain
// touches no cookie for it.
func publicOp(op huma.Operation) huma.Operation { //nolint:gocritic // hugeParam: huma.Register takes huma.Operation by value, so a decorator returning one composes at the call site exactly as 5.5 spells it
	op.Metadata = withMeta(op.Metadata, metaAuth, authPublic)
	op.Security = []map[string][]string{}
	return op
}

// optionalAuthOp marks the one operation that validates a session when one is
// present and never answers 401 (5.5). The role-matrix test fails if any
// operation other than getSession carries it.
func optionalAuthOp(op huma.Operation) huma.Operation { //nolint:gocritic // hugeParam: huma.Register takes huma.Operation by value, so a decorator returning one composes at the call site exactly as 5.5 spells it
	op.Metadata = withMeta(op.Metadata, metaAuth, authOptional)
	op.Security = []map[string][]string{}
	return op
}

// csrfWhenSession marks an operation as CSRF-checked only when the session
// holds a token. See csrfOptional.
func csrfWhenSession(op huma.Operation) huma.Operation { //nolint:gocritic // hugeParam: huma.Register takes huma.Operation by value, so a decorator returning one composes at the call site exactly as 5.5 spells it
	op.Metadata = withMeta(op.Metadata, metaCSRF, csrfOptional)
	return op
}

// rateLimited scopes the login rate limiter onto one operation. Composes with
// roleOp and publicOp in either order.
func rateLimited(op huma.Operation) huma.Operation { //nolint:gocritic // hugeParam: huma.Register takes huma.Operation by value, so a decorator returning one composes at the call site exactly as 5.5 spells it
	op.Metadata = withMeta(op.Metadata, metaRateLimit, rateLimitAuth)
	return op
}

func withMeta(m map[string]any, key string, value any) map[string]any {
	if m == nil {
		m = map[string]any{}
	}
	m[key] = value
	return m
}

// opRole reports the role an operation requires, and whether it declared one.
func opRole(op *huma.Operation) (string, bool) {
	if op == nil {
		return "", false
	}
	role, ok := op.Metadata[metaRole].(string)
	return role, ok && role != ""
}

// opAuth reports an operation's auth mode: authPublic, authOptional, or "" for
// the default of session-required-plus-a-role.
func opAuth(op *huma.Operation) string {
	if op == nil {
		return ""
	}
	mode, _ := op.Metadata[metaAuth].(string)
	return mode
}
