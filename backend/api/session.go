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

// getSession implements GET /api/v1/session. It is the one auth: optional
// operation (5.5): the chain validates a present session and never answers
// 401, so this handler only reports the state and keeps the CSRF pair fresh.
func getSession(h *handlers.Handler) func(context.Context, *struct{}) (*sessionOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*sessionOutput, error) {
		r, w := httpFrom(ctx)

		s, err := h.Store().Get(r, h.SessionCookieName())
		if err != nil {
			s, _ = h.Store().New(r, h.SessionCookieName()) //nolint:errcheck // New only errors on codec setup, never on a bad cookie
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
		h.SetCSRFCookie(w, token)

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

// deleteSessionOutput carries no body: the response is a 204.
type deleteSessionOutput struct {
	Status int
}

// deleteSession implements DELETE /api/v1/session. It expires both halves of
// the pair (5.3): leaving the readable CSRF cookie behind would have the SPA
// echo a token for a session that no longer exists, which reads as a CSRF
// failure rather than as a logged-out state.
//
// It is auth: public because logging out must work from a session the server
// has already rejected. There is nothing to authorise: the only effect is
// expiring the caller's own cookies.
func deleteSession(h *handlers.Handler) func(context.Context, *struct{}) (*deleteSessionOutput, error) {
	return func(ctx context.Context, _ *struct{}) (*deleteSessionOutput, error) {
		r, w := httpFrom(ctx)

		if s, err := h.Store().Get(r, h.SessionCookieName()); err == nil {
			s.Options.MaxAge = -1
			s.Values = map[interface{}]interface{}{}
			if err := s.Save(r, w); err != nil {
				return nil, err
			}
		} else {
			// An undecodable cookie still has to be cleared, or the caller is
			// stuck sending a cookie the server rejects on every request.
			http.SetCookie(w, expiredCookie(h.SessionCookieName(), h.Store().Options.Secure, true))
		}
		http.SetCookie(w, expiredCookie(h.CSRFCookieName(), h.Store().Options.Secure, false))

		return &deleteSessionOutput{Status: http.StatusNoContent}, nil
	}
}

// expiredCookie builds the deletion form of a cookie. The attributes must
// match the ones it was set with or the browser keeps the original, and with
// the __Host- prefix Path=/ and Secure are required for the delete to be
// accepted at all.
func expiredCookie(name string, secure, httpOnly bool) *http.Cookie {
	return &http.Cookie{ //nolint:gosec // G124: Secure mirrors the store (SESSION_SECURE), a runtime value
		Name:     name,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		Secure:   secure,
		SameSite: http.SameSiteStrictMode,
		HttpOnly: httpOnly,
	}
}
