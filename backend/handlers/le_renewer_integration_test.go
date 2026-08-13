//go:build integration

package handlers

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"step-ui/config"

	appdb "step-ui/db"
)

// openRenewerTestDB opens the integration Postgres, creates the schemas
// runRenewal touches, and truncates the LE tables afterwards.
func openRenewerTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dsn := os.Getenv("INTEGRATION_DB_DSN")
	if dsn == "" {
		dsn = "postgres://stepui_test:stepui_test@localhost:5432/stepui_test?sslmode=disable"
	}
	conn, err := appdb.Connect(dsn)
	if err != nil {
		t.Skipf("integration DB not available (%v) — skipping", err)
	}
	if err := appdb.InitLESchema(conn); err != nil {
		_ = conn.Close()
		t.Fatalf("InitLESchema: %v", err)
	}
	if err := appdb.InitNotificationSchema(conn); err != nil {
		_ = conn.Close()
		t.Fatalf("InitNotificationSchema: %v", err)
	}
	t.Cleanup(func() {
		_, _ = conn.Exec(`TRUNCATE le_certificates, le_logs RESTART IDENTITY CASCADE`) //nolint:noctx
		_ = conn.Close()
	})
	return conn
}

// TestIntegration_RunRenewalSkipsOutOfPolicyCerts is the auto-renewer half of
// V6: a name that has fallen outside ALLOWED_DOMAIN_SUFFIXES must stop renewing
// itself, and must not take the rest of the run down with it.
func TestIntegration_RunRenewalSkipsOutOfPolicyCerts(t *testing.T) {
	conn := openRenewerTestDB(t)
	ctx := context.Background()

	// Both are due: expires_at inside the 30-day renewal window.
	due := time.Now().Add(24 * time.Hour)
	seed := func(domain string) int {
		t.Helper()
		var id int
		err := conn.QueryRowContext(ctx,
			`INSERT INTO le_certificates (domain,email,provider,status,auto_renew,expires_at)
			 VALUES ($1,'ops@example.com','cloudflare','active',true,$2) RETURNING id`,
			domain, due).Scan(&id)
		if err != nil {
			t.Fatalf("seed %s: %v", domain, err)
		}
		return id
	}
	outside := seed(fmt.Sprintf("legacy-%d.notcovered.net", time.Now().UnixNano()))
	inside := seed(fmt.Sprintf("app-%d.example.com", time.Now().UnixNano()))

	h := &Handler{
		db:  conn,
		cfg: &config.Config{AllowedDomainSuffixes: []string{"example.com"}},
	}
	h.runRenewal()

	statusOf := func(id int) string {
		t.Helper()
		var status string
		if err := conn.QueryRowContext(ctx, `SELECT status FROM le_certificates WHERE id=$1`, id).Scan(&status); err != nil {
			t.Fatalf("status of %d: %v", id, err)
		}
		return status
	}

	if got := statusOf(outside); got != "active" {
		t.Errorf("out-of-policy certificate: status %q, want it left alone at %q", got, "active")
	}

	// The skip must reach the LE log page, not only the process log: a
	// certificate skipped in silence expires in thirty days.
	var domain, message string
	if err := conn.QueryRowContext(ctx,
		`SELECT c.domain, l.message FROM le_certificates c
		 JOIN le_logs l ON l.domain = c.domain
		 WHERE c.id=$1 ORDER BY l.id DESC LIMIT 1`, outside).Scan(&domain, &message); err != nil {
		t.Fatalf("out-of-policy certificate wrote no le_logs entry: %v", err)
	}
	for _, want := range []string{"skipped", "ALLOWED_DOMAIN_SUFFIXES", domain} {
		if !strings.Contains(message, want) {
			t.Errorf("le_logs message %q does not mention %q", message, want)
		}
	}
	// The covered one was attempted and failed at ACME (no credentials in the
	// test environment), which is what proves the skip did not abort the run.
	if got := statusOf(inside); got != "error" {
		t.Errorf("in-policy certificate: status %q, want the run to have reached it", got)
	}
}
