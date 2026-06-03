package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"testing"
	"time"
)

// ─── passwordResetTokenHash ───────────────────────────────────────────────────

func TestPasswordResetTokenHash_Deterministic(t *testing.T) {
	h1 := passwordResetTokenHash("abc123")
	h2 := passwordResetTokenHash("abc123")
	if h1 != h2 {
		t.Errorf("hash not deterministic: %q vs %q", h1, h2)
	}
}

func TestPasswordResetTokenHash_SHA256(t *testing.T) {
	token := "test-token-value"
	got := passwordResetTokenHash(token)
	sum := sha256.Sum256([]byte(token))
	want := hex.EncodeToString(sum[:])
	if got != want {
		t.Errorf("hash mismatch: got %q want %q", got, want)
	}
}

func TestPasswordResetTokenHash_Length64(t *testing.T) {
	if len(passwordResetTokenHash("anything")) != 64 {
		t.Error("expected 64-char hex hash (SHA-256)")
	}
}

func TestPasswordResetTokenHash_Distinct(t *testing.T) {
	if passwordResetTokenHash("token-a") == passwordResetTokenHash("token-b") {
		t.Error("different inputs produced same hash")
	}
}

// ─── passwordResetAllowed (per-IP rate limiter) ───────────────────────────────

func clearResetIP(ip string) {
	passwordResetRL.Lock()
	delete(passwordResetRL.attempts, ip)
	passwordResetRL.Unlock()
}

func TestPasswordResetAllowed_AllowsUnderLimit(t *testing.T) {
	ip := "192.0.2.100"
	clearResetIP(ip)
	for i := range passwordResetLimitCount {
		if !passwordResetAllowed(ip) {
			t.Fatalf("attempt %d should be allowed (limit=%d)", i+1, passwordResetLimitCount)
		}
	}
}

func TestPasswordResetAllowed_BlocksAtLimit(t *testing.T) {
	ip := "192.0.2.101"
	clearResetIP(ip)
	for range passwordResetLimitCount {
		passwordResetAllowed(ip) //nolint:errcheck // intentionally consuming attempts
	}
	if passwordResetAllowed(ip) {
		t.Error("expected block after limit exceeded")
	}
}

func TestPasswordResetAllowed_ExpiredAttemptsSlideOut(t *testing.T) {
	ip := "192.0.2.102"
	passwordResetRL.Lock()
	old := time.Now().Add(-(passwordResetLimitWindow + time.Second))
	passwordResetRL.attempts[ip] = []time.Time{old, old, old}
	passwordResetRL.Unlock()

	if !passwordResetAllowed(ip) {
		t.Error("expired attempts should slide out of window")
	}
}

func TestPasswordResetAllowed_IndependentIPs(t *testing.T) {
	ip1 := "192.0.2.200"
	ip2 := "192.0.2.201"
	clearResetIP(ip1)
	clearResetIP(ip2)

	for range passwordResetLimitCount {
		passwordResetAllowed(ip1)
	}
	if !passwordResetAllowed(ip2) {
		t.Error("ip2 should not be affected by ip1 rate limit")
	}
	if passwordResetAllowed(ip1) {
		t.Error("ip1 should be blocked after hitting limit")
	}
}

// ─── absoluteURL ─────────────────────────────────────────────────────────────

func newResetRequest(t *testing.T, target string) *http.Request {
	t.Helper()
	r, err := http.NewRequestWithContext(context.Background(), http.MethodGet, target, nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	r.Host = "example.com"
	return r
}

func TestAbsoluteURL_ForwardedProtoHTTPS(t *testing.T) {
	r := newResetRequest(t, "/reset-password?token=abc")
	r.Header.Set("X-Forwarded-Proto", "https")
	got := absoluteURL(r, "/reset-password?token=abc")
	want := "https://example.com/reset-password?token=abc"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAbsoluteURL_PlainHTTPFallback(t *testing.T) {
	r := newResetRequest(t, "/foo")
	got := absoluteURL(r, "/foo")
	want := "http://example.com/foo"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

func TestAbsoluteURL_ForwardedProtoHTTP(t *testing.T) {
	r := newResetRequest(t, "/bar")
	r.Header.Set("X-Forwarded-Proto", "http")
	got := absoluteURL(r, "/bar")
	want := "http://example.com/bar"
	if got != want {
		t.Errorf("got %q want %q", got, want)
	}
}

// ─── constants ───────────────────────────────────────────────────────────────

func TestPasswordResetConstants(t *testing.T) {
	if passwordResetTTL != 30*time.Minute {
		t.Errorf("TTL: got %v want 30m", passwordResetTTL)
	}
	if passwordResetLimitCount != 3 {
		t.Errorf("limit count: got %d want 3", passwordResetLimitCount)
	}
	if passwordResetLimitWindow != 15*time.Minute {
		t.Errorf("limit window: got %v want 15m", passwordResetLimitWindow)
	}
}
