package api_test

import (
	"bytes"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestCSRF_MissingHeaderIsForbidden asserts an authenticated mutation with no
// X-CSRF-Token is refused. Without this the readable cookie is decoration: the
// session cookie alone would authorise the write (5.4).
func TestCSRF_MissingHeaderIsForbidden(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	buf, contentType := multipartUploadBody(t, true)
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/api/v1/_spike/upload", buf, func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Content-Type", contentType)
	})

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// The distinguishable type is what lets the SPA re-fetch a token and retry
	// rather than logging the user out as it would on a generic 403.
	if got, _ := body["type"].(string); !strings.HasSuffix(got, "/csrf") {
		t.Fatalf("type = %q, want a .../csrf problem type", got)
	}
}

// TestCSRF_WrongTokenIsForbidden asserts the check compares against the value
// inside the encrypted session, so a token from anywhere else does not pass.
func TestCSRF_WrongTokenIsForbidden(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	buf, contentType := multipartUploadBody(t, true)
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/api/v1/_spike/upload", buf, func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("X-CSRF-Token", "a-token-from-somewhere-else")
	})

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", resp.StatusCode)
	}
}

// TestRole_InsufficientIsForbidden asserts the role gate denies rather than
// admitting. The session is valid; only the role is short.
func TestRole_InsufficientIsForbidden(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	original := testUser.Role
	testUser.Role = "viewer"
	t.Cleanup(func() { testUser.Role = original })

	buf, contentType := multipartUploadBody(t, true)
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/api/v1/_spike/upload", buf, func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Content-Type", contentType)
		r.Header.Set("X-CSRF-Token", testCSRFToken)
	})

	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status = %d, want 403 (viewer must not reach a manager operation)", resp.StatusCode)
	}
}

// TestSessionEpochBump_IsUnauthorized is the exit criterion for revocation:
// bumping the user's session_epoch invalidates every cookie already minted,
// and on a BasePath route that must be a 401 problem document, never a 302.
func TestSessionEpochBump_IsUnauthorized(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	if resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/_spike/blob", nil, nil); resp.StatusCode != http.StatusOK { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		t.Fatalf("before bump: status = %d, want 200", resp.StatusCode)
	}

	original := testUser.SessionEpoch
	testUser.SessionEpoch = original + 1
	t.Cleanup(func() { testUser.SessionEpoch = original })

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/_spike/blob", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("after bump: status = %d, want 401", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
	if loc := resp.Header.Get("Location"); loc != "" {
		t.Fatalf("Location = %q; a BasePath route must never redirect (5.2)", loc)
	}
}

// TestInactiveUser_IsUnauthorized covers the other revocation path: the row is
// re-read on every request, so deactivation takes effect immediately.
func TestInactiveUser_IsUnauthorized(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	testUser.IsActive = false
	t.Cleanup(func() { testUser.IsActive = true })

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/_spike/blob", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", resp.StatusCode)
	}
}

// TestSessionExpiresHeader asserts X-Session-Expires-At is emitted on an
// authenticated BasePath response, which is what lets the SPA warn before a
// mutation is refused rather than after (5.3).
func TestSessionExpiresHeader(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/_spike/blob", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
	if resp.Header.Get("X-Session-Expires-At") == "" {
		t.Fatal("X-Session-Expires-At not set on an authenticated response")
	}
}

// TestNoCORSHeaders asserts the API answers no cross-origin grant. The custom
// CSRF header only protects a mutation because a cross-origin caller cannot
// set it without a preflight this API never answers (5.4).
func TestNoCORSHeaders(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	resp := doRequest(t, client, http.MethodGet, srv.URL+"/api/v1/config", nil, func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Origin", "https://evil.example")
	})

	for _, h := range []string{
		"Access-Control-Allow-Origin",
		"Access-Control-Allow-Credentials",
		"Access-Control-Allow-Methods",
		"Access-Control-Allow-Headers",
	} {
		if got := resp.Header.Get(h); got != "" {
			t.Errorf("%s = %q; the API must grant no cross-origin access", h, got)
		}
	}
}

// TestOversizeBody_IsRequestEntityTooLarge asserts the ceiling bounds a body
// before any parser sees it (5.7), which is a bound the code did not have
// before the split.
func TestOversizeBody_IsRequestEntityTooLarge(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)
	login(t, client, srv)

	oversize := bytes.Repeat([]byte("x"), (5<<20)+1024)
	resp := doRequest(t, client, http.MethodPost, srv.URL+"/api/v1/_spike/upload", bytes.NewReader(oversize), func(r *http.Request) { //nolint:bodyclose // closed via t.Cleanup inside doRequest
		r.Header.Set("Content-Type", "multipart/form-data; boundary=x")
		r.Header.Set("X-CSRF-Token", testCSRFToken)
	})

	if resp.StatusCode != http.StatusRequestEntityTooLarge {
		t.Fatalf("status = %d, want 413", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
}

// TestMethodNotAllowed_IsProblemDocument covers the 405 path, which chi
// otherwise answers with an empty body the generated client cannot parse.
func TestMethodNotAllowed_IsProblemDocument(t *testing.T) {
	srv := newTestServer(t)
	client := newClient(t)

	resp := doRequest(t, client, http.MethodDelete, srv.URL+"/api/v1/config", nil, nil) //nolint:bodyclose // closed via t.Cleanup inside doRequest
	if resp.StatusCode != http.StatusMethodNotAllowed {
		t.Fatalf("status = %d, want 405", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "application/problem+json" {
		t.Fatalf("Content-Type = %q, want application/problem+json", ct)
	}
}
