package db

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"log/slog"
	"os"
	"time"

	"step-ui/security"

	_ "github.com/lib/pq" // Postgres driver registration
)

// Connect opens and verifies a Postgres connection.
func Connect(dsn string) (*sql.DB, error) {
	conn, err := sql.Open("postgres", dsn)
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(25)
	conn.SetMaxIdleConns(5)
	conn.SetConnMaxLifetime(5 * time.Minute)
	if err = conn.Ping(); err != nil { //nolint:noctx // pre-existing signature
		return nil, fmt.Errorf("db ping failed: %w", err)
	}
	return conn, nil
}

// InitSchema operates on the initschema record.
func InitSchema(d *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS users (
		id           SERIAL PRIMARY KEY,
		username     VARCHAR(100) UNIQUE NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		role         VARCHAR(20) DEFAULT 'viewer',
		is_active    BOOLEAN DEFAULT TRUE,
		created_at   TIMESTAMPTZ DEFAULT NOW(),
		last_login   TIMESTAMPTZ,
		last_ip      VARCHAR(45)
	);

	CREATE TABLE IF NOT EXISTS auth_log (
		id         SERIAL PRIMARY KEY,
		username   VARCHAR(100),
		ip         VARCHAR(45),
		success    BOOLEAN,
		reason     TEXT,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_auth_log_username ON auth_log(username);
	CREATE INDEX IF NOT EXISTS idx_auth_log_created  ON auth_log(created_at);

	CREATE TABLE IF NOT EXISTS certificates (
		id         SERIAL PRIMARY KEY,
		name       VARCHAR(255) NOT NULL,
		domain     VARCHAR(255) NOT NULL,
		cert_path  TEXT,
		key_path   TEXT,
		issued_at  TIMESTAMPTZ,
		expires_at TIMESTAMPTZ,
		serial     VARCHAR(100) UNIQUE,
		status     VARCHAR(20) DEFAULT 'active',
		key_type   VARCHAR(50),
		created_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS cert_history (
		id         SERIAL PRIMARY KEY,
		action     VARCHAR(50) NOT NULL,
		cert_name  VARCHAR(255) NOT NULL,
		domain     VARCHAR(255),
		details    TEXT,
		username   VARCHAR(100),
		role       VARCHAR(20),
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_hist_created ON cert_history(created_at);
	`
	if _, err := d.Exec(schema); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	// Post-creation migrations (columns added to tables created above).
	// migration: users.theme
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS theme VARCHAR(10) DEFAULT 'dark'`); err != nil { //nolint:noctx // pre-existing signature
		return err
	}

	// -- migration: user profile fields
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS display_name VARCHAR(100) DEFAULT ''`); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS email VARCHAR(200) DEFAULT ''`); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_enabled BOOLEAN DEFAULT false`); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_secret TEXT DEFAULT ''`); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_pending_secret TEXT DEFAULT ''`); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	if _, err := d.Exec( //nolint:noctx // pre-existing signature
		` 
		CREATE TABLE IF NOT EXISTS user_recovery_codes (
			id SERIAL PRIMARY KEY,
			user_id INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
			code_hash TEXT NOT NULL,
			used_at TIMESTAMPTZ,
			created_at TIMESTAMPTZ DEFAULT NOW()
		);
		CREATE INDEX IF NOT EXISTS idx_recovery_codes_user ON user_recovery_codes(user_id);
	`,
	); err != nil {
		return err
	}

	// -- migration: totp_last_step for replay protection (P2-1)
	if _, err := d.ExecContext(context.Background(), `ALTER TABLE users ADD COLUMN IF NOT EXISTS totp_last_step BIGINT DEFAULT 0`); err != nil {
		return err
	}

	// Создаём admin если нет пользователей
	var count int
	if err := d.QueryRow(`SELECT COUNT(*) FROM users`).Scan(&count); err != nil { //nolint:noctx // pre-existing signature
		return fmt.Errorf("counting users: %w", err)
	}
	if count == 0 {
		adminPwd, err := resolveAdminPassword(os.Getenv)
		if err != nil {
			log.Fatal(err.Error())
		}
		slog.Info("seeding admin user from STEPUI_ADMIN_PASSWORD")
		if _, err := d.Exec(`INSERT INTO users (username,password_hash,role,is_active) VALUES ($1,$2,'admin',true)`, //nolint:noctx // pre-existing signature
			"admin", security.HashPassword(adminPwd)); err != nil {
			return fmt.Errorf("seeding admin user: %w", err)
		}
		slog.Info("admin user seeded; remove STEPUI_ADMIN_PASSWORD from the environment after first login")
	}

	// -- migration: certificates.issue_duration (P3-8) stores the issuance
	// duration so Renew can replay it instead of using a hardcoded default.
	if _, err := d.Exec(`ALTER TABLE certificates ADD COLUMN IF NOT EXISTS issue_duration VARCHAR(20) DEFAULT ''`); err != nil { //nolint:noctx // pre-existing signature
		return fmt.Errorf("migration certificates.issue_duration: %w", err)
	}

	// -- temp_users_migration_v1: errors are checked so a failing migration is
	// detected rather than silently skipped (P2-5 fix).
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS expires_at TIMESTAMPTZ`); err != nil { //nolint:noctx // pre-existing signature
		return fmt.Errorf("migration temp_users expires_at: %w", err)
	}
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS is_temporary BOOLEAN DEFAULT false`); err != nil { //nolint:noctx // pre-existing signature
		return fmt.Errorf("migration temp_users is_temporary: %w", err)
	}
	if _, err := d.Exec(`ALTER TABLE users ADD COLUMN IF NOT EXISTS temp_note TEXT DEFAULT ''`); err != nil { //nolint:noctx // pre-existing signature
		return fmt.Errorf("migration temp_users temp_note: %w", err)
	}
	return nil
}

// resolveAdminPassword returns the value of STEPUI_ADMIN_PASSWORD from the
// provided lookup function, or an error if it is empty. The caller must treat
// the error as fatal — seeding a literal password would create a known
// network-reachable credential. The getenv parameter allows injection in tests.
func resolveAdminPassword(getenv func(string) string) (string, error) {
	pw := getenv("STEPUI_ADMIN_PASSWORD")
	if pw == "" {
		return "", fmt.Errorf("[FATAL] No admin user exists and STEPUI_ADMIN_PASSWORD is not set. " +
			"Set STEPUI_ADMIN_PASSWORD to a strong password and restart. " +
			"Example: STEPUI_ADMIN_PASSWORD=$(openssl rand -base64 32)")
	}
	return pw, nil
}
