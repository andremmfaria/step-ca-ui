package db

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"step-ui/models"
)

// ─── Users ────────────────────────────────────────────────────────────────────

// GetUserByUsername looks up a user by username, returning nil when not found.
func GetUserByUsername(d *sql.DB, username string) (*models.User, error) {
	u := &models.User{}
	err := d.QueryRow( //nolint:noctx // pre-existing signature
		`SELECT id,username,password_hash,role,is_active,created_at,last_login,last_ip, 
		COALESCE(totp_enabled,false),COALESCE(totp_secret,''),COALESCE(totp_pending_secret,'')
		FROM users WHERE username=$1`, username,
	).
		Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.LastLogin, &u.LastIP,
			&u.TOTPEnabled, &u.TOTPSecret, &u.TOTPPendingSecret)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}

// GetUserByID operates on the getuserbyid record.
func GetUserByID(d *sql.DB, id int) (*models.User, error) {
	u := &models.User{}
	var displayName, email, theme sql.NullString
	err := d.QueryRow( //nolint:noctx // pre-existing signature
		`SELECT id, username, password_hash, role, is_active, created_at, last_login, last_ip, 
		COALESCE(display_name,''), COALESCE(email,''), COALESCE(theme,'dark'),
		COALESCE(totp_enabled,false), COALESCE(totp_secret,''), COALESCE(totp_pending_secret,'')
		FROM users WHERE id=$1`, id,
	).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive,
		&u.CreatedAt, &u.LastLogin, &u.LastIP, &displayName, &email, &theme,
		&u.TOTPEnabled, &u.TOTPSecret, &u.TOTPPendingSecret,
	)
	if err != nil {
		return nil, err
	}
	u.DisplayName = displayName.String
	u.Email = email.String
	u.Theme = theme.String
	if u.Theme == "" {
		u.Theme = "dark"
	}
	return u, nil
}

// GetAllUsers operates on the getallusers record.
func GetAllUsers(d *sql.DB) ([]*models.User, error) {
	rows, err := d.Query(`SELECT id,username,password_hash,role,is_active,created_at,last_login,last_ip FROM users ORDER BY id`) //nolint:noctx // pre-existing signature
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var users []*models.User
	for rows.Next() {
		u := &models.User{}
		if err := rows.Scan(&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive, &u.CreatedAt, &u.LastLogin, &u.LastIP); err != nil {
			return nil, err
		}
		users = append(users, u)
	}
	return users, nil
}

// CreateUser persists a new active user account.
func CreateUser(d *sql.DB, username, passwordHash, role string) error {
	_, err := d.Exec(`INSERT INTO users (username,password_hash,role,is_active) VALUES ($1,$2,$3,true)`, //nolint:noctx // pre-existing signature
		username, passwordHash, role)
	return err
}

// UpdateUserRole operates on the updateuserrole record.
func UpdateUserRole(d *sql.DB, id int, role string) error {
	_, err := d.Exec(`UPDATE users SET role=$1 WHERE id=$2`, role, id) //nolint:noctx // pre-existing signature
	return err
}

// UpdateUserActive operates on the updateuseractive record.
func UpdateUserActive(d *sql.DB, id int, active bool) error {
	_, err := d.Exec(`UPDATE users SET is_active=$1 WHERE id=$2`, active, id) //nolint:noctx // pre-existing signature
	return err
}

// UpdateUserPassword operates on the updateuserpassword record.
func UpdateUserPassword(d *sql.DB, id int, hash string) error {
	_, err := d.Exec(`UPDATE users SET password_hash=$1 WHERE id=$2`, hash, id) //nolint:noctx // pre-existing signature
	return err
}

// UpdateUserInfo operates on the updateuserinfo record.
func UpdateUserInfo(d *sql.DB, id int, username, displayName, email string) error {
	_, err := d.Exec(`UPDATE users SET username=$1, display_name=$2, email=$3 WHERE id=$4`, //nolint:noctx // pre-existing signature
		username, displayName, email, id)
	return err
}

// UpdateUserTheme operates on the updateusertheme record.
func UpdateUserTheme(d *sql.DB, id int, theme string) error {
	_, err := d.Exec(`UPDATE users SET theme=$1 WHERE id=$2`, theme, id) //nolint:noctx // pre-existing signature
	return err
}

// UpdateUserTOTPPending operates on the updateusertotppendin record.
func UpdateUserTOTPPending(d *sql.DB, id int, secret string) error {
	_, err := d.Exec(`UPDATE users SET totp_pending_secret=$1 WHERE id=$2`, secret, id) //nolint:noctx // pre-existing signature
	return err
}

// EnableUserTOTP operates on the enableusertotp record.
func EnableUserTOTP(d *sql.DB, id int, secret string) error {
	_, err := d.Exec(`UPDATE users SET totp_enabled=true, totp_secret=$1, totp_pending_secret='' WHERE id=$2`, secret, id) //nolint:noctx // pre-existing signature
	return err
}

// DisableUserTOTP operates on the disableusertotp record.
func DisableUserTOTP(d *sql.DB, id int) error {
	_, err := d.ExecContext(context.Background(),
		`UPDATE users SET totp_enabled=false, totp_secret='', totp_pending_secret='', totp_last_step=0 WHERE id=$1`, id)
	if err != nil {
		return err
	}
	_, err = d.Exec(`DELETE FROM user_recovery_codes WHERE user_id=$1`, id) //nolint:noctx // pre-existing signature
	return err
}

// GetTOTPLastStep returns the last accepted TOTP timestep for a user (0 = never used).
func GetTOTPLastStep(ctx context.Context, d *sql.DB, userID int) (int64, error) {
	var step int64
	err := d.QueryRowContext(ctx, `SELECT COALESCE(totp_last_step, 0) FROM users WHERE id=$1`, userID).Scan(&step)
	if err != nil {
		return 0, err
	}
	return step, nil
}

// SetTOTPLastStep records the timestep of the last accepted TOTP code so that
// replay attempts using the same or an earlier step are rejected.
func SetTOTPLastStep(ctx context.Context, d *sql.DB, userID int, step int64) error {
	_, err := d.ExecContext(ctx, `UPDATE users SET totp_last_step=$1 WHERE id=$2`, step, userID)
	return err
}

// ReplaceRecoveryCodes operates on the replacerecoverycodes record.
func ReplaceRecoveryCodes(d *sql.DB, userID int, hashes []string) error {
	tx, err := d.Begin() //nolint:noctx // pre-existing signature
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.Exec(`DELETE FROM user_recovery_codes WHERE user_id=$1`, userID); err != nil { //nolint:noctx // pre-existing signature
		return err
	}
	for _, hash := range hashes {
		if _, err := tx.Exec(`INSERT INTO user_recovery_codes (user_id, code_hash) VALUES ($1,$2)`, userID, hash); err != nil { //nolint:noctx // pre-existing signature
			return err
		}
	}
	return tx.Commit()
}

// UseRecoveryCode operates on the userecoverycode record.
func UseRecoveryCode(d *sql.DB, codeID int) error {
	_, err := d.Exec(`UPDATE user_recovery_codes SET used_at=NOW() WHERE id=$1 AND used_at IS NULL`, codeID) //nolint:noctx // pre-existing signature
	return err
}

// GetUnusedRecoveryCodes operates on the getunusedrecoverycod record.
func GetUnusedRecoveryCodes(d *sql.DB, userID int) (map[int]string, error) {
	rows, err := d.Query(`SELECT id, code_hash FROM user_recovery_codes WHERE user_id=$1 AND used_at IS NULL ORDER BY id`, userID) //nolint:noctx // pre-existing signature
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[int]string{}
	for rows.Next() {
		var id int
		var hash string
		if err := rows.Scan(&id, &hash); err != nil {
			return nil, err
		}
		out[id] = hash
	}
	return out, nil
}

// UsernameExistsExceptID operates on the usernameexistsexcept record.
func UsernameExistsExceptID(d *sql.DB, username string, id int) (bool, error) {
	var exists bool
	err := d.QueryRow(`SELECT EXISTS(SELECT 1 FROM users WHERE username=$1 AND id<>$2)`, username, id).Scan(&exists) //nolint:noctx // pre-existing signature
	return exists, err
}

// UpdateUserLogin operates on the updateuserlogin record.
func UpdateUserLogin(d *sql.DB, username, ip string) error {
	_, err := d.Exec(`UPDATE users SET last_login=NOW(),last_ip=$1 WHERE username=$2`, ip, username) //nolint:noctx // pre-existing signature
	return err
}

// DeleteUser operates on the deleteuser record.
func DeleteUser(d *sql.DB, id int) error {
	var username string
	// best-effort: retrieve username for cascading auth_log cleanup; ignore errors
	_ = d.QueryRow(`SELECT username FROM users WHERE id=$1`, id).Scan(&username) //nolint:noctx // pre-existing signature
	if username != "" {
		if _, err := d.Exec(`DELETE FROM auth_log WHERE username=$1`, username); err != nil { //nolint:noctx // pre-existing signature
			return err
		}
	}
	_, err := d.Exec(`DELETE FROM users WHERE id=$1`, id) //nolint:noctx // pre-existing signature
	return err
}

// UpsertOIDCUser inserts or updates a user authenticated via OIDC.
// The sentinel password_hash "oidc:jumpcloud" is structurally rejected by
// VerifyPassword (not a bcrypt or legacy-SHA256 hash), so the account
// cannot be used for password login regardless of LOCAL_LOGIN_ENABLED.
// When syncRole is true the role column is updated on every login so the
// IdP groups remain authoritative.
func UpsertOIDCUser(d *sql.DB, username, displayName, role string, syncRole bool) (*models.User, error) {
	if d == nil {
		return nil, fmt.Errorf("db: nil connection")
	}
	if syncRole {
		_, err := d.Exec( //nolint:noctx // pre-existing signature
			` 
			INSERT INTO users (username, password_hash, display_name, role, is_active)
			VALUES ($1, 'oidc:jumpcloud', $2, $3, true)
			ON CONFLICT (username) DO UPDATE
				SET display_name = EXCLUDED.display_name,
				    role         = EXCLUDED.role,
				    last_login   = NOW()`,
			username, displayName, role,
		)
		if err != nil {
			return nil, err
		}
	} else {
		_, err := d.Exec( //nolint:noctx // pre-existing signature
			` 
			INSERT INTO users (username, password_hash, display_name, role, is_active)
			VALUES ($1, 'oidc:jumpcloud', $2, $3, true)
			ON CONFLICT (username) DO UPDATE
				SET display_name = EXCLUDED.display_name,
				    last_login   = NOW()`,
			username, displayName, role,
		)
		if err != nil {
			return nil, err
		}
	}
	return GetUserByUsername(d, username)
}

// ─── Temporary users ──────────────────────────────────────────────────────────

// TempUserRow — a row representing a temporary user for list views
type TempUserRow struct {
	ID        int
	Username  string
	Role      string
	IsActive  bool
	ExpiresAt *time.Time
	CreatedAt time.Time
	Note      string
}

// CreateTempUser persists a temporary user that expires at expiresAt.
func CreateTempUser(db *sql.DB, username, passwordHash, role string, expiresAt time.Time, note string) (int, error) {
	var id int
	err := db.QueryRow( //nolint:noctx // pre-existing signature
		` 
		INSERT INTO users (username, password_hash, role, is_active, is_temporary, expires_at, temp_note, created_at)
		VALUES ($1, $2, $3, true, true, $4, $5, NOW())
		RETURNING id
	`, username, passwordHash, role, expiresAt, note,
	).Scan(&id)
	return id, err
}

// ListTempUsers returns all temporary users (both active and expired)
func ListTempUsers(db *sql.DB) ([]TempUserRow, error) {
	rows, err := db.Query( //nolint:noctx // pre-existing signature
		` 
		SELECT id, username, role, is_active, expires_at, created_at, COALESCE(temp_note, '')
		FROM users
		WHERE is_temporary = true
		ORDER BY created_at DESC
	`,
	)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []TempUserRow
	for rows.Next() {
		var r TempUserRow
		if err := rows.Scan(&r.ID, &r.Username, &r.Role, &r.IsActive, &r.ExpiresAt, &r.CreatedAt, &r.Note); err != nil {
			continue
		}
		out = append(out, r)
	}
	return out, nil
}

// ExpireOverdueTempUsers sets is_active=false for accounts past their expiry.
func ExpireOverdueTempUsers(db *sql.DB) (int, error) {
	res, err := db.Exec( //nolint:noctx // pre-existing signature; context adoption tracked in P3-8
		`UPDATE users
		SET is_active = false
		WHERE is_temporary = true
		  AND is_active = true
		  AND expires_at IS NOT NULL
		  AND expires_at < NOW()
	`,
	)
	if err != nil {
		return 0, err
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}
