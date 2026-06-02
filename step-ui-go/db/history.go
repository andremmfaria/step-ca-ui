package db

import (
	"database/sql"
	"fmt"
	"step-ui/models"
	"strings"
)

// ─── Cert History ─────────────────────────────────────────────────────────────

// InsertHistory appends a cert_history row.
func InsertHistory(d *sql.DB, action, certName, domain, details, username, role string) error {
	_, err := d.Exec(`INSERT INTO cert_history (action,cert_name,domain,details,username,role) VALUES ($1,$2,$3,$4,$5,$6)`, //nolint:noctx // pre-existing signature
		action, certName, domain, details, username, role)
	return err
}

// GetHistory operates on the gethistory record.
func GetHistory(d *sql.DB, actions []string, cert string, page, limit int) ([]*models.CertHistory, int, error) {
	where := "WHERE 1=1"
	args := []interface{}{}
	i := 1
	if len(actions) > 0 {
		placeholders := make([]string, 0, len(actions))
		for _, a := range actions {
			placeholders = append(placeholders, fmt.Sprintf("$%d", i))
			args = append(args, a)
			i++
		}
		where += " AND action IN (" + strings.Join(placeholders, ",") + ")"
	}
	if cert != "" {
		where += fmt.Sprintf(" AND cert_name=$%d", i)
		args = append(args, cert)
		i++
	}
	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM cert_history `+where, args...).Scan(&total); err != nil { //nolint:noctx // pre-existing signature
		return nil, 0, err
	}
	offset := (page - 1) * limit
	rows, err := d.Query(`SELECT id,action,cert_name,COALESCE(domain,''),COALESCE(details,''),COALESCE(username,''),COALESCE(role,''),created_at FROM cert_history `+where+ //nolint:noctx,gosec // pre-existing; where is built from app-controlled strings
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, i, i+1),
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var list []*models.CertHistory
	for rows.Next() {
		h := &models.CertHistory{}
		if err := rows.Scan(&h.ID, &h.Action, &h.CertName, &h.Domain, &h.Details, &h.Username, &h.Role, &h.CreatedAt); err == nil {
			list = append(list, h)
		}
	}
	return list, total, nil
}
