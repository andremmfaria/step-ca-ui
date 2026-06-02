package db

import (
	"context"
	"database/sql"
	"time"

	"step-ui/models"
)

// InitLESchema creates the ACME/Let's Encrypt certificate tables if absent.
func InitLESchema(d *sql.DB) error {
	schema := `
	CREATE TABLE IF NOT EXISTS le_certificates (
		id         SERIAL PRIMARY KEY,
		domain     VARCHAR(255) NOT NULL UNIQUE,
		email      VARCHAR(255),
		provider   VARCHAR(50) DEFAULT 'http01',
		cert_path  TEXT,
		key_path   TEXT,
		issued_at  TIMESTAMPTZ,
		expires_at TIMESTAMPTZ,
		auto_renew BOOLEAN DEFAULT TRUE,
		status     VARCHAR(20) DEFAULT 'pending',
		last_error TEXT,
		created_at TIMESTAMPTZ DEFAULT NOW(),
		updated_at TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS le_settings (
		id            SERIAL PRIMARY KEY,
		email         VARCHAR(255),
		provider      VARCHAR(50) DEFAULT 'http01',
		cf_token      TEXT,
		cf_zone_id    TEXT,
		r53_key_id    TEXT,
		r53_secret    TEXT,
		r53_region    VARCHAR(50) DEFAULT 'us-east-1',
		updated_at    TIMESTAMPTZ DEFAULT NOW()
	);

	CREATE TABLE IF NOT EXISTS le_logs (
		id         SERIAL PRIMARY KEY,
		domain     VARCHAR(255),
		action     VARCHAR(50),
		message    TEXT,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_le_logs_created ON le_logs(created_at);

	-- Single settings row
	INSERT INTO le_settings (id, email) VALUES (1, '') ON CONFLICT (id) DO NOTHING;
	`
	_, err := d.ExecContext(context.Background(), schema)
	return err
}

// ─── LE Certificates ──────────────────────────────────────────────────────────

// GetLECerts returns all ACME certificates ordered by creation time.
func GetLECerts(ctx context.Context, d *sql.DB) ([]*models.LECertificate, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,domain,COALESCE(email,''),COALESCE(provider,'http01'),
		COALESCE(cert_path,''),COALESCE(key_path,''),issued_at,expires_at,
		auto_renew,COALESCE(status,'pending'),COALESCE(last_error,''),created_at,updated_at
		FROM le_certificates ORDER BY created_at DESC`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var certs []*models.LECertificate
	for rows.Next() {
		c := &models.LECertificate{}
		if err := rows.Scan(&c.ID, &c.Domain, &c.Email, &c.Provider,
			&c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt,
			&c.AutoRenew, &c.Status, &c.LastError, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return certs, nil
}

// GetLECert returns the ACME certificate with the given ID, or nil if not found.
func GetLECert(ctx context.Context, d *sql.DB, id int) (*models.LECertificate, error) {
	c := &models.LECertificate{}
	err := d.QueryRowContext(ctx, `SELECT id,domain,COALESCE(email,''),COALESCE(provider,'http01'),
		COALESCE(cert_path,''),COALESCE(key_path,''),issued_at,expires_at,
		auto_renew,COALESCE(status,'pending'),COALESCE(last_error,''),created_at,updated_at
		FROM le_certificates WHERE id=$1`, id).
		Scan(&c.ID, &c.Domain, &c.Email, &c.Provider,
			&c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt,
			&c.AutoRenew, &c.Status, &c.LastError, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// CreateLECert inserts a new pending ACME certificate record and returns its ID.
func CreateLECert(ctx context.Context, d *sql.DB, domain, email, provider string, autoRenew bool) (int, error) {
	var id int
	err := d.QueryRowContext(ctx, `INSERT INTO le_certificates (domain,email,provider,auto_renew,status)
		VALUES ($1,$2,$3,$4,'pending') RETURNING id`,
		domain, email, provider, autoRenew).Scan(&id)
	return id, err
}

// UpdateLECertStatus updates the status and last error message for an ACME certificate.
func UpdateLECertStatus(ctx context.Context, d *sql.DB, id int, status, lastError string) error {
	_, err := d.ExecContext(ctx, `UPDATE le_certificates SET status=$1,last_error=$2,updated_at=NOW() WHERE id=$3`,
		status, lastError, id)
	return err
}

// UpdateLECertPaths stores the file paths and validity timestamps for an issued certificate.
func UpdateLECertPaths(ctx context.Context, d *sql.DB, id int, certPath, keyPath string, issuedAt, expiresAt *time.Time) error {
	_, err := d.ExecContext(ctx, `UPDATE le_certificates SET cert_path=$1,key_path=$2,issued_at=$3,expires_at=$4,
		status='active',last_error='',updated_at=NOW() WHERE id=$5`,
		certPath, keyPath, issuedAt, expiresAt, id)
	return err
}

// UpdateLECertAutoRenew toggles the auto-renewal flag for an ACME certificate.
func UpdateLECertAutoRenew(ctx context.Context, d *sql.DB, id int, autoRenew bool) error {
	_, err := d.ExecContext(ctx, `UPDATE le_certificates SET auto_renew=$1,updated_at=NOW() WHERE id=$2`, autoRenew, id)
	return err
}

// DeleteLECert removes an ACME certificate record by ID.
func DeleteLECert(ctx context.Context, d *sql.DB, id int) error {
	_, err := d.ExecContext(ctx, `DELETE FROM le_certificates WHERE id=$1`, id)
	return err
}

// GetLECertsForRenewal returns active certificates expiring within 30 days that have auto-renew enabled.
func GetLECertsForRenewal(ctx context.Context, d *sql.DB) ([]*models.LECertificate, error) {
	rows, err := d.QueryContext(ctx, `SELECT id,domain,COALESCE(email,''),COALESCE(provider,'http01'),
		COALESCE(cert_path,''),COALESCE(key_path,''),issued_at,expires_at,
		auto_renew,COALESCE(status,'pending'),COALESCE(last_error,''),created_at,updated_at
		FROM le_certificates
		WHERE auto_renew=true AND status='active'
		AND expires_at < NOW() + INTERVAL '30 days'`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var certs []*models.LECertificate
	for rows.Next() {
		c := &models.LECertificate{}
		if err := rows.Scan(&c.ID, &c.Domain, &c.Email, &c.Provider,
			&c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt,
			&c.AutoRenew, &c.Status, &c.LastError, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return certs, nil
}

// ─── LE Settings ──────────────────────────────────────────────────────────────

// GetLESettings returns the single ACME provider settings row.
func GetLESettings(ctx context.Context, d *sql.DB) (*models.LESettings, error) {
	s := &models.LESettings{}
	err := d.QueryRowContext(ctx, `SELECT id,COALESCE(email,''),COALESCE(provider,'http01'),
		COALESCE(cf_token,''),COALESCE(cf_zone_id,''),
		COALESCE(r53_key_id,''),COALESCE(r53_secret,''),COALESCE(r53_region,'us-east-1'),
		updated_at FROM le_settings WHERE id=1`).
		Scan(&s.ID, &s.Email, &s.Provider, &s.CFToken, &s.CFZoneID,
			&s.R53KeyID, &s.R53SecretKey, &s.R53Region, &s.UpdatedAt)
	if err == sql.ErrNoRows {
		return &models.LESettings{Provider: "http01"}, nil
	}
	return s, err
}

// SaveLESettings upserts the ACME provider settings.
func SaveLESettings(ctx context.Context, d *sql.DB, s *models.LESettings) error {
	_, err := d.ExecContext(ctx, `INSERT INTO le_settings (id,email,provider,cf_token,cf_zone_id,r53_key_id,r53_secret,r53_region,updated_at)
		VALUES (1,$1,$2,$3,$4,$5,$6,$7,NOW())
		ON CONFLICT (id) DO UPDATE SET
			email=$1,provider=$2,cf_token=$3,cf_zone_id=$4,
			r53_key_id=$5,r53_secret=$6,r53_region=$7,updated_at=NOW()`,
		s.Email, s.Provider, s.CFToken, s.CFZoneID,
		s.R53KeyID, s.R53SecretKey, s.R53Region)
	return err
}

// ─── LE Logs ──────────────────────────────────────────────────────────────────

// AddLELog appends a log entry for an ACME domain event (best-effort; errors are discarded).
func AddLELog(ctx context.Context, d *sql.DB, domain, action, message string) {
	_, _ = d.ExecContext(ctx, `INSERT INTO le_logs (domain,action,message) VALUES ($1,$2,$3)`, domain, action, message)
}

// GetLELogs returns up to limit log entries for the given domain (or all domains if domain is empty).
func GetLELogs(ctx context.Context, d *sql.DB, domain string, limit int) ([]*models.LELog, error) {
	var rows *sql.Rows
	var err error
	if domain != "" {
		rows, err = d.QueryContext(ctx, `SELECT id,COALESCE(domain,''),COALESCE(action,''),COALESCE(message,''),created_at
			FROM le_logs WHERE domain=$1 ORDER BY created_at DESC LIMIT $2`, domain, limit)
	} else {
		rows, err = d.QueryContext(ctx, `SELECT id,COALESCE(domain,''),COALESCE(action,''),COALESCE(message,''),created_at
			FROM le_logs ORDER BY created_at DESC LIMIT $1`, limit)
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var logs []*models.LELog
	for rows.Next() {
		l := &models.LELog{}
		if err := rows.Scan(&l.ID, &l.Domain, &l.Action, &l.Message, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// ─── Stats ────────────────────────────────────────────────────────────────────

// GetLEStats returns aggregate counts of ACME certificates by state.
func GetLEStats(ctx context.Context, d *sql.DB) (total, active, expiringSoon, expired int) {
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM le_certificates`).Scan(&total)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM le_certificates WHERE status='active'`).Scan(&active)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM le_certificates WHERE status='active' AND expires_at < NOW() + INTERVAL '30 days'`).Scan(&expiringSoon)
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM le_certificates WHERE expires_at < NOW() OR status='expired'`).Scan(&expired)
	return total, active, expiringSoon, expired
}

// ─── Helper ───────────────────────────────────────────────────────────────────

// LECertExists returns true when a certificate for the given domain already exists.
func LECertExists(ctx context.Context, d *sql.DB, domain string) bool {
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM le_certificates WHERE domain=$1`, domain).Scan(&n)
	return n > 0
}

// GetLECertByDomain returns the certificate for the given domain, or nil if not found.
func GetLECertByDomain(ctx context.Context, d *sql.DB, domain string) (*models.LECertificate, error) {
	c := &models.LECertificate{}
	err := d.QueryRowContext(ctx, `SELECT id,domain,COALESCE(email,''),COALESCE(provider,'http01'),
		COALESCE(cert_path,''),COALESCE(key_path,''),issued_at,expires_at,
		auto_renew,COALESCE(status,'pending'),COALESCE(last_error,''),created_at,updated_at
		FROM le_certificates WHERE domain=$1`, domain).
		Scan(&c.ID, &c.Domain, &c.Email, &c.Provider,
			&c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt,
			&c.AutoRenew, &c.Status, &c.LastError, &c.CreatedAt, &c.UpdatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// GetLECertCount returns the total number of ACME certificate records.
func GetLECertCount(ctx context.Context, d *sql.DB) int {
	var n int
	_ = d.QueryRowContext(ctx, `SELECT COUNT(*) FROM le_certificates`).Scan(&n)
	return n
}
