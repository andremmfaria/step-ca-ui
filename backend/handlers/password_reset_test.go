package handlers

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"strings"
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

// ─── resetLink ────────────────────────────────────────────────────────────────

// TestResetLinkUsesConfiguredOrigin pins the property that replaced
// absoluteURL: the link comes from configuration, so no request header can
// steer where a victim's reset token is sent.
func TestResetLinkUsesConfiguredOrigin(t *testing.T) {
	tests := []struct {
		name string
		base string
		want string
	}{
		{"https origin", "https://ca.example.com", "https://ca.example.com/reset-password?token=t0k"},
		{"http origin", "http://ca.example.com", "http://ca.example.com/reset-password?token=t0k"},
		{"explicit port", "https://ca.example.com:8443", "https://ca.example.com:8443/reset-password?token=t0k"},
		{"path on base is dropped", "https://ca.example.com/ui", "https://ca.example.com/reset-password?token=t0k"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resetLink(tc.base, "t0k")
			if err != nil {
				t.Fatalf("resetLink(%q): %v", tc.base, err)
			}
			if got != tc.want {
				t.Errorf("resetLink(%q) = %q, want %q", tc.base, got, tc.want)
			}
		})
	}
}

// TestResetLinkFailsClosed confirms an unusable origin stops the send instead
// of falling back to anything request-derived.
func TestResetLinkFailsClosed(t *testing.T) {
	for _, base := range []string{
		"", "   ", "not a url", "ftp://ca.example.com", "https://", "/reset", "javascript:alert(1)",
	} {
		if got, err := resetLink(base, "t0k"); err == nil {
			t.Errorf("resetLink(%q) returned %q, want an error", base, got)
		}
	}
}

// TestResetLinkEscapesToken guards the one caller-supplied component.
func TestResetLinkEscapesToken(t *testing.T) {
	got, err := resetLink("https://ca.example.com", "a b&c=d#e")
	if err != nil {
		t.Fatalf("resetLink: %v", err)
	}
	want := "https://ca.example.com/reset-password?token=a+b%26c%3Dd%23e"
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

// ─── SMTP header injection ────────────────────────────────────────────────────

// TestSendPasswordResetMailRejectsHeaderInjection asserts that addresses
// carrying CR/LF are refused before any connection is attempted, so injected
// headers can never reach the SMTP DATA stage.  Port 1 is used deliberately:
// if validation regressed, the dial would fail with a connection error rather
// than the address error asserted here.
func TestSendPasswordResetMailRejectsHeaderInjection(t *testing.T) {
	tests := []struct {
		name    string
		from    string
		to      string
		wantErr string
	}{
		{
			name:    "bcc injected into recipient",
			from:    "ca@example.com",
			to:      "user@example.com\r\nBcc: attacker@evil.com",
			wantErr: "invalid recipient address",
		},
		{
			name:    "subject and body injected into recipient",
			from:    "ca@example.com",
			to:      "user@example.com\nSubject: Account seized\n\nGo to evil.com",
			wantErr: "invalid recipient address",
		},
		{
			name:    "injected sender",
			from:    "ca@example.com\r\nBcc: attacker@evil.com",
			to:      "user@example.com",
			wantErr: "invalid sender address",
		},
		{
			name:    "recipient is not an address at all",
			from:    "ca@example.com",
			to:      "not-an-email",
			wantErr: "invalid recipient address",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()

			err := sendPasswordResetMail(
				ctx,
				"127.0.0.1", 1, "starttls",
				"", "",
				tc.from, tc.to, "https://ca.example.com/reset?token=x",
			)
			if err == nil {
				t.Fatalf("expected error for from=%q to=%q, got nil", tc.from, tc.to)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestSendPasswordResetMailAcceptsDisplayNameForm confirms the validation does
// not reject legitimate RFC 5322 forms; it fails at the dial, not the parse.
func TestSendPasswordResetMailAcceptsDisplayNameForm(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	err := sendPasswordResetMail(
		ctx,
		"127.0.0.1", 1, "starttls",
		"", "",
		"Step CA <ca@example.com>", "User <user@example.com>", "https://ca.example.com/reset?token=x",
	)
	if err == nil {
		t.Fatal("expected a dial error, got nil")
	}
	if strings.Contains(err.Error(), "invalid recipient address") ||
		strings.Contains(err.Error(), "invalid sender address") {
		t.Errorf("valid addresses were rejected as malformed: %v", err)
	}
}
