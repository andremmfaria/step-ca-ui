// Package middleware provides HTTP middleware for security headers, session
// authentication, and role-based access control.
package middleware

import (
	"context"
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

// RequireLogin checks that the user is authenticated
func RequireLogin(store *sessions.CookieStore, loadUser UserLoader) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			sess, err := store.Get(r, "step-ui")
			if err != nil {
				slog.Warn("session decode failed; redirecting to login", "remote", r.RemoteAddr, "host", r.Host, "path", r.URL.Path, "err", err)
				sess.Options.MaxAge = -1
				_ = sess.Save(r, w)
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			userID, ok := sess.Values["user_id"]
			if !ok || userID == nil {
				http.Redirect(w, r, "/login", http.StatusFound)
				return
			}
			now := time.Now()
			// Absolute session-lifetime cap: stamp created_at on first request and
			// reject the session once it exceeds SessionMaxLifetime regardless of
			// activity.  This limits the damage from a stolen session cookie.
			if created, ok := sess.Values["session_created_at"].(int64); ok {
				if now.Sub(time.Unix(created, 0)) > SessionMaxLifetime {
					sess.Values = map[interface{}]interface{}{}
					_ = sess.Save(r, w)
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
			} else {
				// Stamp the creation time on sessions that pre-date this check.
				sess.Values["session_created_at"] = now.Unix()
			}
			// Sliding-window idle timeout.
			if last, ok := sess.Values["last_activity"].(int64); ok {
				if now.Sub(time.Unix(last, 0)) > SessionTimeout {
					sess.Values = map[interface{}]interface{}{}
					_ = sess.Save(r, w)
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
			}
			sess.Values["last_activity"] = now.Unix()
			// The cookie store is client-side, so the checks above cannot see a
			// user who was deactivated, deleted or had their sessions revoked
			// since the cookie was minted. Re-read the row on every request.
			id, _ := userID.(int)
			user, err := loadUser(id)
			if err != nil || user == nil || !user.IsActive {
				slog.Warn("session rejected: user missing or inactive", "user_id", id, "err", err)
				rejectSession(w, r, sess)
				return
			}
			epoch, _ := sess.Values["session_epoch"].(int)
			if epoch != user.SessionEpoch {
				slog.Warn("session rejected: stale epoch", "user_id", id, "session_epoch", epoch, "user_epoch", user.SessionEpoch)
				rejectSession(w, r, sess)
				return
			}
			_ = sess.Save(r, w)
			next.ServeHTTP(w, r.WithContext(context.WithValue(r.Context(), ctxKeyUser, user)))
		})
	}
}

// rejectSession clears the session and sends the client back to the login page.
func rejectSession(w http.ResponseWriter, r *http.Request, sess *sessions.Session) {
	sess.Values = map[interface{}]interface{}{}
	_ = sess.Save(r, w)
	http.Redirect(w, r, "/login", http.StatusFound)
}

// RequireRole checks the user's role (viewer=1, manager=2, admin=3).
// The role comes from the user RequireLogin loaded, not from the session, so a
// demotion takes effect on the very next request.
func RequireRole(minRole string) func(http.Handler) http.Handler {
	roleLevel := map[string]int{"viewer": 1, "manager": 2, "admin": 3}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			user, ok := r.Context().Value(ctxKeyUser).(*models.User)
			if !ok || user == nil {
				// Only reachable if this middleware is mounted outside a
				// RequireLogin group, which is a routing bug: refuse.
				slog.Error("RequireRole reached with no authenticated user in context", "path", r.URL.Path)
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			if roleLevel[user.Role] < roleLevel[minRole] {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
