package db

import (
	"database/sql"
	"time"

	"step-ui/models"
)

// InitPasswordResetSchema creates the password_reset_tokens table and its indexes
// if they do not already exist (idempotent).
func InitPasswordResetSchema(d *sql.DB) error {
	const schema = `
	CREATE TABLE IF NOT EXISTS password_reset_tokens (
		id         SERIAL PRIMARY KEY,
		user_id    INT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
		token_hash TEXT UNIQUE NOT NULL,
		request_ip VARCHAR(80) DEFAULT '',
		expires_at TIMESTAMPTZ NOT NULL,
		used_at    TIMESTAMPTZ,
		created_at TIMESTAMPTZ DEFAULT NOW()
	);
	CREATE INDEX IF NOT EXISTS idx_password_reset_token_hash ON password_reset_tokens(token_hash);
	CREATE INDEX IF NOT EXISTS idx_password_reset_user       ON password_reset_tokens(user_id);
	CREATE INDEX IF NOT EXISTS idx_password_reset_created    ON password_reset_tokens(created_at);
	`
	_, err := d.Exec(schema) //nolint:noctx // schema init runs at startup without a request context
	return err
}

// CreatePasswordResetToken inserts a new hashed reset token for the given user.
func CreatePasswordResetToken(d *sql.DB, userID int, tokenHash, requestIP string, expiresAt time.Time) error {
	_, err := d.Exec( //nolint:noctx // pre-existing signature
		`INSERT INTO password_reset_tokens (user_id,token_hash,request_ip,expires_at)
		VALUES ($1,$2,$3,$4)`,
		userID, tokenHash, requestIP, expiresAt,
	)
	return err
}

// InvalidatePasswordResetTokens marks all unused tokens for a user as used,
// preventing replay after a successful reset or a new request.
func InvalidatePasswordResetTokens(d *sql.DB, userID int) error {
	_, err := d.Exec( //nolint:noctx // pre-existing signature
		`UPDATE password_reset_tokens SET used_at=NOW()
		WHERE user_id=$1 AND used_at IS NULL`,
		userID,
	)
	return err
}

// GetValidPasswordResetToken retrieves an unexpired, unused token by its hash.
// Returns (nil, nil) when no matching token exists.
func GetValidPasswordResetToken(d *sql.DB, tokenHash string) (*models.PasswordResetToken, error) {
	t := &models.PasswordResetToken{}
	var usedAt sql.NullTime
	err := d.QueryRow( //nolint:noctx // pre-existing signature
		`SELECT id,user_id,token_hash,expires_at,used_at,created_at
		FROM password_reset_tokens
		WHERE token_hash=$1 AND used_at IS NULL AND expires_at>NOW()`,
		tokenHash,
	).Scan(&t.ID, &t.UserID, &t.TokenHash, &t.ExpiresAt, &usedAt, &t.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if usedAt.Valid {
		t.UsedAt = &usedAt.Time
	}
	return t, nil
}

// MarkPasswordResetTokenUsed sets used_at=NOW() for a single token, enforcing
// the single-use invariant.
func MarkPasswordResetTokenUsed(d *sql.DB, id int) error {
	_, err := d.Exec( //nolint:noctx // pre-existing signature
		`UPDATE password_reset_tokens SET used_at=NOW()
		WHERE id=$1 AND used_at IS NULL`,
		id,
	)
	return err
}

// GetUserByLoginOrEmail returns the user whose username or email (case-insensitive)
// matches identifier. Returns (nil, nil) when no account exists — callers must not
// distinguish this case from a found user in user-facing responses (no enumeration).
func GetUserByLoginOrEmail(d *sql.DB, identifier string) (*models.User, error) {
	u := &models.User{}
	err := d.QueryRow( //nolint:noctx // pre-existing signature
		`SELECT id,username,password_hash,role,is_active,created_at,last_login,last_ip,
		COALESCE(display_name,''),COALESCE(email,''),COALESCE(theme,'dark'),
		COALESCE(totp_enabled,false),COALESCE(totp_secret,''),COALESCE(totp_pending_secret,'')
		FROM users WHERE username=$1 OR lower(email)=lower($1) LIMIT 1`,
		identifier,
	).Scan(
		&u.ID, &u.Username, &u.PasswordHash, &u.Role, &u.IsActive,
		&u.CreatedAt, &u.LastLogin, &u.LastIP,
		&u.DisplayName, &u.Email, &u.Theme,
		&u.TOTPEnabled, &u.TOTPSecret, &u.TOTPPendingSecret,
	)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return u, err
}
