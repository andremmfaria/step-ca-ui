package api

import (
	"context"
	"net/http"
	"time"

	"step-ui/handlers"
	appmw "step-ui/middleware"
	"step-ui/security"

	"github.com/gorilla/sessions"
)

// csrfCookieName is the readable sibling of the encrypted session cookie
// (5.4). Its value always matches session.Values["csrf_token"].
const csrfCookieName = "step-ui-csrf"

// SessionState is GET /api/v1/session's required discriminator (5.3): the
// endpoint answers 200 in all three cases and never 401.
type SessionState string

// The three values of SessionState.
const (
	SessionAnonymous     SessionState = "anonymous"
	SessionPendingMFA    SessionState = "pendingMfa"
	SessionAuthenticated SessionState = "authenticated"
)

type sessionUserBody struct {
	ID       int    `json:"id"`
	Username string `json:"username"`
	Role     string `json:"role"`
}

type sessionBody struct {
	State SessionState     `json:"state" enum:"anonymous,pendingMfa,authenticated"`
	User  *sessionUserBody `json:"user,omitempty"`
}

type sessionOutput struct {
	Body sessionBody
}

// setCSRFCookie mirrors handlers.Handler.csrf's readable half (5.4): same
// value as the encrypted session's csrf_token, same MaxAge, but readable by
// JavaScript so the SPA can echo it in X-CSRF-Token.
func setCSRFCookie(w http.ResponseWriter, h *handlers.Handler, token string) {
	opts := h.Store().Options
	http.SetCookie(w, &http.Cookie{ //nolint:gosec // G124: Secure mirrors opts.Secure (SESSION_SECURE), a runtime value gosec can't evaluate statically; HttpOnly:false is deliberate (5.4)
		Name:     csrfCookieName,
		Value:    token,
		Path:     "/",
		MaxAge:   opts.MaxAge,
		Secure:   opts.Secure,
		SameSite: http.SameSiteStrictMode,
		HttpOnly: false,
	})
}

// getSession implements GET /api/v1/session. It is the one auth: optional
// operation (5.5): the chain validates a present session and never answers
// 401, so this handler only reports the state and keeps the CSRF pair fresh.
func getSession(h *handlers.Handler) func(context.Context, *struct{}) (*sessionOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		r, w := httpFrom(ctx)

		s, err := h.Store().Get(r, handlers.SessionCookieName)
		if err != nil {
			s, _ = h.Store().New(r, handlers.SessionCookieName) //nolint:errcheck // New only errors on codec setup, never on a bad cookie
		}

		out := &sessionOutput{}
		switch {
		case isPending2FA(s):
			out.Body.State = SessionPendingMFA
		default:
			// sessionMiddleware ran with auth: optional, so it validated a
			// session if one was present and put the user on the context
			// without ever answering 401 (5.5). Nothing is re-checked here.
			if user, ok := appmw.UserFrom(ctx); ok {
				out.Body.State = SessionAuthenticated
				out.Body.User = &sessionUserBody{ID: user.ID, Username: user.Username, Role: user.Role}
			} else {
				out.Body.State = SessionAnonymous
			}
		}

		token, _ := s.Values["csrf_token"].(string)
		if token == "" {
			token = security.GenerateToken()
			s.Values["csrf_token"] = token
		}
		// The write this comment is about: persisting the session from inside
		// a huma operation handler, and the cookie carrying it back to the
		// client on this same response (D2 cost 2's go/no-go proof).
		if err := s.Save(r, w); err != nil {
			return nil, err
		}
		setCSRFCookie(w, h, token)

		return out, nil
	}
}

// isPending2FA mirrors handlers.Handler.pending2FAUserID's expiry check
// without exporting that unexported method.
func isPending2FA(s *sessions.Session) bool {
	uid, ok := s.Values["pending_2fa_user_id"].(int)
	if !ok || uid <= 0 {
		return false
	}
	exp, _ := s.Values["pending_2fa_expires"].(int64)
	return exp == 0 || time.Now().Unix() <= exp
}
