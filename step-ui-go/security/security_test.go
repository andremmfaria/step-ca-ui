package security

import (
	"strings"
	"testing"
)

func TestVerifyPasswordWithBcryptHash(t *testing.T) {
	hash := HashPassword("Admin123!")
	if hash == legacySHA256("Admin123!") {
		t.Fatal("HashPassword returned legacy SHA-256 hash")
	}
	if !VerifyPassword("Admin123!", hash) {
		t.Fatal("bcrypt password did not verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("wrong password verified")
	}
	if NeedsPasswordRehash(hash) {
		t.Fatal("fresh bcrypt hash should not need rehash")
	}
}

func TestVerifyPasswordWithLegacySHA256Hash(t *testing.T) {
	hash := legacySHA256("Admin123!")
	if !VerifyPassword("Admin123!", hash) {
		t.Fatal("legacy SHA-256 password did not verify")
	}
	if VerifyPassword("wrong", hash) {
		t.Fatal("wrong password verified against legacy SHA-256 hash")
	}
	if !NeedsPasswordRehash(hash) {
		t.Fatal("legacy SHA-256 hash should need rehash")
	}
}

// TestVerifyPasswordUnknownHash ensures an unknown hash format is rejected.
func TestVerifyPasswordUnknownHash(t *testing.T) {
	if VerifyPassword("anything", "not-a-valid-hash") {
		t.Fatal("VerifyPassword accepted an unknown hash format")
	}
}

// TestNeedsPasswordRehashUnknown ensures an unrecognised hash needs rehash.
func TestNeedsPasswordRehashUnknown(t *testing.T) {
	if !NeedsPasswordRehash("not-a-valid-hash") {
		t.Fatal("expected NeedsPasswordRehash=true for unknown hash format")
	}
}

// TestIsLegacySHA256Hash covers the helper directly.
func TestIsLegacySHA256Hash(t *testing.T) {
	// A 64-char lowercase hex string is a legacy SHA-256 hash.
	validHex := strings.Repeat("a", 64)
	if !isLegacySHA256Hash(validHex) {
		t.Error("expected isLegacySHA256Hash=true for 64-char hex string")
	}
	// Wrong length.
	if isLegacySHA256Hash("abc") {
		t.Error("expected false for short string")
	}
	// Right length but non-hex chars.
	notHex := strings.Repeat("z", 64)
	if isLegacySHA256Hash(notHex) {
		t.Error("expected false for non-hex string of correct length")
	}
}

// TestValidatePassword covers the complexity rules.
func TestValidatePassword(t *testing.T) {
	cases := []struct {
		pw      string
		wantOK  bool
		wantSub string // substring expected in reason when !wantOK
	}{
		{"short", false, "8"},
		{strings.Repeat("a", 73), false, "72"},
		{"nospecia1", false, "спецсимвол"},
		{"NoDigit!", false, "цифр"},
		{"12345678!", false, "букв"},
		{"ValidPass1!", true, ""},
	}
	for _, tc := range cases {
		ok, reason := ValidatePassword(tc.pw)
		if ok != tc.wantOK {
			t.Errorf("ValidatePassword(%q): ok=%v want=%v reason=%q", tc.pw, ok, tc.wantOK, reason)
			continue
		}
		if !tc.wantOK && tc.wantSub != "" && !strings.Contains(reason, tc.wantSub) {
			t.Errorf("ValidatePassword(%q): reason %q does not contain %q", tc.pw, reason, tc.wantSub)
		}
	}
}

// TestGenerateToken produces a non-empty, 64-char hex token.
func TestGenerateToken(t *testing.T) {
	tok := GenerateToken()
	if len(tok) != 64 {
		t.Errorf("GenerateToken length=%d, want 64", len(tok))
	}
	tok2 := GenerateToken()
	if tok == tok2 {
		t.Error("GenerateToken returned identical tokens — entropy suspect")
	}
}

// TestRateLimiterBasic registers up to LimitCount attempts and confirms
// IsBlocked triggers at the boundary.
func TestRateLimiterBasic(t *testing.T) {
	rl := NewRateLimiter()
	const ip = "192.0.2.1"

	for i := range LimitCount {
		if rl.IsBlocked(ip) {
			t.Fatalf("IsBlocked before limit at attempt %d", i)
		}
		rl.Register(ip)
	}
	if !rl.IsBlocked(ip) {
		t.Fatal("expected IsBlocked after LimitCount registrations")
	}
	if rl.Left(ip) != 0 {
		t.Errorf("Left should be 0 when blocked, got %d", rl.Left(ip))
	}
}

// TestRateLimiterPortStripping verifies that connections from the same host
// on different ephemeral ports all count under one rate-limit key.
// The clientIP helper (handlers package) strips the port before calling
// rl.Register; this test confirms the limiter itself treats the bare host
// as the key.
func TestRateLimiterPortStripping(t *testing.T) {
	rl := NewRateLimiter()
	host := "10.0.0.1"
	// Register exactly LimitCount times using the bare host (post-strip).
	for range LimitCount {
		rl.Register(host)
	}
	if !rl.IsBlocked(host) {
		t.Fatal("IsBlocked should be true after LimitCount registrations with same host")
	}
}

// TestRateLimiterClear verifies Clear resets the block.
func TestRateLimiterClear(t *testing.T) {
	rl := NewRateLimiter()
	const ip = "203.0.113.7"
	for range LimitCount {
		rl.Register(ip)
	}
	if !rl.IsBlocked(ip) {
		t.Fatal("should be blocked before clear")
	}
	rl.Clear(ip)
	if rl.IsBlocked(ip) {
		t.Fatal("should not be blocked after clear")
	}
}

// TestRateLimiterEmptyIP ensures an empty IP string is never blocked.
func TestRateLimiterEmptyIP(t *testing.T) {
	rl := NewRateLimiter()
	if rl.IsBlocked("") {
		t.Fatal("empty IP should never be blocked")
	}
}

// TestRateLimiterLeft reports remaining attempts correctly.
func TestRateLimiterLeft(t *testing.T) {
	rl := NewRateLimiter()
	const ip = "198.51.100.1"
	if rl.Left(ip) != LimitCount {
		t.Errorf("Left before any attempt: want %d got %d", LimitCount, rl.Left(ip))
	}
	rl.Register(ip)
	if rl.Left(ip) != LimitCount-1 {
		t.Errorf("Left after 1 attempt: want %d got %d", LimitCount-1, rl.Left(ip))
	}
}
