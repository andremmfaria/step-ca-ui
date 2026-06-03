package handlers

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"step-ui/security"

	"github.com/go-chi/chi/v5"
	appdb "step-ui/db"
)

// Users renders the admin user management list page.
func (h *Handler) Users(w http.ResponseWriter, r *http.Request) {
	users, _ := appdb.GetAllUsers(h.db)
	since := time.Now().Add(-24 * time.Hour)
	failCounts := map[int]int{}
	for _, u := range users {
		failCounts[u.ID] = appdb.GetFailCount(h.db, u.Username, since)
	}
	data := h.base(w, r, "admin_users")
	data["Users"] = users
	data["FailCounts"] = failCounts
	h.render(w, "admin_users", data)
}

// UsersPost handles user creation, role update, block/unblock, and password reset actions.
func (h *Handler) UsersPost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/admin/users") {
		return
	}
	si := h.sessionInfo(r)
	action := r.FormValue("action")
	switch action {
	case "create":
		username := trimStr(r.FormValue("username"))
		password := trimStr(r.FormValue("password"))
		role := r.FormValue("role")
		if username == "" || password == "" {
			h.flash(w, r, "err", "Please fill in all fields")
			break
		}
		if ok, msg := security.ValidatePassword(password); !ok {
			h.flash(w, r, "err", msg)
			break
		}
		if err := appdb.CreateUser(h.db, username, security.HashPassword(password), role); err != nil {
			h.flash(w, r, "err", "User already exists")
		} else {
			h.auditSecurity(r, fmt.Sprintf("user.create target=%s role=%s", username, role))
			h.flash(w, r, "ok", "User "+username+" created")
		}

	case "delete":
		uid, _ := strconv.Atoi(r.FormValue("uid"))
		if uid == si.UserID {
			h.flash(w, r, "err", "You cannot delete your own account")
			break
		}
		target, _ := appdb.GetUserByID(h.db, uid)
		if err := appdb.DeleteUser(h.db, uid); err != nil {
			h.flash(w, r, "err", "Delete error: "+err.Error())
			break
		}
		if target != nil {
			h.auditSecurity(r, fmt.Sprintf("user.delete target=%s uid=%d", target.Username, uid))
		} else {
			h.auditSecurity(r, fmt.Sprintf("user.delete uid=%d", uid))
		}
		h.flash(w, r, "ok", "User deleted")

	case "change_role":
		uid, _ := strconv.Atoi(r.FormValue("uid"))
		role := r.FormValue("role")
		if uid == si.UserID {
			h.flash(w, r, "err", "You cannot change your own role")
			break
		}
		if role == "viewer" || role == "manager" || role == "admin" {
			_ = appdb.UpdateUserRole(h.db, uid, role)
			roleTarget, _ := appdb.GetUserByID(h.db, uid)
			if roleTarget != nil {
				h.auditSecurity(r, fmt.Sprintf("user.change_role target=%s uid=%d role=%s", roleTarget.Username, uid, role))
			} else {
				h.auditSecurity(r, fmt.Sprintf("user.change_role uid=%d role=%s", uid, role))
			}
			h.flash(w, r, "ok", "Role updated")
		}

	case "toggle_active":
		uid, _ := strconv.Atoi(r.FormValue("uid"))
		if uid == si.UserID {
			h.flash(w, r, "err", "You cannot deactivate your own account")
			break
		}
		u, _ := appdb.GetUserByID(h.db, uid)
		if u != nil {
			newState := !u.IsActive
			_ = appdb.UpdateUserActive(h.db, uid, newState)
			if newState {
				h.auditSecurity(r, fmt.Sprintf("user.activate target=%s uid=%d", u.Username, uid))
				h.flash(w, r, "ok", "User activated")
			} else {
				h.auditSecurity(r, fmt.Sprintf("user.deactivate target=%s uid=%d", u.Username, uid))
				h.flash(w, r, "ok", "User deactivated")
			}
		}

	case "unblock_ip":
		ip := r.FormValue("target_ip")
		if ip != "" {
			security.RL.Clear(ip)
			h.auditSecurity(r, fmt.Sprintf("ip.unblock target=%s", ip))
			h.flash(w, r, "ok", fmt.Sprintf("IP %s unblocked", ip))
		}

	case "reset_password":
		uid, _ := strconv.Atoi(r.FormValue("uid"))
		newPW := trimStr(r.FormValue("new_password"))
		if ok, msg := security.ValidatePassword(newPW); !ok {
			h.flash(w, r, "err", msg)
			break
		}
		_ = appdb.UpdateUserPassword(h.db, uid, security.HashPassword(newPW))
		pwTarget, _ := appdb.GetUserByID(h.db, uid)
		if pwTarget != nil {
			h.auditSecurity(r, fmt.Sprintf("user.reset_password target=%s uid=%d", pwTarget.Username, uid))
		} else {
			h.auditSecurity(r, fmt.Sprintf("user.reset_password uid=%d", uid))
		}
		h.flash(w, r, "ok", "Password reset")
	}
	returnTo := r.FormValue("return_to")
	// Restrict to relative paths to prevent open-redirect via user-supplied return_to.
	if returnTo == "" || !strings.HasPrefix(returnTo, "/") || strings.HasPrefix(returnTo, "//") {
		returnTo = "/admin/users"
	}
	//nolint:gosec // G710: returnTo is validated above to be a relative path (starts with / but not //)
	http.Redirect(w, r, returnTo, http.StatusFound)
}

// UserProfile renders the admin view of a specific user's profile and auth log.
func (h *Handler) UserProfile(w http.ResponseWriter, r *http.Request) {
	uid, _ := strconv.Atoi(chi.URLParam(r, "id"))
	u, _ := appdb.GetUserByID(h.db, uid)
	if u == nil {
		http.Redirect(w, r, "/admin/users", http.StatusFound)
		return
	}
	logs, _ := appdb.GetUserAuthLogs(h.db, u.Username, 50)
	ok, _ := appdb.GetAuthStats(h.db)
	totalOK := 0
	totalFail := 0
	for _, l := range logs {
		if l.Success {
			totalOK++
		} else {
			totalFail++
		}
	}
	_ = ok
	ipBlocked := false
	if u.LastIP != nil && *u.LastIP != "" {
		ipBlocked = security.RL.IsBlocked(*u.LastIP)
	}
	data := h.base(w, r, "admin_users")
	data["U"] = u
	data["Logs"] = logs
	data["TotalOK"] = totalOK
	data["TotalFail"] = totalFail
	data["IPBlocked"] = ipBlocked
	h.render(w, "admin_user_profile", data)
}

// ProfileGet renders the current user's own profile page.
func (h *Handler) ProfileGet(w http.ResponseWriter, r *http.Request) {
	si := h.sessionInfo(r)
	u, _ := appdb.GetUserByID(h.db, si.UserID)
	data := h.base(w, r, "profile")
	data["U"] = u
	h.render(w, "profile", data)
}

// ProfilePost handles the current user's profile update (theme, display name, password).
func (h *Handler) ProfilePost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/profile") {
		return
	}
	si := h.sessionInfo(r)
	action := r.FormValue("action")

	switch action {
	case "theme":
		theme := trimStr(r.FormValue("theme"))
		valid := map[string]bool{"dark": true, "light": true, "blue": true, "auto": true}
		if !valid[theme] {
			theme = "dark"
		}
		if err := appdb.UpdateUserTheme(h.db, si.UserID, theme); err != nil {
			h.flash(w, r, "err", "Failed to save theme")
		} else {
			h.flash(w, r, "ok", "Theme updated")
		}
		http.Redirect(w, r, "/profile", http.StatusFound)
		return

	case "update_info":
		username := trimStr(r.FormValue("username"))
		displayName := trimStr(r.FormValue("display_name"))
		email := trimStr(r.FormValue("email"))
		if username == "" {
			h.flash(w, r, "err", "Username cannot be empty")
			http.Redirect(w, r, "/profile", http.StatusFound)
			return
		}
		// Check that the username is not taken by another user
		exists, _ := appdb.UsernameExistsExceptID(h.db, username, si.UserID)
		if exists {
			h.flash(w, r, "err", "A user with that username already exists")
			http.Redirect(w, r, "/profile", http.StatusFound)
			return
		}
		if err := appdb.UpdateUserInfo(h.db, si.UserID, username, displayName, email); err != nil {
			h.flash(w, r, "err", "Update error: "+err.Error())
			http.Redirect(w, r, "/profile", http.StatusFound)
			return
		}
		// Update username in session
		s := h.sess(r)
		s.Values["username"] = username
		_ = s.Save(r, w)
		h.flash(w, r, "ok", "Profile updated")
		http.Redirect(w, r, "/profile", http.StatusFound)
		return

	case "change_password", "":
		current := r.FormValue("current_password")
		newPW := trimStr(r.FormValue("new_password"))
		confirm := trimStr(r.FormValue("confirm_password"))

		u, _ := appdb.GetUserByID(h.db, si.UserID)
		if u == nil || !security.VerifyPassword(current, u.PasswordHash) {
			h.flash(w, r, "err", "Current password is incorrect")
			http.Redirect(w, r, "/profile", http.StatusFound)
			return
		}
		if newPW != confirm {
			h.flash(w, r, "err", "Passwords do not match")
			http.Redirect(w, r, "/profile", http.StatusFound)
			return
		}
		if ok, msg := security.ValidatePassword(newPW); !ok {
			h.flash(w, r, "err", msg)
			http.Redirect(w, r, "/profile", http.StatusFound)
			return
		}
		_ = appdb.UpdateUserPassword(h.db, si.UserID, security.HashPassword(newPW))
		h.flash(w, r, "ok", "Password changed successfully")
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}

	http.Redirect(w, r, "/profile", http.StatusFound)
}
