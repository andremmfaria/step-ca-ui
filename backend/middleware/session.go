package middleware

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	"step-ui/models"

	"github.com/gorilla/sessions"
)

// SessionCookieName is the name RequireLogin reads. It is declared here
// rather than taken from handlers/ because middleware/ must stay free of a
// dependency on that package.
const SessionCookieName = "step-ui"

// RejectReason says why a session was refused, so the two callers of
// validateSession can answer in their own contract: a 302 to /login for the
// template routes, a problem document for BasePath (5.3). One
// implementation, wrapped twice.
type RejectReason int

// The reasons validateSession can refuse a session.
const (
	// RejectDecode means the cookie failed to decode; the caller must clear it.
	RejectDecode RejectReason = iota + 1
	// RejectAnonymous means there is no user_id at all.
	RejectAnonymous
	// RejectExpired means the absolute lifetime or the idle window elapsed.
	RejectExpired
	// RejectUser means the row is missing, inactive, or its epoch moved.
	RejectUser
)

// SessionResult is validateSession's outcome. Exactly one of User and Reason
// is meaningful: Reason is zero on success.
type SessionResult struct {
	Session *sessions.Session
	User    *models.User
	Reason  RejectReason
	// Clear is true when the caller must wipe and re-save the session before
	// answering. It is false for a plain anonymous request, which has nothing
	// to wipe.
	Clear bool
}

// ValidateSession is the single implementation of 5.3's session contract:
// decode, absolute lifetime, idle window, row reload, IsActive, session_epoch.
//
// slide controls the sliding-window renewal. Every route passes true except
// GET /api/v1/session, which is exempt: otherwise an open tab with
// refetchOnWindowFocus would keep an idle session alive forever.
//
// It performs no writes and emits no response. Persisting the session and
// choosing the refusal shape both belong to the caller.
func ValidateSession(store *sessions.CookieStore, loadUser UserLoader, r *http.Request, slide bool) SessionResult {
	sess, err := store.Get(r, SessionCookieName)
	if err != nil {
		slog.Warn("session decode failed", "host", r.Host, "path", r.URL.Path, "err", err)
		return SessionResult{Session: sess, Reason: RejectDecode, Clear: true}
	}

	userID, ok := sess.Values["user_id"]
	if !ok || userID == nil {
		return SessionResult{Session: sess, Reason: RejectAnonymous}
	}

	now := time.Now()
	// Absolute session-lifetime cap: stamp created_at on first request and
	// reject once it exceeds SessionMaxLifetime regardless of activity.
	if created, ok := sess.Values["session_created_at"].(int64); ok {
		if now.Sub(time.Unix(created, 0)) > SessionMaxLifetime {
			return SessionResult{Session: sess, Reason: RejectExpired, Clear: true}
		}
	} else {
		sess.Values["session_created_at"] = now.Unix()
	}
	if last, ok := sess.Values["last_activity"].(int64); ok {
		if now.Sub(time.Unix(last, 0)) > SessionTimeout {
			return SessionResult{Session: sess, Reason: RejectExpired, Clear: true}
		}
	}
	if slide {
		sess.Values["last_activity"] = now.Unix()
	}

	// The cookie store is client-side, so the checks above cannot see a user
	// who was deactivated, deleted or had their sessions revoked since the
	// cookie was minted. Re-read the row on every request.
	id, _ := userID.(int)
	user, err := loadUser(id)
	if err != nil || user == nil || !user.IsActive {
		slog.Warn("session rejected: user missing or inactive", "user_id", id, "err", err)
		return SessionResult{Session: sess, Reason: RejectUser, Clear: true}
	}
	epoch, _ := sess.Values["session_epoch"].(int)
	if epoch != user.SessionEpoch {
		slog.Warn("session rejected: stale epoch", "user_id", id, "session_epoch", epoch, "user_epoch", user.SessionEpoch)
		return SessionResult{Session: sess, Reason: RejectUser, Clear: true}
	}

	return SessionResult{Session: sess, User: user}
}

// WithUser puts a loaded user on a context. It exists because ctxKeyUser is
// unexported and api/ needs to write the same key the template chain reads
// (5.5), so that handlers reached from either path see one context shape.
func WithUser(ctx context.Context, user *models.User) context.Context {
	return context.WithValue(ctx, ctxKeyUser, user)
}

// UserFrom recovers the user WithUser or RequireLogin stored.
func UserFrom(ctx context.Context) (*models.User, bool) {
	user, ok := ctx.Value(ctxKeyUser).(*models.User)
	return user, ok && user != nil
}

// RoleLevels is the single ranking of the three roles. RequireRole and
// GET /api/v1/config both read it, and the role golden table is checked
// against it, so a role added here cannot be missed by either.
var RoleLevels = map[string]int{"viewer": 1, "manager": 2, "admin": 3}

// RoleAllows reports whether have satisfies want under RoleLevels. An
// unknown want is never satisfied, so a typo in a role name denies rather
// than admits.
func RoleAllows(have, want string) bool {
	wantLevel, ok := RoleLevels[want]
	if !ok {
		return false
	}
	return RoleLevels[have] >= wantLevel
}

// RoleFrom returns the role of the user WithUser stored. It exists so that
// api/ can enforce a role without naming step-ui/models, which Section 2's
// depguard allowlist forbids it from importing.
func RoleFrom(ctx context.Context) (string, bool) {
	user, ok := UserFrom(ctx)
	if !ok {
		return "", false
	}
	return user.Role, true
}
