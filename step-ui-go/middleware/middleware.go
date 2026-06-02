package middleware

import (
	"log/slog"
	"net/http"
	"time"

	"github.com/gorilla/sessions"
)

// SessionTimeout is the idle (sliding-window) timeout.  A session that has
// not been active for this long is invalidated regardless of creation time.
const SessionTimeout = 8 * time.Hour

// SessionMaxLifetime is the absolute cap on session age.  Even an
// continuously-active session cannot outlive this duration after it was
// created.  This limits the blast radius of a stolen session cookie.
const SessionMaxLifetime = 24 * time.Hour

// SecurityHeaders добавляет security HTTP заголовки.
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
			// 'unsafe-inline' removed from script-src after externalising all inline
			// scripts (P2-3).
			// style-src retains 'unsafe-inline' because 10+ templates contain inline
			// <style> blocks (login.html, history.html, le_issue.html, …).
			// Removing it requires externalising ~150 lines of per-page CSS into
			// static/css/ — tracked as Wave 4 frontend cleanup.  Until then, the CSP
			// mitigates XSS only for script injection, not style injection.
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self'; "+
					"style-src 'self' 'unsafe-inline'; "+
					"font-src 'self'; img-src 'self' data:;")
			w.Header().Del("Server")
			next.ServeHTTP(w, r)
		})
	}
}

// RequireLogin проверяет что пользователь авторизован
func RequireLogin(store *sessions.CookieStore) func(http.Handler) http.Handler {
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
			_ = sess.Save(r, w)
			next.ServeHTTP(w, r)
		})
	}
}

// RequireRole проверяет роль пользователя (viewer=1, manager=2, admin=3)
func RequireRole(minRole string, store *sessions.CookieStore) func(http.Handler) http.Handler {
	roleLevel := map[string]int{"viewer": 1, "manager": 2, "admin": 3}
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
			role, _ := sess.Values["role"].(string)
			if roleLevel[role] < roleLevel[minRole] {
				http.Error(w, "403 Forbidden", http.StatusForbidden)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}
