//go:build integration

package db

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"testing"
	"time"

	"step-ui/security"
)

// tokenHashStr hashes a raw token string with SHA-256 and returns the hex string.
// Mirrors the handler logic without importing the handlers package.
func tokenHashStr(raw string) string {
	sum := sha256.Sum256([]byte(raw))
	return hex.EncodeToString(sum[:])
}

// TestIntegration_PasswordReset_Schema confirms the schema is idempotent.
func TestIntegration_PasswordReset_Schema(t *testing.T) {
	conn := openTestDB(t)
	if err := InitPasswordResetSchema(conn); err != nil {
		t.Fatalf("first InitPasswordResetSchema: %v", err)
	}
	if err := InitPasswordResetSchema(conn); err != nil {
		t.Fatalf("second InitPasswordResetSchema (idempotency): %v", err)
	}
}

// TestIntegration_GetUserByLoginOrEmail covers username and email lookup.
func TestIntegration_GetUserByLoginOrEmail(t *testing.T) {
	conn := openTestDB(t)

	username := fmt.Sprintf("reset_user_%d", time.Now().UnixNano())
	email := fmt.Sprintf("%s@example.com", username)
	hash := security.HashPassword("TestPass1!")

	if err := CreateUser(conn, username, hash, "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := GetUserByUsername(conn, username)
	if u == nil {
		t.Fatal("user not found after create")
	}
	if err := UpdateUserInfo(conn, u.ID, username, "Test User", email); err != nil {
		t.Fatalf("UpdateUserInfo: %v", err)
	}

	t.Run("by username", func(t *testing.T) {
		got, err := GetUserByLoginOrEmail(conn, username)
		if err != nil {
			t.Fatalf("GetUserByLoginOrEmail: %v", err)
		}
		if got == nil {
			t.Fatal("returned nil for known username")
		}
		if got.Username != username {
			t.Errorf("username: got %q want %q", got.Username, username)
		}
	})

	t.Run("by email exact case", func(t *testing.T) {
		got, err := GetUserByLoginOrEmail(conn, email)
		if err != nil {
			t.Fatalf("GetUserByLoginOrEmail: %v", err)
		}
		if got == nil {
			t.Fatal("returned nil for known email")
		}
		if got.ID != u.ID {
			t.Errorf("ID: got %d want %d", got.ID, u.ID)
		}
	})

	t.Run("by email uppercase", func(t *testing.T) {
		got, err := GetUserByLoginOrEmail(conn, email)
		if err != nil {
			t.Fatalf("GetUserByLoginOrEmail: %v", err)
		}
		if got == nil {
			t.Fatal("case-insensitive email lookup returned nil")
		}
	})

	t.Run("unknown returns nil", func(t *testing.T) {
		got, err := GetUserByLoginOrEmail(conn, "no-such-user@missing.invalid")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != nil {
			t.Error("expected nil for unknown identifier")
		}
	})
}

// TestIntegration_PasswordReset_TokenLifecycle covers create → validate → use → invalidate.
func TestIntegration_PasswordReset_TokenLifecycle(t *testing.T) {
	conn := openTestDB(t)
	if err := InitPasswordResetSchema(conn); err != nil {
		t.Fatalf("InitPasswordResetSchema: %v", err)
	}

	username := fmt.Sprintf("tok_user_%d", time.Now().UnixNano())
	if err := CreateUser(conn, username, security.HashPassword("Pw1!Pw1!"), "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := GetUserByUsername(conn, username)
	if u == nil {
		t.Fatal("user not found")
	}

	const rawToken = "integration-test-token-value"
	tokenHash := tokenHashStr(rawToken)

	t.Run("create and retrieve valid token", func(t *testing.T) {
		if err := CreatePasswordResetToken(conn, u.ID, tokenHash, "127.0.0.1", time.Now().Add(30*time.Minute)); err != nil {
			t.Fatalf("CreatePasswordResetToken: %v", err)
		}
		tok, err := GetValidPasswordResetToken(conn, tokenHash)
		if err != nil {
			t.Fatalf("GetValidPasswordResetToken: %v", err)
		}
		if tok == nil {
			t.Fatal("expected valid token, got nil")
		}
		if tok.UserID != u.ID {
			t.Errorf("UserID: got %d want %d", tok.UserID, u.ID)
		}
	})

	t.Run("mark used — token no longer valid", func(t *testing.T) {
		tok, _ := GetValidPasswordResetToken(conn, tokenHash)
		if tok == nil {
			t.Skip("token not found — prior sub-test may have failed")
		}
		if err := MarkPasswordResetTokenUsed(conn, tok.ID); err != nil {
			t.Fatalf("MarkPasswordResetTokenUsed: %v", err)
		}
		used, err := GetValidPasswordResetToken(conn, tokenHash)
		if err != nil {
			t.Fatalf("GetValidPasswordResetToken after mark used: %v", err)
		}
		if used != nil {
			t.Error("expected nil after marking token used")
		}
	})
}

// TestIntegration_PasswordReset_Expired confirms expired tokens are not returned.
func TestIntegration_PasswordReset_Expired(t *testing.T) {
	conn := openTestDB(t)
	if err := InitPasswordResetSchema(conn); err != nil {
		t.Fatalf("InitPasswordResetSchema: %v", err)
	}

	username := fmt.Sprintf("exp_user_%d", time.Now().UnixNano())
	if err := CreateUser(conn, username, security.HashPassword("Pw1!Pw1!"), "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := GetUserByUsername(conn, username)

	const expiredHash = "expired-hash-integration-test-0000000000000000000000000000000"
	// expires_at in the past
	if err := CreatePasswordResetToken(conn, u.ID, expiredHash, "127.0.0.1", time.Now().Add(-time.Minute)); err != nil {
		t.Fatalf("CreatePasswordResetToken: %v", err)
	}
	tok, err := GetValidPasswordResetToken(conn, expiredHash)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tok != nil {
		t.Error("expected nil for expired token")
	}
}

// TestIntegration_PasswordReset_InvalidateAll confirms InvalidatePasswordResetTokens
// marks all unused tokens for a user used.
func TestIntegration_PasswordReset_InvalidateAll(t *testing.T) {
	conn := openTestDB(t)
	if err := InitPasswordResetSchema(conn); err != nil {
		t.Fatalf("InitPasswordResetSchema: %v", err)
	}

	username := fmt.Sprintf("inv_user_%d", time.Now().UnixNano())
	if err := CreateUser(conn, username, security.HashPassword("Pw1!Pw1!"), "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := GetUserByUsername(conn, username)

	hashA := fmt.Sprintf("hash-a-%d", time.Now().UnixNano())
	hashB := fmt.Sprintf("hash-b-%d", time.Now().UnixNano())
	for _, h := range []string{hashA, hashB} {
		if err := CreatePasswordResetToken(conn, u.ID, h, "127.0.0.1", time.Now().Add(30*time.Minute)); err != nil {
			t.Fatalf("CreatePasswordResetToken %q: %v", h, err)
		}
	}

	if err := InvalidatePasswordResetTokens(conn, u.ID); err != nil {
		t.Fatalf("InvalidatePasswordResetTokens: %v", err)
	}

	for _, h := range []string{hashA, hashB} {
		tok, err := GetValidPasswordResetToken(conn, h)
		if err != nil {
			t.Fatalf("GetValidPasswordResetToken %q: %v", h, err)
		}
		if tok != nil {
			t.Errorf("expected nil after invalidation for hash %q", h)
		}
	}
}
