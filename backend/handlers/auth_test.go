package handlers

import (
	"fmt"
	"net/http"
	"testing"

	"step-ui/security"
)

func TestClientIPStripsPort(t *testing.T) {
	cases := []struct {
		remoteAddr string
		wantIP     string
	}{
		{"192.0.2.1:54321", "192.0.2.1"},
		{"192.0.2.1:1024", "192.0.2.1"},
		{"[::1]:54321", "::1"},
		// Already a bare IP (TrustProxy=true path, RealIP has normalised it)
		{"192.0.2.1", "192.0.2.1"},
	}
	for _, tc := range cases {
		r := &http.Request{RemoteAddr: tc.remoteAddr}
		got := clientIP(r)
		if got != tc.wantIP {
			t.Errorf("clientIP(%q) = %q, want %q", tc.remoteAddr, got, tc.wantIP)
		}
	}
}

// TestRateLimiterKeyedOnHostNotPort verifies that 6 consecutive Register calls
// from the same host on different ephemeral ports all count under one key,
// so IsBlocked returns true after LimitCount attempts.
func TestRateLimiterKeyedOnHostNotPort(t *testing.T) {
	rl := security.NewRateLimiter()

	const host = "10.0.0.1"
	for i := 0; i < security.LimitCount+1; i++ {
		// Simulate different ephemeral ports for each TCP connection.
		addr := fmt.Sprintf("%s:%d", host, 50000+i)
		r := &http.Request{RemoteAddr: addr}
		ip := clientIP(r)
		rl.Register(ip)
	}

	r := &http.Request{RemoteAddr: host + ":60000"}
	if !rl.IsBlocked(clientIP(r)) {
		t.Fatalf("expected IsBlocked=true for %s after %d attempts from different ports", host, security.LimitCount+1)
	}
}
