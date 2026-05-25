package security

import "testing"

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
