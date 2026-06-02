package handlers

import (
	"fmt"
	"net"
	"net/http"
	"strconv"
	"time"

	"step-ui/models"
	"step-ui/security"

	appdb "step-ui/db"
)

// clientIP returns the host portion of r.RemoteAddr, stripping the ephemeral
// port so that all connections from the same client count under one rate-limit
// key regardless of TCP connection cycling.
// When TrustProxy=true the chi RealIP middleware has already normalised
// RemoteAddr to a bare IP, so SplitHostPort returns an error and we fall back
// to the raw value — both cases produce the correct host-only string.
func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// LoginGet renders the login page, showing a blocked notice if the IP is rate-limited.
func (h *Handler) LoginGet(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)
	data := h.base(w, r, "")
	if h.pending2FAUserID(r) > 0 {
		data["NeedTOTP"] = true
	}
	if security.RL.IsBlocked(ip) {
		data["Error"] = "Too many attempts. Please wait 15 minutes."
		data["Blocked"] = true
	}
	data["OIDCEnabled"] = h.cfg.OIDCEnabled
	data["LocalLoginEnabled"] = h.cfg.LocalLoginEnabled
	h.render(w, "login", data)
}

// LoginPost handles credential submission and initiates the login flow.
func (h *Handler) LoginPost(w http.ResponseWriter, r *http.Request) {
	ip := clientIP(r)

	if !h.cfg.LocalLoginEnabled {
		if h.cfg.OIDCEnabled {
			http.Redirect(w, r, "/auth/oidc/login", http.StatusFound)
		} else {
			data := h.base(w, r, "")
			data["Error"] = "Local login is disabled."
			data["OIDCEnabled"] = false
			data["LocalLoginEnabled"] = false
			h.render(w, "login", data)
		}
		return
	}

	if security.RL.IsBlocked(ip) {
		data := h.base(w, r, "")
		data["Error"] = "Too many attempts. Please wait 15 minutes."
		data["Blocked"] = true
		h.render(w, "login", data)
		return
	}

	if !h.csrfOK(r) {
		data := h.base(w, r, "")
		data["Error"] = "Session error. Please refresh the page."
		h.render(w, "login", data)
		return
	}

	if uid := h.pending2FAUserID(r); uid > 0 {
		h.loginPost2FA(w, r, uid)
		return
	}

	username := trimStr(r.FormValue("username"))
	password := r.FormValue("password")

	user, _ := appdb.GetUserByUsername(h.db, username)
	if user == nil || !security.VerifyPassword(password, user.PasswordHash) {
		security.RL.Register(ip)
		left := security.RL.Left(ip)
		_ = appdb.LogAuth(h.db, username, ip, false, fmt.Sprintf("Wrong password (%d attempts remaining)", left))
		if left > 0 {
			h.flash(w, r, "err", fmt.Sprintf("Invalid username or password. Attempts remaining: %d", left))
		} else {
			h.notifyAsync("auth-burst:"+ip+":"+time.Now().Format("2006-01-02T15:04"), "auth.failed_burst", "warn",
				"Failed login burst",
				fmt.Sprintf("IP %s blocked after repeated failed login attempts", ip),
				map[string]string{"username": username, "ip": ip})
			h.flash(w, r, "err", "Too many attempts. Please wait 15 minutes.")
		}
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if !user.IsActive {
		_ = appdb.LogAuth(h.db, username, ip, false, "Account is disabled")
		h.flash(w, r, "err", "Account is disabled. Please contact your administrator.")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	if security.NeedsPasswordRehash(user.PasswordHash) {
		_ = appdb.UpdateUserPassword(h.db, user.ID, security.HashPassword(password))
	}

	if user.TOTPEnabled {
		s := h.sess(r)
		s.Values["pending_2fa_user_id"] = user.ID
		s.Values["pending_2fa_expires"] = time.Now().Add(totpPendingTTL).Unix()
		_ = s.Save(r, w)
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}

	h.completeLogin(w, r, user, "")
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) loginPost2FA(w http.ResponseWriter, r *http.Request, uid int) {
	ip := clientIP(r)
	user, _ := appdb.GetUserByID(h.db, uid)
	if user == nil || !user.IsActive || !user.TOTPEnabled {
		h.clearPending2FA(w, r)
		h.flash(w, r, "err", "2FA session is invalid")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	code := r.FormValue("totp_code")
	recovery := r.FormValue("recovery_code")
	ok := false
	recoveryUsed := false

	if code != "" {
		ok = h.validateTOTPWithReplayCtx(r.Context(), user.ID, user.TOTPSecret, code)
	}
	if !ok && recovery != "" {
		ok = h.verifyRecoveryCode(user.ID, recovery)
		recoveryUsed = ok
	}
	if !ok {
		security.RL.Register(ip)
		left := security.RL.Left(ip)
		_ = appdb.LogAuth(h.db, user.Username, ip, false, fmt.Sprintf("Wrong 2FA code (%d attempts remaining)", left))
		h.flash(w, r, "err", "Invalid 2FA or recovery code")
		http.Redirect(w, r, "/login", http.StatusFound)
		return
	}
	reason := ""
	if recoveryUsed {
		reason = "Login with recovery code"
	}
	h.completeLogin(w, r, user, reason)
	http.Redirect(w, r, "/", http.StatusFound)
}

func (h *Handler) pending2FAUserID(r *http.Request) int {
	s := h.sess(r)
	rawID, ok := s.Values["pending_2fa_user_id"].(int)
	if !ok || rawID <= 0 {
		return 0
	}
	exp, _ := s.Values["pending_2fa_expires"].(int64)
	if exp == 0 {
		if expStr, ok := s.Values["pending_2fa_expires"].(string); ok {
			exp, _ = strconv.ParseInt(expStr, 10, 64)
		}
	}
	if exp > 0 && time.Now().Unix() > exp {
		return 0
	}
	return rawID
}

func (h *Handler) clearPending2FA(w http.ResponseWriter, r *http.Request) {
	s := h.sess(r)
	delete(s.Values, "pending_2fa_user_id")
	delete(s.Values, "pending_2fa_expires")
	_ = s.Save(r, w)
}

func (h *Handler) completeLogin(w http.ResponseWriter, r *http.Request, user *models.User, reason string) {
	security.RL.Clear(clientIP(r))
	s := h.sess(r)
	s.Values = map[interface{}]interface{}{}
	s.Values["user_id"] = user.ID
	s.Values["username"] = user.Username
	s.Values["role"] = user.Role
	s.Values["last_activity"] = time.Now().Unix()
	s.Values["csrf_token"] = security.GenerateToken()
	_ = s.Save(r, w)
	_ = appdb.LogAuth(h.db, user.Username, r.RemoteAddr, true, reason)
}

// Logout clears the session and redirects to the login page.
func (h *Handler) Logout(w http.ResponseWriter, r *http.Request) {
	si := h.sessionInfo(r)
	if si.UserID != 0 {
		_ = appdb.LogAuth(h.db, si.Username, r.RemoteAddr, true, "Logout")
	}
	s := h.sess(r)
	s.Values = map[interface{}]interface{}{}
	s.Options.MaxAge = -1
	_ = s.Save(r, w)
	http.Redirect(w, r, "/login", http.StatusFound)
}
