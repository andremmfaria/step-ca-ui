package api

import (
	"crypto/subtle"
	"log/slog"
	"math"
	"net/http"
	"runtime/debug"
	"strconv"
	"time"

	"step-ui/handlers"
	appmw "step-ui/middleware"
	"step-ui/security"

	"github.com/danielgtaylor/huma/v2"
	"github.com/gorilla/sessions"
)

// csrfHeaderName is the header the SPA echoes the readable CSRF cookie in
// (5.4). A custom header is what makes the check work at all: it cannot be set
// cross-origin without a CORS preflight this API never answers.
const csrfHeaderName = "X-CSRF-Token"

// sessionExpiresHeader tells the SPA when the idle window closes, so it can
// warn before a mutation is refused rather than after (5.3).
const sessionExpiresHeader = "X-Session-Expires-At"

// recoverMiddleware turns a panic anywhere in the huma chain into a problem
// document.
//
// It is registered second, immediately inside unwrapMiddleware, rather than
// last. chiMiddleware.Recoverer writes text/plain and huma has no recovery of
// its own, so a recoverer at the innermost position would leave a panic in
// session validation, role enforcement, CSRF or rate limiting emitting exactly
// the non-problem 500 it exists to prevent. It runs after unwrapMiddleware
// because it needs the ResponseWriter that middleware stashes.
func recoverMiddleware(ctx huma.Context, next func(huma.Context)) {
	defer func() {
		if rec := recover(); rec != nil {
			r, w := httpFrom(ctx.Context())
			slog.Error("panic in api chain", "path", r.URL.Path, "panic", rec, "stack", string(debug.Stack()))
			writeProblem(w, http.StatusInternalServerError, "Internal Server Error", "the server failed to handle this request", r.URL.Path)
		}
	}()
	next(ctx)
}

// sessionMiddleware is 5.3's fail-closed session gate. auth: public and
// auth: optional are the only exemptions, and optional still fully validates a
// session that is present: it just never answers 401.
//
// It is this side of the split's only call site of middleware.ValidateSession.
// The template chain's RequireLogin calls the same function and answers with a
// 302 instead. One implementation, wrapped twice.
func sessionMiddleware(h *handlers.Handler, loadUser appmw.UserLoader) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		mode := opAuth(ctx.Operation())
		if mode == authPublic {
			next(ctx)
			return
		}

		r, w := httpFrom(ctx.Context())
		// GET /api/v1/session is exempt from sliding-window renewal (5.3):
		// otherwise an open tab refetching on window focus keeps an idle
		// session alive forever.
		res := appmw.ValidateSession(h.Store(), loadUser, r, mode != authOptional)

		if res.Reason != 0 {
			if mode == authOptional {
				next(ctx)
				return
			}
			if res.Clear {
				res.Session.Values = map[interface{}]interface{}{}
				if res.Reason == appmw.RejectDecode {
					res.Session.Options.MaxAge = -1
				}
				_ = res.Session.Save(r, w)
			}
			writeProblem(w, http.StatusUnauthorized, "Unauthorized", "authentication required", r.URL.Path)
			return
		}

		// getSession saves its own session, because it also mints the CSRF
		// pair; saving here as well would put two Set-Cookie headers for the
		// same name on one response.
		if mode != authOptional {
			if err := res.Session.Save(r, w); err != nil {
				slog.Error("session save failed", "path", r.URL.Path, "err", err)
			}
		}
		setSessionExpires(w, res.Session)
		// The user rides on middleware's own context key, so roleMiddleware and
		// every handler read it with middleware.UserFrom exactly as the template
		// chain does, and this package never names step-ui/models.
		next(huma.WithContext(ctx, appmw.WithUser(ctx.Context(), res.User)))
	}
}

// roleMiddleware denies by default (5.5). An operation carrying neither a role
// nor an auth mode gets 403: "requires a logged-in session" is
// viewer-equivalent, and viewer-by-default on a certificate authority is
// fail-open.
func roleMiddleware(ctx huma.Context, next func(huma.Context)) {
	op := ctx.Operation()
	switch opAuth(op) {
	case authPublic, authOptional:
		next(ctx)
		return
	}

	r, w := httpFrom(ctx.Context())
	role, declared := opRole(op)
	if !declared {
		slog.Error("operation declares neither a role nor an auth mode", "operation", op.OperationID, "path", r.URL.Path)
		writeProblem(w, http.StatusForbidden, "Forbidden", "this operation declares no authorisation requirement", r.URL.Path)
		return
	}
	have, ok := appmw.RoleFrom(ctx.Context())
	if !ok || !appmw.RoleAllows(have, role) {
		writeProblem(w, http.StatusForbidden, "Forbidden", "insufficient role", r.URL.Path)
		return
	}
	next(ctx)
}

// csrfMiddleware requires the readable cookie's value to be echoed in
// X-CSRF-Token on every unsafe method, and to match the value held inside the
// encrypted session (5.4). Session-bound, so a token minted for one session is
// not interchangeable with another's.
func csrfMiddleware(h *handlers.Handler) func(huma.Context, func(huma.Context)) {
	return func(ctx huma.Context, next func(huma.Context)) {
		if safeMethod(ctx.Method()) {
			next(ctx)
			return
		}
		r, w := httpFrom(ctx.Context())

		sent := r.Header.Get(csrfHeaderName)
		var want string
		if s, err := h.Store().Get(r, h.SessionCookieName()); err == nil {
			want, _ = s.Values["csrf_token"].(string)
		}
		// An operation marked csrfOptional is checked only when the session
		// actually holds a token: see csrfWhenSession for why logout needs it.
		if want == "" {
			if mode, _ := ctx.Operation().Metadata[metaCSRF].(string); mode == csrfOptional {
				next(ctx)
				return
			}
		}
		if sent == "" || want == "" || subtle.ConstantTimeCompare([]byte(sent), []byte(want)) != 1 {
			writeCSRFProblem(w, r.URL.Path)
			return
		}
		next(ctx)
	}
}

// rateLimitMiddleware runs only for operations carrying Metadata["ratelimit"]
// (5.5). Applied globally, five bad logins from a shared corporate egress
// address would 429 every authenticated user on the installation. Retry-After
// is computed from the time remaining until the IP's oldest counted attempt
// ages out of the window, never echoed from input.
func rateLimitMiddleware(ctx huma.Context, next func(huma.Context)) {
	if scope, _ := ctx.Operation().Metadata[metaRateLimit].(string); scope != rateLimitAuth {
		next(ctx)
		return
	}
	r, w := httpFrom(ctx.Context())
	ip := appmw.ClientIP(r)
	if security.RL.IsBlocked(ip) {
		retrySecs := int(math.Ceil(security.RL.RetryAfter(ip).Seconds()))
		w.Header().Set("Retry-After", strconv.Itoa(retrySecs))
		writeRateLimitProblem(w, r.URL.Path, security.RL.Left(ip))
		return
	}
	next(ctx)
}

// setSessionExpires emits the idle-window deadline on every BasePath response
// carrying a validated session.
func setSessionExpires(w http.ResponseWriter, s *sessions.Session) {
	last, ok := s.Values["last_activity"].(int64)
	if !ok {
		last = time.Now().Unix()
	}
	w.Header().Set(sessionExpiresHeader, time.Unix(last, 0).Add(appmw.SessionTimeout).UTC().Format(time.RFC3339))
}

func safeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	}
	return false
}
