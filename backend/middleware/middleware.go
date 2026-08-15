// Package middleware provides HTTP middleware for security headers, session
// authentication, and role-based access control.
package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"step-ui/models"

	"github.com/gorilla/sessions"
)

// UserLoader returns the current database row for a session's user_id.
// RequireLogin takes it as a parameter rather than a *sql.DB so this package
// stays free of a database dependency and testable without one.
type UserLoader func(id int) (*models.User, error)

type ctxKey int

// ctxKeyUser holds the user RequireLogin loaded, so that middleware further
// down the chain does not query the database again.
const ctxKeyUser ctxKey = iota

// SessionTimeout is the idle (sliding-window) timeout.  A session that has
// not been active for this long is invalidated regardless of creation time.
const SessionTimeout = 8 * time.Hour

// SessionMaxLifetime is the absolute cap on session age.  Even an
// continuously-active session cannot outlive this duration after it was
// created.  This limits the blast radius of a stolen session cookie.
const SessionMaxLifetime = 24 * time.Hour

// SecurityHeaders adds security HTTP headers.
func SecurityHeaders(enableHSTS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			// X-XSS-Protection is deprecated in modern browsers and removed (P2-3).
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if enableHSTS {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			} else {
				w.Header().Set("Strict-Transport-Security", "max-age=0")
			}
			// All CSS/JS/fonts are served locally; Google-Fonts grants removed (P2-3).
			// 'unsafe-inline' removed from script-src (P2-3) and from style-src (W4-4)
			// after all inline <style> blocks were moved into static/css/pages.css.
			// No unsafe-inline remains anywhere in the CSP.
			// frame-ancestors/base-uri/form-action/object-src close the gaps
			// default-src does not cover: framing, <base href> hijacking,
			// off-origin form posts, and plugin content.
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self'; "+
					"style-src 'self'; "+
					"font-src 'self'; img-src 'self' data:; "+
					"object-src 'none'; base-uri 'self'; "+
					"form-action 'self'; frame-ancestors 'none';")
			// Global, not per-route: an allowlist goes stale every time a
			// sensitive route is added. main.go's static handler runs after
			// this chain and overwrites it for assets, which is intended.
			w.Header().Set("Cache-Control", "no-store")
			w.Header().Del("Server")
			next.ServeHTTP(w, r)
		})
	}
}

// RequireLogin is the template-route wrapper around ValidateSession: it
// answers every refusal with a 302 to /login, which is the contract the
// server-rendered UI has always had. The BasePath wrapper in api/ shares the
// same ValidateSession call and answers with a problem document instead
// (5.3). One implementation, wrapped twice.
func RequireLogin(store *sessions.CookieStore, loadUser UserLoader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			res := ValidateSession(store, loadUser, r, true)
			if res.Reason != 0 {
				if res.Clear {
					res.Session.Values = map[interface{}]interface{}{}
					if res.Reason == RejectDecode {
						res.Session.Options.MaxAge = -1
					}
					_ = res.Session.Save(r, w)
				}
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			_ = res.Session.Save(r, w)
			next.ServeHTTP(w, r.WithContext(WithUser(r.Context(), res.User)))
		})
	}
}

// RequireRole checks the user's role against RoleLevels.
// The role comes from the user RequireLogin loaded, not from the session, so a
// demotion takes effect on the very next request.
func RequireRole(minRole string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := UserFrom(r.Context())
			if !ok {
				// Only reachable if this middleware is mounted outside a
				// RequireLogin group, which is a routing bug: refuse.
				slog.Error("RequireRole reached with no authenticated user in context", "path", r.URL.Path)
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			if !RoleAllows(user.Role, minRole) {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
