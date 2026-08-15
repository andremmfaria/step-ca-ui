package middleware

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"step-ui/models"

	"github.com/gorilla/sessions"
)

// errNoSuchUser stands in for the sql.ErrNoRows a real loader returns for a
// deleted account.
var errNoSuchUser = errors.New("no such user")

// activeUser builds an active user row with the zero session epoch.
func activeUser(id int, role string) *models.User {
	return &models.User{ID: id, Username: "u", Role: role, IsActive: true}
}

// fakeLoader serves the given users by ID and reports every other ID missing.
func fakeLoader(users ...*models.User) UserLoader {
	byID := make(map[int]*models.User, len(users))
	for _, u := range users {
		byID[u.ID] = u
	}
	return func(id int) (*models.User, error) {
		u, ok := byID[id]
		if !ok {
			return nil, errNoSuchUser
		}
		return u, nil
	}
}

// withUser puts a user in the request context the way RequireLogin does.
func withUser(req *http.Request, u *models.User) *http.Request {
	return req.WithContext(context.WithValue(req.Context(), ctxKeyUser, u))
}

// newTestStore returns a CookieStore suitable for tests.
func newTestStore() *sessions.CookieStore {
	return sessions.NewCookieStore(
		[]byte("test-hash-key-32charsXXXXXXXXXXX"),
		[]byte("test-block-key16"),
	)
}

// newReq is a test helper that creates a request with a background context,
// satisfying the noctx linter requirement for test code.
//
//nolint:unparam // method is always "GET" in this file but kept for readability
func newReq(method, path string) *http.Request {
	return httptest.NewRequestWithContext(context.Background(), method, path, nil)
}

// injectSessionMiddleware saves session values and returns cookies for the next request.
func injectSessionMiddleware(t *testing.T, store *sessions.CookieStore, values map[interface{}]interface{}) []*http.Cookie {
	t.Helper()
	req := newReq("GET", "/")
	rr := httptest.NewRecorder()
	sess, _ := store.New(req, SessionCookieName(store))
	for k, v := range values {
		sess.Values[k] = v
	}
	if err := sess.Save(req, rr); err != nil {
		t.Fatalf("save session: %v", err)
	}
	return rr.Result().Cookies()
}

// applyRequestCookies copies cookies onto a request.
func applyRequestCookies(req *http.Request, cookies []*http.Cookie) {
	for _, c := range cookies {
		req.AddCookie(c)
	}
}

// ─── SecurityHeaders ───────────────────────────────────────────────────────────

func TestSecurityHeaders_Present(t *testing.T) {
	handler := SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	want := map[string]string{
		"X-Frame-Options":        "DENY",
		"X-Content-Type-Options": "nosniff",
		"Referrer-Policy":        "strict-origin-when-cross-origin",
	}
	for header, val := range want {
		if got := rr.Header().Get(header); got != val {
			t.Errorf("%s: got %q want %q", header, got, val)
		}
	}
}

func TestSecurityHeaders_HSTSEnabled(t *testing.T) {
	handler := SecurityHeaders(true)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	hsts := rr.Header().Get("Strict-Transport-Security")
	if hsts == "" || hsts == "max-age=0" {
		t.Errorf("expected HSTS header to be set when enableHSTS=true, got %q", hsts)
	}
}

func TestSecurityHeaders_HSTSDisabled(t *testing.T) {
	handler := SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	hsts := rr.Header().Get("Strict-Transport-Security")
	if hsts != "max-age=0" {
		t.Errorf("expected HSTS=max-age=0 when disabled, got %q", hsts)
	}
}

func TestSecurityHeaders_CSP(t *testing.T) {
	handler := SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	csp := rr.Header().Get("Content-Security-Policy")
	if csp == "" {
		t.Fatal("Content-Security-Policy header missing")
	}
	// unsafe-inline must not appear in script-src (P2-3 requirement).
	for _, tok := range []string{
		"script-src 'self'",
		"object-src 'none'",
		"base-uri 'self'",
		"form-action 'self'",
		"frame-ancestors 'none'",
	} {
		found := false
		for i := range len(csp) - len(tok) + 1 {
			if csp[i:i+len(tok)] == tok {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("CSP %q missing expected token %q", csp, tok)
		}
	}
}

func TestSecurityHeaders_NoXXSSProtection(t *testing.T) {
	handler := SecurityHeaders(false)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if got := rr.Header().Get("X-XSS-Protection"); got != "" {
		t.Errorf("X-XSS-Protection should be absent (deprecated), got %q", got)
	}
}

// ─── RequireLogin ──────────────────────────────────────────────────────────────

func TestRequireLogin_NoSession_Redirects(t *testing.T) {
	store := newTestStore()
	handler := RequireLogin(store, fakeLoader())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
}

func TestRequireLogin_WithValidSession_Passes(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       1,
		"last_activity": time.Now().Unix(),
	})

	handler := RequireLogin(store, fakeLoader(activeUser(1, "viewer")))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
}

// TestRequireLogin_AbsoluteLifetimeExpired verifies that a session past
// SessionMaxLifetime is rejected even when last_activity is recent.
func TestRequireLogin_AbsoluteLifetimeExpired(t *testing.T) {
	store := newTestStore()
	// session_created_at is more than 24 h ago → absolute cap exceeded.
	oldCreatedAt := time.Now().Add(-(SessionMaxLifetime + time.Minute)).Unix()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":            42,
		"session_created_at": oldCreatedAt,
		"last_activity":      time.Now().Unix(), // recently active — must still be rejected
	})

	handler := RequireLogin(store, fakeLoader(activeUser(42, "viewer")))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("absolute lifetime exceeded: expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
}

// TestRequireLogin_FreshSession_AbsoluteLifetimeNotExpired confirms a brand-new
// session with a recent created_at timestamp passes the absolute-lifetime check.
func TestRequireLogin_FreshSession_AbsoluteLifetimeNotExpired(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":            99,
		"session_created_at": time.Now().Unix(),
		"last_activity":      time.Now().Unix(),
	})

	handler := RequireLogin(store, fakeLoader(activeUser(99, "viewer")))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("fresh session: expected 200, got %d", rr.Code)
	}
}

func TestRequireLogin_ExpiredSession_Redirects(t *testing.T) {
	store := newTestStore()
	expired := time.Now().Add(-(SessionTimeout + time.Minute)).Unix()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       42,
		"last_activity": expired,
	})

	handler := RequireLogin(store, fakeLoader(activeUser(42, "viewer")))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("expected 302 for expired session, got %d", rr.Code)
	}
}

// ─── RequireRole ───────────────────────────────────────────────────────────────

func TestRequireRole_SufficientRole_Passes(t *testing.T) {
	for _, minRole := range []string{"viewer", "manager", "admin"} {
		t.Run("admin_satisfies_"+minRole, func(t *testing.T) {
			handler := RequireRole(minRole)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := withUser(newReq("GET", "/"), activeUser(1, "admin"))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusOK {
				t.Errorf("minRole=%s: expected 200, got %d", minRole, rr.Code)
			}
		})
	}
}

func TestRequireRole_InsufficientRole_Forbidden(t *testing.T) {
	cases := []struct {
		role    string
		minRole string
	}{
		{"viewer", "manager"},
		{"viewer", "admin"},
		{"manager", "admin"},
	}
	for _, tc := range cases {
		t.Run(tc.role+"_vs_"+tc.minRole, func(t *testing.T) {
			handler := RequireRole(tc.minRole)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusOK)
			}))
			req := withUser(newReq("GET", "/"), activeUser(1, tc.role))
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != http.StatusForbidden {
				t.Errorf("role=%s minRole=%s: expected 403, got %d", tc.role, tc.minRole, rr.Code)
			}
		})
	}
}

func TestRequireRole_EmptyRole_Forbidden(t *testing.T) {
	handler := RequireRole("viewer")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := withUser(newReq("GET", "/"), activeUser(99, ""))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("empty role: expected 403, got %d", rr.Code)
	}
}

func TestRequireRole_UnknownRole_Forbidden(t *testing.T) {
	handler := RequireRole("viewer")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := withUser(newReq("GET", "/"), activeUser(1, "superuser"))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("unknown role: expected 403, got %d", rr.Code)
	}
}

// TestRequireRole_NoUserInContext_Forbidden covers the fail-closed branch taken
// when RequireRole is mounted outside a RequireLogin group.
func TestRequireRole_NoUserInContext_Forbidden(t *testing.T) {
	handler := RequireRole("viewer")(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, newReq("GET", "/"))
	if rr.Code != http.StatusForbidden {
		t.Errorf("no user in context: expected 403, got %d", rr.Code)
	}
}

// TestRequireLogin_BadCookie confirms a tampered / unreadable cookie causes a
// redirect to /login (the session decode-failure branch).
func TestRequireLogin_BadCookie_Redirects(t *testing.T) {
	store := newTestStore()
	handler := RequireLogin(store, fakeLoader())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	// Inject a cookie with the right name but garbage value so gorilla sessions
	// fails to decode it, exercising the err != nil branch.
	//nolint:gosec // G124: test-only cookie intentionally missing Secure/HttpOnly attributes
	req.AddCookie(&http.Cookie{Name: SessionCookieName(store), Value: "not-a-valid-encoded-session"})
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)
	if rr.Code != http.StatusFound {
		t.Errorf("expected 302 on bad cookie, got %d", rr.Code)
	}
}

// ─── Session revocation (V3, V5, V8) ──────────────────────────────────────────

// TestRequireLogin_StaleEpoch_Rejected covers a cookie captured before the
// user's session epoch was bumped (logout, password change, role change).
func TestRequireLogin_StaleEpoch_Rejected(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       7,
		"session_epoch": 3,
		"last_activity": time.Now().Unix(),
	})

	user := activeUser(7, "admin")
	user.SessionEpoch = 4

	handler := RequireLogin(store, fakeLoader(user))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("stale epoch: expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/login" {
		t.Errorf("expected redirect to /login, got %q", loc)
	}
}

// TestRequireLogin_MatchingEpoch_Passes is the control for the test above.
func TestRequireLogin_MatchingEpoch_Passes(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       7,
		"session_epoch": 4,
		"last_activity": time.Now().Unix(),
	})

	user := activeUser(7, "admin")
	user.SessionEpoch = 4

	handler := RequireLogin(store, fakeLoader(user))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("matching epoch: expected 200, got %d", rr.Code)
	}
}

// TestRequireLogin_DeactivatedUser_Rejected covers V5: is_active=false must end
// the session on the next request, not at cookie expiry.
func TestRequireLogin_DeactivatedUser_Rejected(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       11,
		"last_activity": time.Now().Unix(),
	})

	user := activeUser(11, "admin")
	user.IsActive = false

	handler := RequireLogin(store, fakeLoader(user))(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("deactivated user: expected 302, got %d", rr.Code)
	}
}

// TestRequireLogin_DeletedUser_Rejected covers the deleted-account half of V5:
// the row is gone, so the loader errors.
func TestRequireLogin_DeletedUser_Rejected(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       404,
		"last_activity": time.Now().Unix(),
	})

	handler := RequireLogin(store, fakeLoader())(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	req := newReq("GET", "/protected")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("deleted user: expected 302, got %d", rr.Code)
	}
}

// TestRequireRole_FollowsDemotion asserts the role gate reads the live user row:
// a cookie minted while the user was an admin loses admin routes as soon as the
// database says viewer.
func TestRequireRole_FollowsDemotion(t *testing.T) {
	store := newTestStore()
	cookies := injectSessionMiddleware(t, store, map[interface{}]interface{}{
		"user_id":       5,
		"role":          "admin",
		"last_activity": time.Now().Unix(),
	})

	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	// UpdateUserRole bumps the epoch too, but the session predates that column
	// for pre-existing cookies, so the role check must stand on its own.
	handler := RequireLogin(store, fakeLoader(activeUser(5, "viewer")))(RequireRole("admin")(inner))

	req := newReq("GET", "/admin")
	applyRequestCookies(req, cookies)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusForbidden {
		t.Errorf("demoted user on an admin route: expected 403, got %d", rr.Code)
	}
}
