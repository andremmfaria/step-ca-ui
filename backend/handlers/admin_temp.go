package handlers

import (
	"crypto/rand"
	"fmt"
	"math/big"
	"net/http"
	"strconv"
	"strings"
	"time"

	"step-ui/security"

	appdb "step-ui/db"
)

// AdminUsersTempGet — temporary user list page.
func (h *Handler) AdminUsersTempGet(w http.ResponseWriter, r *http.Request) {
	data := h.adminUsersTempData(w, r)
	// take deletes on read, so a refresh of this URL shows nothing (V7).
	if cred, ok := tempCreds.take(r.URL.Query().Get("cred")); ok {
		data["NewUsername"] = cred.Username
		data["NewPassword"] = cred.Password
	}
	h.render(w, "admin_users_temp", data)
}

// adminUsersTempData builds the page's view model.
func (h *Handler) adminUsersTempData(w http.ResponseWriter, r *http.Request) map[string]interface{} {
	users, _ := appdb.ListTempUsers(h.db)

	// Build view-model: pre-computed status and formatted dates
	type tempUserVM struct {
		ID        int
		Username  string
		Role      string
		Note      string
		CreatedAt string
		ExpiresAt string
		Status    string // "active" | "expired" | "blocked"
	}
	now := time.Now()
	var vms []tempUserVM
	for _, u := range users {
		vm := tempUserVM{
			ID:        u.ID,
			Username:  u.Username,
			Role:      u.Role,
			Note:      u.Note,
			CreatedAt: u.CreatedAt.Local().Format("2006-01-02 15:04"),
		}
		if u.ExpiresAt != nil {
			vm.ExpiresAt = u.ExpiresAt.Local().Format("2006-01-02 15:04")
		} else {
			vm.ExpiresAt = ""
		}
		switch {
		case u.IsActive:
			vm.Status = "active"
		case u.ExpiresAt != nil && now.After(*u.ExpiresAt):
			vm.Status = "expired"
		default:
			vm.Status = "blocked"
		}
		vms = append(vms, vm)
	}
	data := h.base(w, r, "admin_users_temp")
	scheme := "https"
	if r.TLS == nil && r.Header.Get("X-Forwarded-Proto") != "https" {
		scheme = "http"
		if p := r.Header.Get("X-Forwarded-Proto"); p != "" {
			scheme = p
		}
	}
	data["LoginURL"] = scheme + "://" + r.Host + "/login"
	data["Users"] = vms
	data["Now"] = time.Now()
	return data
}

// AdminUsersTempPost — creates a temporary user.
func (h *Handler) AdminUsersTempPost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/admin/users-temp") {
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, "bad form", http.StatusBadRequest)
		return
	}
	role := r.FormValue("role")
	if !appdb.ValidRole(role) {
		role = "viewer"
	}
	note := r.FormValue("note")

	// Expiry: either custom_datetime (format "2006-01-02 15:04")
	// or a preset ("30m"|"1h"|"4h"|"24h"|"7d"|"30d").
	var expiresAt time.Time
	if custom := strings.TrimSpace(r.FormValue("custom_datetime")); custom != "" {
		if t, err := time.ParseInLocation("2006-01-02 15:04", custom, time.Local); err == nil {
			expiresAt = t
		} else {
			h.flash(w, r, "err", "Invalid date/time format")
			http.Redirect(w, r, "/admin/users-temp", http.StatusSeeOther)
			return
		}
	}
	if expiresAt.IsZero() {
		preset := r.FormValue("preset")
		if preset == "" {
			// Backward compatibility with the old form
			if hrs, _ := strconv.Atoi(r.FormValue("preset_hours")); hrs > 0 {
				preset = fmt.Sprintf("%dh", hrs)
			}
		}
		dur := presetToDuration(preset)
		if dur <= 0 {
			dur = 24 * time.Hour
		}
		expiresAt = time.Now().Add(dur)
	}

	if !expiresAt.After(time.Now().Add(1 * time.Minute)) {
		h.flash(w, r, "err", "Expiry must be in the future (at least one minute from now)")
		http.Redirect(w, r, "/admin/users-temp", http.StatusSeeOther)
		return
	}

	// Generate username and password
	username := generateTempUsername()
	password := generateTempPassword(16)

	hash := security.HashPassword(password)
	if _, err := appdb.CreateTempUser(h.db, username, hash, role, expiresAt, note); err != nil {
		h.flash(w, r, "err", "Failed to create user: "+err.Error())
		http.Redirect(w, r, "/admin/users-temp", http.StatusSeeOther)
		return
	}

	// Post/redirect/get, with the credential handed over by an unguessable
	// single-use token rather than in a cookie: refreshing the result page must
	// not create a second account (V7).
	h.flash(w, r, "ok", "Temporary user created")
	//nolint:gosec // G710: the token is server-minted by security.GenerateToken, not caller text
	http.Redirect(w, r, "/admin/users-temp?cred="+tempCreds.put(username, password), http.StatusSeeOther)
}

// generateTempUsername → "guest-ab12cd"
func generateTempUsername() string {
	const alphabet = "abcdefghijkmnopqrstuvwxyz23456789" // excluding 0,1,l,o
	b := make([]byte, 6)
	for i := range b {
		n, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[n.Int64()]
	}
	return "guest-" + string(b)
}

// generateTempPassword — cryptographically secure password of length n, excluding ambiguous characters
func generateTempPassword(n int) string {
	const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghjkmnpqrstuvwxyz23456789!@#$%&*+-=?"
	b := make([]byte, n)
	for i := range b {
		idx, _ := rand.Int(rand.Reader, big.NewInt(int64(len(alphabet))))
		b[i] = alphabet[idx.Int64()]
	}
	return string(b)
}

// presetToDuration maps a preset string to a Duration.
// Supports: 30m, 1h, 4h, 24h, 7d, 30d
func presetToDuration(p string) time.Duration {
	switch p {
	case "30m":
		return 30 * time.Minute
	case "1h":
		return 1 * time.Hour
	case "4h":
		return 4 * time.Hour
	case "24h":
		return 24 * time.Hour
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	default:
		return 0
	}
}
