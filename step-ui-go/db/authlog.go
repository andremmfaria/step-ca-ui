package db

import (
	"database/sql"
	"fmt"
	"log/slog"
	"time"

	"step-ui/models"
)

// ─── Auth Log ─────────────────────────────────────────────────────────────────

// LogAuth records an authentication event and updates last_login on success.
func LogAuth(d *sql.DB, username, ip string, success bool, reason string) error {
	if d == nil {
		return nil
	}
	_, err := d.Exec(`INSERT INTO auth_log (username,ip,success,reason) VALUES ($1,$2,$3,$4)`, //nolint:noctx // pre-existing signature; context adoption tracked in P3-8
		username, ip, success, reason)
	if success {
		if loginErr := UpdateUserLogin(d, username, ip); loginErr != nil {
			slog.Warn("UpdateUserLogin failed", "err", loginErr)
		}
	}
	return err
}

// GetAuthLogs returns paginated auth log entries with optional search/filter.
func GetAuthLogs(d *sql.DB, search, filter string, page, limit int) ([]*models.AuthLog, int, error) {
	where := "WHERE 1=1"
	args := []interface{}{}
	i := 1
	if search != "" {
		where += fmt.Sprintf(" AND (username ILIKE $%d OR ip ILIKE $%d)", i, i+1)
		args = append(args, "%"+search+"%", "%"+search+"%")
		i += 2
	}
	if filter == "fail" {
		where += fmt.Sprintf(" AND success=$%d", i)
		args = append(args, false)
		i++
	} else if filter == "ok" {
		where += fmt.Sprintf(" AND success=$%d", i)
		args = append(args, true)
		i++
	}
	var total int
	if err := d.QueryRow(`SELECT COUNT(*) FROM auth_log `+where, args...).Scan(&total); err != nil { //nolint:noctx // pre-existing signature
		return nil, 0, err
	}
	offset := (page - 1) * limit
	rows, err := d.Query(`SELECT id,username,ip,success,COALESCE(reason,''),created_at FROM auth_log `+where+ //nolint:noctx,gosec // pre-existing; where is built from app-controlled strings, not user input
		fmt.Sprintf(` ORDER BY created_at DESC LIMIT $%d OFFSET $%d`, i, i+1),
		append(args, limit, offset)...)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = rows.Close() }()
	var logs []*models.AuthLog
	for rows.Next() {
		l := &models.AuthLog{}
		if err := rows.Scan(&l.ID, &l.Username, &l.IP, &l.Success, &l.Reason, &l.CreatedAt); err != nil {
			return nil, 0, err
		}
		logs = append(logs, l)
	}
	return logs, total, nil
}

// GetUserAuthLogs returns recent auth log entries for a specific user.
func GetUserAuthLogs(d *sql.DB, username string, limit int) ([]*models.AuthLog, error) {
	rows, err := d.Query(`SELECT id,username,ip,success,COALESCE(reason,''),created_at FROM auth_log WHERE username=$1 ORDER BY created_at DESC LIMIT $2`, //nolint:noctx // pre-existing signature
		username, limit)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var logs []*models.AuthLog
	for rows.Next() {
		l := &models.AuthLog{}
		if err := rows.Scan(&l.ID, &l.Username, &l.IP, &l.Success, &l.Reason, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	return logs, nil
}

// GetFailCount returns the number of failed logins for a user since a given time.
func GetFailCount(d *sql.DB, username string, since time.Time) int {
	var n int
	_ = d.QueryRow(`SELECT COUNT(*) FROM auth_log WHERE username=$1 AND success=false AND created_at>$2`, username, since).Scan(&n) //nolint:noctx // pre-existing signature
	return n
}

// GetAuthStats returns aggregate success/failure counts.
func GetAuthStats(d *sql.DB) (ok, fail int) {
	_ = d.QueryRow(`SELECT COUNT(*) FROM auth_log WHERE success=true`).Scan(&ok)    //nolint:noctx // pre-existing signature
	_ = d.QueryRow(`SELECT COUNT(*) FROM auth_log WHERE success=false`).Scan(&fail) //nolint:noctx // pre-existing signature
	return ok, fail
}
