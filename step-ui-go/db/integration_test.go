//go:build integration

package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"step-ui/models"
	"step-ui/security"
	"testing"
	"time"
)

// testDSN returns the DSN for the integration Postgres.  In CI the
// INTEGRATION_DB_DSN env var is set by the services: postgres job step.
// Locally, set the same variable.
func testDSN(t *testing.T) string {
	t.Helper()
	if dsn := os.Getenv("INTEGRATION_DB_DSN"); dsn != "" {
		return dsn
	}
	// Default matches the CI postgres service definition.
	return "postgres://stepui_test:stepui_test@localhost:5432/stepui_test?sslmode=disable"
}

// openTestDB opens a connection, runs InitSchema, and registers a cleanup that
// truncates all tables and closes the connection so tests are isolated.
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	t.Setenv("STEPUI_ADMIN_PASSWORD", "IntegrationTest123!")
	conn, err := Connect(testDSN(t))
	if err != nil {
		t.Skipf("integration DB not available (%v) — skipping", err)
	}
	if err := InitSchema(conn); err != nil {
		_ = conn.Close()
		t.Fatalf("InitSchema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`TRUNCATE users, auth_log, certificates, cert_history, user_recovery_codes RESTART IDENTITY CASCADE`) //nolint:noctx
		_ = conn.Close()
	})
	return conn
}

// TestIntegration_Connect confirms that Connect returns a working connection.
func TestIntegration_Connect(t *testing.T) {
	t.Setenv("STEPUI_ADMIN_PASSWORD", "IntegrationTest123!")
	conn, err := Connect(testDSN(t))
	if err != nil {
		t.Skipf("DB not reachable: %v", err)
	}
	defer func() { _ = conn.Close() }()
	if err := conn.PingContext(context.Background()); err != nil {
		t.Fatalf("Ping after Connect: %v", err)
	}
}

// TestIntegration_InitSchema_Idempotent calls InitSchema twice and confirms the
// second call does not error (all IF NOT EXISTS guards).
func TestIntegration_InitSchema_Idempotent(t *testing.T) {
	conn := openTestDB(t)
	t.Setenv("STEPUI_ADMIN_PASSWORD", "IntegrationTest123!")
	if err := InitSchema(conn); err != nil {
		t.Fatalf("second InitSchema (idempotency): %v", err)
	}
}

// TestIntegration_UserCRUD exercises CreateUser → GetUserByUsername → GetUserByID.
func TestIntegration_UserCRUD(t *testing.T) {
	conn := openTestDB(t)

	username := fmt.Sprintf("testuser_%d", time.Now().UnixNano())
	hash := security.HashPassword("TestPass1!")

	if err := CreateUser(conn, username, hash, "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, err := GetUserByUsername(conn, username)
	if err != nil {
		t.Fatalf("GetUserByUsername: %v", err)
	}
	if u == nil {
		t.Fatal("GetUserByUsername returned nil")
	}
	if u.Username != username {
		t.Errorf("Username: got %q want %q", u.Username, username)
	}
	if u.Role != "viewer" {
		t.Errorf("Role: got %q want viewer", u.Role)
	}
	u2, err := GetUserByID(conn, u.ID)
	if err != nil {
		t.Fatalf("GetUserByID: %v", err)
	}
	if u2 == nil {
		t.Fatal("GetUserByID returned nil")
	}
	if u2.ID != u.ID {
		t.Errorf("ID mismatch: GetUserByID=%d GetUserByUsername=%d", u2.ID, u.ID)
	}
}

// TestIntegration_AuthLog exercises LogAuth and GetAuthLogs.
func TestIntegration_AuthLog(t *testing.T) {
	conn := openTestDB(t)

	if err := LogAuth(conn, "alice", "192.0.2.1", true, "login"); err != nil {
		t.Fatalf("LogAuth success: %v", err)
	}
	if err := LogAuth(conn, "alice", "192.0.2.1", false, "bad password"); err != nil {
		t.Fatalf("LogAuth failure: %v", err)
	}
	_, total, err := GetAuthLogs(conn, "", "", 1, 10)
	if err != nil {
		t.Fatalf("GetAuthLogs: %v", err)
	}
	if total < 2 {
		t.Errorf("expected at least 2 auth log entries, got total=%d", total)
	}
}

// TestIntegration_CertCRUD exercises InsertCert, GetCert, UpdateCertStatus.
func TestIntegration_CertCRUD(t *testing.T) {
	conn := openTestDB(t)

	serial := fmt.Sprintf("SN%d", time.Now().UnixNano())
	now := time.Now()
	expires := now.Add(365 * 24 * time.Hour)
	cert := &models.Certificate{
		Name:      "test-cert",
		Domain:    "example.com",
		CertPath:  "/tmp/test.crt",
		KeyPath:   "/tmp/test.key",
		IssuedAt:  &now,
		ExpiresAt: &expires,
		Serial:    serial,
		KeyType:   "EC:P-256",
	}
	if err := InsertCert(conn, cert); err != nil {
		t.Fatalf("InsertCert: %v", err)
	}
	found, err := GetCertBySerial(conn, serial)
	if err != nil {
		t.Fatalf("GetCertBySerial: %v", err)
	}
	if found == nil {
		t.Fatal("GetCertBySerial returned nil")
	}
	if err := UpdateCertStatus(conn, found.ID, "revoked"); err != nil {
		t.Fatalf("UpdateCertStatus: %v", err)
	}
	updated, err := GetCert(conn, found.ID)
	if err != nil {
		t.Fatalf("GetCert after update: %v", err)
	}
	if updated == nil {
		t.Fatal("GetCert returned nil after status update")
	}
	if updated.Status != "revoked" {
		t.Errorf("Status: got %q want revoked", updated.Status)
	}
}

// TestIntegration_RecoveryCodes exercises ReplaceRecoveryCodes /
// GetUnusedRecoveryCodes / UseRecoveryCode.
func TestIntegration_RecoveryCodes(t *testing.T) {
	conn := openTestDB(t)

	username := fmt.Sprintf("rc_user_%d", time.Now().UnixNano())
	if err := CreateUser(conn, username, security.HashPassword("Pw1!Pw1!"), "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := GetUserByUsername(conn, username)
	if u == nil {
		t.Fatal("user not found after create")
	}

	hashes := []string{
		security.HashPassword("CODE-0001"),
		security.HashPassword("CODE-0002"),
	}
	if err := ReplaceRecoveryCodes(conn, u.ID, hashes); err != nil {
		t.Fatalf("ReplaceRecoveryCodes: %v", err)
	}

	codes, err := GetUnusedRecoveryCodes(conn, u.ID)
	if err != nil {
		t.Fatalf("GetUnusedRecoveryCodes: %v", err)
	}
	if len(codes) != 2 {
		t.Fatalf("expected 2 unused recovery codes, got %d", len(codes))
	}

	for id := range codes {
		if err := UseRecoveryCode(conn, id); err != nil {
			t.Fatalf("UseRecoveryCode id=%d: %v", id, err)
		}
		break
	}

	remaining, err := GetUnusedRecoveryCodes(conn, u.ID)
	if err != nil {
		t.Fatalf("GetUnusedRecoveryCodes after use: %v", err)
	}
	if len(remaining) != 1 {
		t.Errorf("expected 1 remaining recovery code, got %d", len(remaining))
	}
}

// TestIntegration_TOTPLastStep exercises GetTOTPLastStep / SetTOTPLastStep.
func TestIntegration_TOTPLastStep(t *testing.T) {
	conn := openTestDB(t)

	username := fmt.Sprintf("totp_user_%d", time.Now().UnixNano())
	if err := CreateUser(conn, username, security.HashPassword("Pw1!Pw1!"), "viewer"); err != nil {
		t.Fatalf("CreateUser: %v", err)
	}
	u, _ := GetUserByUsername(conn, username)
	if u == nil {
		t.Fatal("user not found")
	}

	ctx := context.Background()
	step, err := GetTOTPLastStep(ctx, conn, u.ID)
	if err != nil {
		t.Fatalf("GetTOTPLastStep: %v", err)
	}
	if step != 0 {
		t.Errorf("initial step: got %d want 0", step)
	}

	if err := SetTOTPLastStep(ctx, conn, u.ID, 12345); err != nil {
		t.Fatalf("SetTOTPLastStep: %v", err)
	}
	step, err = GetTOTPLastStep(ctx, conn, u.ID)
	if err != nil {
		t.Fatalf("GetTOTPLastStep after set: %v", err)
	}
	if step != 12345 {
		t.Errorf("step after set: got %d want 12345", step)
	}
}
