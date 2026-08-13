// Package security provides password hashing, token generation, and
// per-IP rate limiting for login attempt protection.
package security

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// ─── Password ─────────────────────────────────────────────────────────────────

// HashPassword hashes pw with bcrypt at the default cost and panics on failure.
func HashPassword(pw string) string {
	hash, err := bcrypt.GenerateFromPassword([]byte(pw), bcrypt.DefaultCost)
	if err != nil {
		panic("bcrypt password hash failed: " + err.Error())
	}
	return string(hash)
}

func legacySHA256(pw string) string {
	h := sha256.Sum256([]byte(pw))
	return hex.EncodeToString(h[:])
}

func isLegacySHA256Hash(hash string) bool {
	if len(hash) != sha256.Size*2 {
		return false
	}
	_, err := hex.DecodeString(hash)
	return err == nil
}

// VerifyPassword returns true when pw matches the stored hash (bcrypt or legacy SHA-256).
func VerifyPassword(pw, hash string) bool {
	if strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") || strings.HasPrefix(hash, "$2y$") {
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
	}
	if isLegacySHA256Hash(hash) {
		expected := legacySHA256(pw)
		return subtle.ConstantTimeCompare([]byte(expected), []byte(hash)) == 1
	}
	return false
}

// NeedsPasswordRehash returns true when the stored hash uses the legacy SHA-256
// scheme or a bcrypt cost below the current default, indicating it should be
// upgraded on the next successful login.
func NeedsPasswordRehash(hash string) bool {
	if isLegacySHA256Hash(hash) {
		return true
	}
	cost, err := bcrypt.Cost([]byte(hash))
	return err != nil || cost < bcrypt.DefaultCost
}

// ValidatePassword returns (true, "") when pw satisfies the policy (8–72 chars,
// at least one digit, letter and special character), or (false, reason) otherwise.
func ValidatePassword(pw string) (bool, string) {
	if len(pw) < 8 {
		return false, "Minimum 8 characters required"
	}
	if len(pw) > 72 {
		return false, "Maximum 72 characters allowed"
	}
	hasDigit, hasLetter, hasSpecial := false, false, false
	for _, c := range pw {
		switch {
		case c >= '0' && c <= '9':
			hasDigit = true
		case (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z'):
			hasLetter = true
		case strings.ContainsRune(`+!@#$%^&*()_-=[]{}|;:,.<>?`, c):
			hasSpecial = true
		}
	}
	if !hasDigit {
		return false, "At least one digit is required"
	}
	if !hasLetter {
		return false, "At least one letter is required"
	}
	if !hasSpecial {
		return false, "At least one special character is required"
	}
	return true, ""
}

// ─── CSRF token ───────────────────────────────────────────────────────────────

// GenerateToken returns a 32-byte cryptographically random hex token.
func GenerateToken() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

// ─── Rate Limiting ────────────────────────────────────────────────────────────

// Rate-limiter thresholds and windows.
const (
	// LimitCount is the maximum number of login attempts within LimitWindow
	// before the IP is considered blocked.
	LimitCount  = 5
	LimitWindow = 5 * time.Minute
	BlockTime   = 15 * time.Minute
)

// RateLimiter tracks per-IP login attempt counts with a sliding time window.
type RateLimiter struct {
	mu       sync.Mutex
	attempts map[string][]time.Time
}

// RL is the process-global rate limiter used for login attempt tracking.
var RL = NewRateLimiter()

// NewRateLimiter returns an initialised RateLimiter. Use this in tests instead
// of constructing the struct literal directly, to avoid touching unexported fields.
func NewRateLimiter() *RateLimiter {
	return &RateLimiter{attempts: make(map[string][]time.Time)}
}

func (r *RateLimiter) clean(ip string) {
	now := time.Now()
	var v []time.Time
	for _, t := range r.attempts[ip] {
		if now.Sub(t) < LimitWindow {
			v = append(v, t)
		}
	}
	r.attempts[ip] = v
}

// IsBlocked returns true when ip has reached the LimitCount threshold within LimitWindow.
func (r *RateLimiter) IsBlocked(ip string) bool {
	if ip == "" {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clean(ip)
	return len(r.attempts[ip]) >= LimitCount
}

// Register records one login attempt from ip.
func (r *RateLimiter) Register(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clean(ip)
	r.attempts[ip] = append(r.attempts[ip], time.Now())
}

// Clear removes all recorded attempts for ip (called on successful login).
func (r *RateLimiter) Clear(ip string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.attempts, ip)
}

// Left returns the number of remaining attempts before ip is blocked.
func (r *RateLimiter) Left(ip string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.clean(ip)
	n := LimitCount - len(r.attempts[ip])
	if n < 0 {
		return 0
	}
	return n
}
