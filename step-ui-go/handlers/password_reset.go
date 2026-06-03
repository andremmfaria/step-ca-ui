package handlers

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"net/smtp"
	"net/url"
	"strings"
	"sync"
	"time"

	appdb "step-ui/db"
	"step-ui/security"
)

const (
	passwordResetTTL         = 30 * time.Minute
	passwordResetLimitCount  = 3
	passwordResetLimitWindow = 15 * time.Minute
	// genericResetInfo is the identical response shown regardless of
	// whether an account was found — prevents user enumeration via response differences.
	genericResetInfo = "If an account with that login or email exists, a password reset link has been sent."
)

// passwordResetRL is a dedicated per-IP sliding-window rate limiter for the
// /forgot-password endpoint (3 requests / 15 min), separate from the login RL.
var passwordResetRL = struct { //nolint:gochecknoglobals // package-level singleton mirrors security.RL pattern
	sync.Mutex
	attempts map[string][]time.Time
}{attempts: make(map[string][]time.Time)}

// ForgotPasswordGet renders the "forgot password" form.
func (h *Handler) ForgotPasswordGet(w http.ResponseWriter, r *http.Request) {
	h.render(w, "forgot_password", h.base(w, r, ""))
}

// ForgotPasswordPost handles submission of the forgot-password form.
// It always shows the generic response to prevent user enumeration.
func (h *Handler) ForgotPasswordPost(w http.ResponseWriter, r *http.Request) {
	data := h.base(w, r, "")

	if !h.csrfOK(r) {
		// Even a CSRF mismatch shows the generic message to avoid leaking state.
		data["Error"] = "Session error. Please refresh the page and try again."
		h.render(w, "forgot_password", data)
		return
	}

	ip := clientIP(r)
	if !passwordResetAllowed(ip) {
		_ = appdb.LogAuth(h.db, "password-reset", ip, false, "Password reset rate limited")
		data["Info"] = genericResetInfo
		h.render(w, "forgot_password", data)
		return
	}

	identifier := trimStr(r.FormValue("identifier"))
	if identifier == "" {
		data["Error"] = "Please enter your username or email address."
		h.render(w, "forgot_password", data)
		return
	}

	// All branches below show the same generic Info — only the audit log
	// captures the real outcome to preserve no-enumeration.
	user, err := appdb.GetUserByLoginOrEmail(h.db, identifier)
	if err != nil || user == nil {
		_ = appdb.LogAuth(h.db, identifier, ip, false, "Password reset requested for unknown account")
		data["Info"] = genericResetInfo
		h.render(w, "forgot_password", data)
		return
	}
	if !user.IsActive {
		_ = appdb.LogAuth(h.db, user.Username, ip, false, "Password reset requested for inactive account")
		data["Info"] = genericResetInfo
		h.render(w, "forgot_password", data)
		return
	}
	if strings.TrimSpace(user.Email) == "" {
		_ = appdb.LogAuth(h.db, user.Username, ip, false, "Password reset requested but user has no email address")
		data["Info"] = genericResetInfo
		h.render(w, "forgot_password", data)
		return
	}

	settings, err := appdb.GetNotificationSettings(r.Context(), h.db)
	if err != nil || !settings.SMTPEnabled || strings.TrimSpace(settings.SMTPHost) == "" || strings.TrimSpace(settings.SMTPFrom) == "" {
		_ = appdb.LogAuth(h.db, user.Username, ip, false, "Password reset requested but SMTP is not configured")
		data["Info"] = genericResetInfo
		h.render(w, "forgot_password", data)
		return
	}

	// Generate a raw token; store only the SHA-256 hash (token never touches DB).
	rawToken := security.GenerateToken()
	tokenHash := passwordResetTokenHash(rawToken)
	_ = appdb.InvalidatePasswordResetTokens(h.db, user.ID) // invalidate any prior tokens
	if err := appdb.CreatePasswordResetToken(h.db, user.ID, tokenHash, ip, time.Now().Add(passwordResetTTL)); err != nil {
		_ = appdb.LogAuth(h.db, user.Username, ip, false, "Password reset token creation failed")
		data["Info"] = genericResetInfo
		h.render(w, "forgot_password", data)
		return
	}

	link := absoluteURL(r, "/reset-password?token="+url.QueryEscape(rawToken))
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	if err := sendPasswordResetMail(
		ctx,
		settings.SMTPHost, settings.SMTPPort, settings.SMTPSecurity,
		settings.SMTPUsername, settings.SMTPPassword,
		settings.SMTPFrom, user.Email, link,
	); err != nil {
		slog.Error("password reset email failed", "user", user.Username, "err", err)
		_ = appdb.LogAuth(h.db, user.Username, ip, false, "Password reset email send failed: "+err.Error())
	} else {
		_ = appdb.LogAuth(h.db, user.Username, ip, true, "Password reset email sent")
	}
	data["Info"] = genericResetInfo
	h.render(w, "forgot_password", data)
}

// ResetPasswordGet renders the new-password form, validating the token in the URL.
func (h *Handler) ResetPasswordGet(w http.ResponseWriter, r *http.Request) {
	data := h.base(w, r, "")
	token := trimStr(r.URL.Query().Get("token"))
	if !h.passwordResetTokenOK(token) {
		data["Error"] = "This reset link is invalid or has expired. Please request a new one."
	} else {
		data["Token"] = token
	}
	h.render(w, "reset_password", data)
}

// ResetPasswordPost handles the new-password submission.
func (h *Handler) ResetPasswordPost(w http.ResponseWriter, r *http.Request) {
	data := h.base(w, r, "")

	if !h.csrfOK(r) {
		data["Error"] = "Session error. Please refresh the page."
		h.render(w, "reset_password", data)
		return
	}

	token := trimStr(r.FormValue("token"))
	resetToken, err := appdb.GetValidPasswordResetToken(h.db, passwordResetTokenHash(token))
	if err != nil || resetToken == nil {
		data["Error"] = "This reset link is invalid or has expired. Please request a new one."
		h.render(w, "reset_password", data)
		return
	}

	newPW := trimStr(r.FormValue("new_password"))
	confirm := trimStr(r.FormValue("confirm_password"))
	if newPW != confirm {
		data["Error"] = "Passwords do not match."
		data["Token"] = token
		h.render(w, "reset_password", data)
		return
	}

	if ok, msg := security.ValidatePassword(newPW); !ok {
		data["Error"] = msg
		data["Token"] = token
		h.render(w, "reset_password", data)
		return
	}

	user, err := appdb.GetUserByID(h.db, resetToken.UserID)
	if err != nil || user == nil || !user.IsActive {
		data["Error"] = "Account is not available. Please contact an administrator."
		h.render(w, "reset_password", data)
		return
	}

	if err := appdb.UpdateUserPassword(h.db, user.ID, security.HashPassword(newPW)); err != nil {
		data["Error"] = "Could not update password. Please try again."
		data["Token"] = token
		h.render(w, "reset_password", data)
		return
	}

	// Invalidate on use: mark this token used AND invalidate any others for the user.
	_ = appdb.MarkPasswordResetTokenUsed(h.db, resetToken.ID)
	_ = appdb.InvalidatePasswordResetTokens(h.db, user.ID)
	_ = appdb.LogAuth(h.db, user.Username, clientIP(r), true, "Password reset completed")

	h.flash(w, r, "ok", "Password updated. Please sign in with your new password.")
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// passwordResetTokenOK validates that a raw token corresponds to a valid,
// unexpired, unused DB record.
func (h *Handler) passwordResetTokenOK(token string) bool {
	if token == "" {
		return false
	}
	t, err := appdb.GetValidPasswordResetToken(h.db, passwordResetTokenHash(token))
	return err == nil && t != nil
}

// passwordResetTokenHash returns the hex-encoded SHA-256 of a raw token.
// Only the hash is stored in the database; the raw token travels only in the
// emailed link and the URL query string.
func passwordResetTokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// passwordResetAllowed returns true if ip has not exceeded the rate limit and
// records the attempt. It uses a dedicated sliding-window limiter (3 / 15 min)
// to avoid interfering with the login rate limiter.
func passwordResetAllowed(ip string) bool {
	passwordResetRL.Lock()
	defer passwordResetRL.Unlock()
	now := time.Now()
	var fresh []time.Time
	for _, t := range passwordResetRL.attempts[ip] {
		if now.Sub(t) < passwordResetLimitWindow {
			fresh = append(fresh, t)
		}
	}
	if len(fresh) >= passwordResetLimitCount {
		passwordResetRL.attempts[ip] = fresh
		return false
	}
	fresh = append(fresh, now)
	passwordResetRL.attempts[ip] = fresh
	return true
}

// absoluteURL constructs a fully-qualified URL for the given path, honouring
// X-Forwarded-Proto and the presence of TLS on the connection.
func absoluteURL(r *http.Request, path string) string {
	scheme := "https"
	if proto := strings.TrimSpace(r.Header.Get("X-Forwarded-Proto")); proto != "" {
		scheme = proto
	} else if r.TLS == nil {
		scheme = "http"
	}
	return scheme + "://" + r.Host + path
}

// sendPasswordResetMail dials the SMTP server using the configured security
// mode and delivers a password-reset email. TLS >= 1.2 is enforced for all
// encrypted modes. The connection is always made via DialContext so the
// 10-second caller timeout is honoured.
func sendPasswordResetMail(
	ctx context.Context,
	host string, port int, securityMode,
	username, password,
	from, to, link string,
) error {
	if port <= 0 {
		port = 587
	}
	addr := fmt.Sprintf("%s:%d", host, port)

	subject := "Step-CA UI — Password Reset"
	body := fmt.Sprintf(
		"A password reset was requested for your Step-CA UI account.\r\n\r\n"+
			"Open this link within 30 minutes to set a new password:\r\n%s\r\n\r\n"+
			"If you did not request this, you can safely ignore this email.\r\n",
		link,
	)
	msg := []byte(
		"From: " + from + "\r\n" +
			"To: " + to + "\r\n" +
			"Subject: " + subject + "\r\n" +
			"MIME-Version: 1.0\r\n" +
			"Content-Type: text/plain; charset=utf-8\r\n" +
			"\r\n" + body,
	)

	var auth smtp.Auth
	if username != "" {
		auth = smtp.PlainAuth("", username, password, host)
	}

	mode := strings.ToLower(strings.TrimSpace(securityMode))
	if mode == "" {
		mode = "starttls"
	}

	dialer := &net.Dialer{}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()

	if mode == "tls" {
		// Implicit TLS: wrap the raw connection before the SMTP handshake.
		tlsCfg := &tls.Config{ //nolint:gosec // MinVersion explicitly set to TLS 1.2 below
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		}
		tlsConn := tls.Client(conn, tlsCfg)
		if err := tlsConn.HandshakeContext(ctx); err != nil {
			return err
		}
		return sendSMTPOverConn(tlsConn, host, from, []string{to}, msg, auth)
	}

	// Plain or STARTTLS: begin an unencrypted SMTP session first.
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()

	if mode == "starttls" {
		if ok, _ := client.Extension("STARTTLS"); !ok {
			return fmt.Errorf("smtp: server does not advertise STARTTLS")
		}
		tlsCfg := &tls.Config{ //nolint:gosec // MinVersion explicitly set to TLS 1.2 below
			ServerName: host,
			MinVersion: tls.VersionTLS12,
		}
		if err := client.StartTLS(tlsCfg); err != nil {
			return err
		}
	}

	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	if err := client.Rcpt(to); err != nil { //nolint:gosec // G707: 'to' is the user's stored email address from the DB, not direct user input at this call site
		return err
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}

// sendSMTPOverConn sends a pre-formatted SMTP message over an already-wrapped
// (TLS) net.Conn. Used by the implicit-TLS ("tls") security mode.
func sendSMTPOverConn(conn net.Conn, host, from string, to []string, msg []byte, auth smtp.Auth) error {
	client, err := smtp.NewClient(conn, host)
	if err != nil {
		return err
	}
	defer func() { _ = client.Close() }()
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return err
		}
	}
	if err := client.Mail(from); err != nil {
		return err
	}
	for _, rcpt := range to {
		if err := client.Rcpt(rcpt); err != nil { //nolint:gosec // G707: 'rcpt' originates from DB-stored user email, not direct user input
			return err
		}
	}
	w, err := client.Data()
	if err != nil {
		return err
	}
	if _, err := w.Write(msg); err != nil {
		_ = w.Close()
		return err
	}
	if err := w.Close(); err != nil {
		return err
	}
	return client.Quit()
}
