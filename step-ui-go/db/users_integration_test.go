//go:build integration

package db

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"step-ui/security"
)

// TestIntegration_UpsertOIDCUserCannotTakeOverLocalRow is the V1 regression:
// an OIDC login carrying the username of a local account must change nothing
// and must be reported, or the IdP silently promotes an account whose bcrypt
// hash still works.
func TestIntegration_UpsertOIDCUserCannotTakeOverLocalRow(t *testing.T) {
	conn := openTestDB(t)

	username := fmt.Sprintf("local_victim_%d", time.Now().UnixNano())
	hash := security.HashPassword("TestPass1!")
	if err := CreateUser(conn, username, hash, "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}

	for _, syncRole := range []bool{true, false} {
		t.Run(fmt.Sprintf("syncRole=%t", syncRole), func(t *testing.T) {
			user, err := UpsertOIDCUser(conn, username, "Attacker", "admin", syncRole)
			if !errors.Is(err, ErrOIDCLocalUser) {
				t.Fatalf("err: got %v want ErrOIDCLocalUser", err)
			}
			if user != nil {
				t.Errorf("user: got %+v want nil", user)
			}

			got, err := GetUserByUsername(conn, username)
			if err != nil || got == nil {
				t.Fatalf("GetUserByUsername: %v", err)
			}
			if got.Role != "viewer" {
				t.Errorf("role: got %q want %q, the local row was promoted", got.Role, "viewer")
			}
			if got.PasswordHash != hash {
				t.Errorf("password_hash changed: got %q want %q", got.PasswordHash, hash)
			}
		})
	}
}

// TestIntegration_UpsertOIDCUserOwnsItsOwnRows confirms the auth_source guard
// does not break the normal OIDC path: create, then re-login with a new role.
func TestIntegration_UpsertOIDCUserOwnsItsOwnRows(t *testing.T) {
	conn := openTestDB(t)

	username := fmt.Sprintf("oidc_user_%d", time.Now().UnixNano())
	created, err := UpsertOIDCUser(conn, username, "OIDC User", "viewer", true)
	if err != nil {
		t.Fatalf("first UpsertOIDCUser: %v", err)
	}
	if created == nil || created.Role != "viewer" {
		t.Fatalf("first upsert: got %+v want role viewer", created)
	}

	promoted, err := UpsertOIDCUser(conn, username, "OIDC User", "manager", true)
	if err != nil {
		t.Fatalf("second UpsertOIDCUser: %v", err)
	}
	if promoted.Role != "manager" {
		t.Errorf("role after IdP group change: got %q want %q", promoted.Role, "manager")
	}

	t.Run("syncRole=false leaves the role alone", func(t *testing.T) {
		unchanged, err := UpsertOIDCUser(conn, username, "OIDC User", "admin", false)
		if err != nil {
			t.Fatalf("UpsertOIDCUser: %v", err)
		}
		if unchanged.Role != "manager" {
			t.Errorf("role: got %q want %q", unchanged.Role, "manager")
		}
	})
}

// ─── Session epoch (V3, V5, V8) ───────────────────────────────────────────────

// TestIntegration_SessionEpochBumps covers every write that must revoke live
// sessions.
func TestIntegration_SessionEpochBumps(t *testing.T) {
	conn := openTestDB(t)

	newUser := func(t *testing.T, prefix string) int {
		t.Helper()
		username := fmt.Sprintf("%s_%d", prefix, time.Now().UnixNano())
		if err := CreateUser(conn, username, security.HashPassword("TestPass1!"), "viewer"); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		u, err := GetUserByUsername(conn, username)
		if err != nil || u == nil {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		if u.SessionEpoch != 0 {
			t.Fatalf("fresh user epoch: got %d want 0", u.SessionEpoch)
		}
		return u.ID
	}

	epochOf := func(t *testing.T, id int) int {
		t.Helper()
		u, err := GetUserByID(conn, id)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		return u.SessionEpoch
	}

	t.Run("BumpSessionEpoch", func(t *testing.T) {
		id := newUser(t, "bump")
		if err := BumpSessionEpoch(conn, id); err != nil {
			t.Fatalf("BumpSessionEpoch: %v", err)
		}
		if got := epochOf(t, id); got != 1 {
			t.Errorf("epoch: got %d want 1", got)
		}
	})

	t.Run("UpdateUserRole", func(t *testing.T) {
		id := newUser(t, "role")
		if err := UpdateUserRole(conn, id, "admin"); err != nil {
			t.Fatalf("UpdateUserRole: %v", err)
		}
		if got := epochOf(t, id); got != 1 {
			t.Errorf("epoch after role change: got %d want 1", got)
		}
	})

	t.Run("UpdateUserActive", func(t *testing.T) {
		id := newUser(t, "active")
		if err := UpdateUserActive(conn, id, false); err != nil {
			t.Fatalf("deactivate: %v", err)
		}
		if got := epochOf(t, id); got != 1 {
			t.Errorf("epoch after deactivation: got %d want 1", got)
		}
		if err := UpdateUserActive(conn, id, true); err != nil {
			t.Fatalf("reactivate: %v", err)
		}
		if got := epochOf(t, id); got != 1 {
			t.Errorf("reactivation must not bump the epoch: got %d want 1", got)
		}
	})

	t.Run("ExpireOverdueTempUsers", func(t *testing.T) {
		username := fmt.Sprintf("temp_%d", time.Now().UnixNano())
		id, err := CreateTempUser(conn, username, security.HashPassword("TestPass1!"), "admin",
			time.Now().Add(-time.Minute), "expired on purpose")
		if err != nil {
			t.Fatalf("CreateTempUser: %v", err)
		}
		n, err := ExpireOverdueTempUsers(conn)
		if err != nil {
			t.Fatalf("ExpireOverdueTempUsers: %v", err)
		}
		if n < 1 {
			t.Fatalf("expired count: got %d want at least 1", n)
		}
		if got := epochOf(t, id); got != 1 {
			t.Errorf("epoch after expiry: got %d want 1", got)
		}
	})
}

// ─── Role allowlist (V9) ──────────────────────────────────────────────────────

// TestIntegration_InvalidRoleRejectedAtDataLayer holds the invariant one level
// below the handler: a role outside the allowlist scores zero in roleLevel, so
// the row it produces logs in and then fails every role check.
func TestIntegration_InvalidRoleRejectedAtDataLayer(t *testing.T) {
	conn := openTestDB(t)
	hash := security.HashPassword("TestPass1!")

	t.Run("CreateUser", func(t *testing.T) {
		for _, role := range []string{"superuser", "Admin", "", "admin viewer"} {
			username := fmt.Sprintf("badrole_%d", time.Now().UnixNano())
			err := CreateUser(conn, username, hash, role)
			if !errors.Is(err, ErrInvalidRole) {
				t.Errorf("CreateUser role=%q: got %v want ErrInvalidRole", role, err)
			}
			u, err := GetUserByUsername(conn, username)
			if err != nil {
				t.Fatalf("GetUserByUsername: %v", err)
			}
			if u != nil {
				t.Errorf("CreateUser role=%q wrote a row anyway: %+v", role, u)
			}
		}
	})

	t.Run("UpdateUserRole", func(t *testing.T) {
		username := fmt.Sprintf("rolekeeper_%d", time.Now().UnixNano())
		if err := CreateUser(conn, username, hash, "manager"); err != nil {
			t.Fatalf("CreateUser: %v", err)
		}
		u, err := GetUserByUsername(conn, username)
		if err != nil || u == nil {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		if err := UpdateUserRole(conn, u.ID, "superuser"); !errors.Is(err, ErrInvalidRole) {
			t.Errorf("UpdateUserRole: got %v want ErrInvalidRole", err)
		}
		after, err := GetUserByID(conn, u.ID)
		if err != nil {
			t.Fatalf("GetUserByID: %v", err)
		}
		if after.Role != "manager" {
			t.Errorf("role: got %q want %q", after.Role, "manager")
		}
		// The rejection happens before the write, so it must not revoke the
		// user's sessions either.
		if after.SessionEpoch != 0 {
			t.Errorf("session_epoch: got %d want 0", after.SessionEpoch)
		}
	})

	t.Run("valid roles still pass", func(t *testing.T) {
		for _, role := range []string{"viewer", "manager", "admin"} {
			username := fmt.Sprintf("goodrole_%s_%d", role, time.Now().UnixNano())
			if err := CreateUser(conn, username, hash, role); err != nil {
				t.Fatalf("CreateUser role=%q: %v", role, err)
			}
			u, err := GetUserByUsername(conn, username)
			if err != nil || u == nil || u.Role != role {
				t.Errorf("role %q round-trip: got %+v, %v", role, u, err)
			}
		}
	})
}

// TestIntegration_UpsertOIDCUserRejectsInvalidRole closes V9's other route in:
// mapGroupsToRole only returns known roles, but it falls back to the
// operator-supplied OIDC_DEFAULT_ROLE, which reaches this function unchecked.
func TestIntegration_UpsertOIDCUserRejectsInvalidRole(t *testing.T) {
	conn := openTestDB(t)

	for _, role := range []string{"garbage", "Admin", ""} {
		username := fmt.Sprintf("oidc_badrole_%d", time.Now().UnixNano())
		user, err := UpsertOIDCUser(conn, username, "OIDC User", role, true)
		if !errors.Is(err, ErrInvalidRole) {
			t.Errorf("UpsertOIDCUser role=%q: got %v want ErrInvalidRole", role, err)
		}
		if user != nil {
			t.Errorf("UpsertOIDCUser role=%q returned %+v want nil", role, user)
		}
		got, err := GetUserByUsername(conn, username)
		if err != nil {
			t.Fatalf("GetUserByUsername: %v", err)
		}
		if got != nil {
			t.Errorf("UpsertOIDCUser role=%q wrote a row anyway: %+v", role, got)
		}
	}
}
