package handlers

import (
	"context"
	"html/template"
	"net/http"
	"net/http/httptest"
	"step-ui/config"
	"step-ui/middleware"
	"step-ui/security"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/sessions"
)

// newTestHandler wires a Handler with no DB and no loaded templates.  Callers
// that trigger h.render() will get "template not found" but that is OK — we
// test the redirect / status-code paths, not the rendered HTML.
func newTestHandler(cfg *config.Config, store *sessions.CookieStore) *Handler {
	return &Handler{
		db:    nil,
		cfg:   cfg,
		store: store,
		tmpls: make(map[string]*template.Template),
	}
}

// newTestHandlerWithTemplate wires a Handler with a minimal stub template so
// that render() does not short-circuit with 500.
func newTestHandlerWithTemplate(cfg *config.Config, store *sessions.CookieStore, pages ...string) *Handler {
	h := newTestHandler(cfg, store)
	for _, page := range pages {
		// Minimal template: just emit a fixed string so render() succeeds.
		t := template.Must(template.New("layout").Parse(`{{define "layout"}}OK{{end}}`))
		if page == "login" {
			t = template.Must(template.New("login.html").Parse(`{{define "login.html"}}OK{{end}}`))
		}
		h.tmpls[page] = t
	}
	return h
}

// testReq builds a request with a background context.
func testReq(method, path, body string) *http.Request {
	var bodyReader *strings.Reader
	if body != "" {
		bodyReader = strings.NewReader(body)
	} else {
		bodyReader = strings.NewReader("")
	}
	req := httptest.NewRequestWithContext(context.Background(), method, path, bodyReader)
	if method == "POST" {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}
	return req
}

// seedCSRF injects a CSRF token into the session and returns the cookies + token.
func seedCSRF(t *testing.T, store *sessions.CookieStore) ([]*http.Cookie, string) {
	t.Helper()
	token := security.GenerateToken()
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"csrf_token": token,
	})
	return cookies, token
}

// ─── LoginPost — pre-DB paths ─────────────────────────────────────────────────

// TestLoginPost_LocalLoginDisabledOIDC — when local login is disabled and OIDC
// is enabled, LoginPost redirects to the OIDC login path.
func TestLoginPost_LocalLoginDisabledOIDC(t *testing.T) {
	store := testStore()
	cfg := &config.Config{ //nolint:gosec // G101: test-only key
		SecretKey:         "a-long-test-secret-key-32charsXX",
		LocalLoginEnabled: false,
		OIDCEnabled:       true,
	}
	h := newTestHandler(cfg, store)
	req := testReq("POST", "/login", "username=alice&password=pw")
	rr := httptest.NewRecorder()
	h.LoginPost(rr, req)
	if rr.Code != http.StatusFound {
		t.Errorf("expected 302, got %d", rr.Code)
	}
	if loc := rr.Header().Get("Location"); loc != "/auth/oidc/login" {
		t.Errorf("expected redirect to /auth/oidc/login, got %q", loc)
	}
}

// TestLoginPost_LocalLoginDisabledNoOIDC — when both are off, renders login
// with an error (uses nil db but render() returns 500 — we just check it
// doesn't panic and returns non-redirect).
func TestLoginPost_LocalLoginDisabledNoOIDC(t *testing.T) {
	store := testStore()
	cfg := &config.Config{ //nolint:gosec // G101: test-only key
		SecretKey:         "a-long-test-secret-key-32charsXX",
		LocalLoginEnabled: false,
		OIDCEnabled:       false,
	}
	h := newTestHandlerWithTemplate(cfg, store, "login")
	req := testReq("POST", "/login", "")
	rr := httptest.NewRecorder()
	h.LoginPost(rr, req)
	// Should render login page (not a redirect).
	if rr.Code == http.StatusFound {
		t.Errorf("expected render, got redirect to %q", rr.Header().Get("Location"))
	}
}

// TestLoginPost_RateLimited — when IP is blocked, LoginPost renders the blocked
// state (no redirect, no DB call).
func TestLoginPost_RateLimited(t *testing.T) {
	store := testStore()
	cfg := &config.Config{ //nolint:gosec // G101: test-only key
		SecretKey:         "a-long-test-secret-key-32charsXX",
		LocalLoginEnabled: true,
	}
	h := newTestHandlerWithTemplate(cfg, store, "login")

	// Block the IP by force-registering LimitCount times.
	rl := security.NewRateLimiter()
	for range security.LimitCount {
		rl.Register("192.0.2.99")
	}
	// Swap in the custom limiter via the global (reset afterwards).
	orig := security.RL
	security.RL = rl
	t.Cleanup(func() { security.RL = orig })

	req := testReq("POST", "/login", "username=alice&password=pw")
	req.RemoteAddr = "192.0.2.99:1234"
	rr := httptest.NewRecorder()
	h.LoginPost(rr, req)
	if rr.Code == http.StatusFound {
		t.Errorf("blocked IP should render, not redirect")
	}
}

// TestLoginPost_BadCSRF — CSRF check fails → redirect to /login.
func TestLoginPost_BadCSRF(t *testing.T) {
	store := testStore()
	cfg := &config.Config{ //nolint:gosec // G101: test-only key
		SecretKey:         "a-long-test-secret-key-32charsXX",
		LocalLoginEnabled: true,
	}
	h := newTestHandlerWithTemplate(cfg, store, "login")

	// Session has a CSRF token but the form sends a wrong one.
	cookies, _ := seedCSRF(t, store)
	req := testReq("POST", "/login", "username=alice&password=pw&csrf_token=WRONG")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	h.LoginPost(rr, req)
	// CSRF failure renders an error page (not redirecting to a different path).
	// Status may be 200 (render) — we confirm it is NOT a successful login redirect.
	if rr.Code == http.StatusFound && rr.Header().Get("Location") == "/" {
		t.Error("CSRF failure should not redirect to /")
	}
}

// ─── pending2FAUserID ─────────────────────────────────────────────────────────

// TestPending2FAUserID_Set — session has a valid pending_2fa_user_id.
func TestPending2FAUserID_Set(t *testing.T) {
	store := testStore()
	h := newTestHandler(testConfig(""), store)
	future := time.Now().Add(5 * time.Minute).Unix()
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"pending_2fa_user_id": 42,
		"pending_2fa_expires": future,
	})
	req := testReq("GET", "/login", "")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if uid := h.pending2FAUserID(req); uid != 42 {
		t.Errorf("pending2FAUserID: got %d want 42", uid)
	}
}

// TestPending2FAUserID_Expired — pending_2fa_expires is in the past.
func TestPending2FAUserID_Expired(t *testing.T) {
	store := testStore()
	h := newTestHandler(testConfig(""), store)
	past := time.Now().Add(-1 * time.Minute).Unix()
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"pending_2fa_user_id": 7,
		"pending_2fa_expires": past,
	})
	req := testReq("GET", "/login", "")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if uid := h.pending2FAUserID(req); uid != 0 {
		t.Errorf("expired 2FA pending: got %d want 0", uid)
	}
}

// TestPending2FAUserID_NotSet — no pending_2fa_user_id in session.
func TestPending2FAUserID_NotSet(t *testing.T) {
	store := testStore()
	h := newTestHandler(testConfig(""), store)
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"user_id": 1,
	})
	req := testReq("GET", "/login", "")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if uid := h.pending2FAUserID(req); uid != 0 {
		t.Errorf("no 2FA pending: got %d want 0", uid)
	}
}

// ─── RBAC via RequireRole middleware ──────────────────────────────────────────

// TestRequireRole_AdminCanAccessAdminRoute confirms the middleware integration
// tested end-to-end: admin session → 200; viewer session → 403.
func TestRequireRole_AdminCanAccessAdminRoute(t *testing.T) {
	store := testStore()
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})
	handler := middleware.RequireRole("admin", store)(inner)

	for _, tc := range []struct {
		role string
		want int
	}{
		{"admin", http.StatusOK},
		{"manager", http.StatusForbidden},
		{"viewer", http.StatusForbidden},
		{"", http.StatusForbidden},
	} {
		t.Run("role_"+tc.role, func(t *testing.T) {
			cookies := injectSession(t, store, map[interface{}]interface{}{
				"role": tc.role,
			})
			req := httptest.NewRequestWithContext(context.Background(), "GET", "/admin", nil)
			for _, c := range cookies {
				req.AddCookie(c)
			}
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)
			if rr.Code != tc.want {
				t.Errorf("role=%q: got %d want %d", tc.role, rr.Code, tc.want)
			}
		})
	}
}

// TestCSRFOK confirms csrfOK returns true only when form token matches session.
func TestCSRFOK(t *testing.T) {
	store := testStore()
	h := newTestHandler(testConfig(""), store)
	token := "abc123"
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"csrf_token": token,
	})

	// Correct token.
	req := testReq("POST", "/", "csrf_token="+token)
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if !h.csrfOK(req) {
		t.Error("csrfOK should return true for matching token")
	}

	// Wrong token.
	req2 := testReq("POST", "/", "csrf_token=WRONG")
	for _, c := range cookies {
		req2.AddCookie(c)
	}
	if h.csrfOK(req2) {
		t.Error("csrfOK should return false for mismatched token")
	}
}

// TestCSRFOK_Empty confirms that an empty form token is always rejected.
func TestCSRFOK_Empty(t *testing.T) {
	store := testStore()
	h := newTestHandler(testConfig(""), store)
	cookies := injectSession(t, store, map[interface{}]interface{}{
		"csrf_token": "",
	})
	req := testReq("POST", "/", "csrf_token=")
	for _, c := range cookies {
		req.AddCookie(c)
	}
	if h.csrfOK(req) {
		t.Error("csrfOK should reject empty tokens")
	}
}
