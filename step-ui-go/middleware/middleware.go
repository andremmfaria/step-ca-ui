package middleware

import (
	"log"
	"net/http"
	"time"

	"github.com/gorilla/sessions"
)

const SessionTimeout = 8 * time.Hour

// SecurityHeaders добавляет security HTTP заголовки.
func SecurityHeaders(enableHSTS bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("X-Frame-Options", "DENY")
			w.Header().Set("X-Content-Type-Options", "nosniff")
			w.Header().Set("X-XSS-Protection", "1; mode=block")
			w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
			if enableHSTS {
				w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
			} else {
				w.Header().Set("Strict-Transport-Security", "max-age=0")
			}
			w.Header().Set("Content-Security-Policy",
				"default-src 'self'; script-src 'self' 'unsafe-inline'; "+
					"style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; "+
					"font-src 'self' https://fonts.gstatic.com; img-src 'self' data:;")
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
				log.Printf("session decode failed: remote=%s host=%s path=%s err=%v", r.RemoteAddr, r.Host, r.URL.Path, err)
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
			// Проверяем таймаут сессии
			if last, ok := sess.Values["last_activity"].(int64); ok {
				if time.Since(time.Unix(last, 0)) > SessionTimeout {
					sess.Values = map[interface{}]interface{}{}
					_ = sess.Save(r, w)
					http.Redirect(w, r, "/login", http.StatusFound)
					return
				}
			}
			sess.Values["last_activity"] = time.Now().Unix()
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
				log.Printf("session decode failed: remote=%s host=%s path=%s err=%v", r.RemoteAddr, r.Host, r.URL.Path, err)
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
