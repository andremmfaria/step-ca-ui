package db

import (
	"database/sql"
	"step-ui/models"
)

// ─── Certificates ─────────────────────────────────────────────────────────────

// GetCerts returns all certificates, optionally filtered by status.
func GetCerts(d *sql.DB, statusFilter string) ([]*models.Certificate, error) {
	q := `SELECT id,name,domain,cert_path,key_path,issued_at,expires_at,COALESCE(serial,''),status,COALESCE(key_type,''),created_at FROM certificates`
	var args []interface{}
	if statusFilter != "" {
		q += ` WHERE status=$1`
		args = append(args, statusFilter)
	}
	q += ` ORDER BY expires_at ASC`
	rows, err := d.Query(q, args...) //nolint:noctx // pre-existing signature; context adoption tracked in P3-8
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var certs []*models.Certificate
	for rows.Next() {
		c := &models.Certificate{}
		if err := rows.Scan(&c.ID, &c.Name, &c.Domain, &c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt, &c.Serial, &c.Status, &c.KeyType, &c.CreatedAt); err != nil {
			return nil, err
		}
		certs = append(certs, c)
	}
	return certs, nil
}

// GetCert returns the certificate with the given id, or (nil, nil) when not found.
func GetCert(d *sql.DB, id int) (*models.Certificate, error) {
	c := &models.Certificate{}
	err := d.QueryRow( //nolint:noctx // pre-existing signature
		`SELECT id,name,domain,cert_path,key_path,issued_at,expires_at,COALESCE(serial,''),status,COALESCE(key_type,''),created_at FROM certificates WHERE id=$1`, id).
		Scan(&c.ID, &c.Name, &c.Domain, &c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt, &c.Serial, &c.Status, &c.KeyType, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}

// InsertCert inserts a certificate row, upserting on serial conflict.
func InsertCert(d *sql.DB, c *models.Certificate) error {
	_, err := d.Exec( //nolint:noctx // pre-existing signature
		`INSERT INTO certificates (name,domain,cert_path,key_path,issued_at,expires_at,serial,status,key_type)
		VALUES ($1,$2,$3,$4,$5,$6,$7,'active',$8)
		ON CONFLICT (serial) DO UPDATE SET
			name=$1,domain=$2,cert_path=$3,key_path=$4,issued_at=$5,expires_at=$6,key_type=$8,status='active'`,
		c.Name, c.Domain, c.CertPath, c.KeyPath, c.IssuedAt, c.ExpiresAt, c.Serial, c.KeyType)
	return err
}

// UpdateCertStatus sets the status field for a certificate row.
func UpdateCertStatus(d *sql.DB, id int, status string) error {
	_, err := d.Exec(`UPDATE certificates SET status=$1 WHERE id=$2`, status, id) //nolint:noctx // pre-existing signature
	return err
}

// GetCertBySerial returns the certificate with the given serial, or (nil, nil) when not found.
func GetCertBySerial(d *sql.DB, serial string) (*models.Certificate, error) {
	c := &models.Certificate{}
	err := d.QueryRow( //nolint:noctx // pre-existing signature
		`SELECT id,name,domain,cert_path,key_path,issued_at,expires_at,COALESCE(serial,''),status,COALESCE(key_type,''),created_at FROM certificates WHERE serial=$1`, serial).
		Scan(&c.ID, &c.Name, &c.Domain, &c.CertPath, &c.KeyPath, &c.IssuedAt, &c.ExpiresAt, &c.Serial, &c.Status, &c.KeyType, &c.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return c, err
}
