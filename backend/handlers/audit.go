package handlers

import (
	"net/http"
	"strings"

	appdb "step-ui/db"
)

const auditPrefix = "Audit: "

// auditSecurity records an administrative action in the security/auth log.
// It is a no-op when the session carries no authenticated user or the reason
// is empty — keeping the call sites unconditional.
func (h *Handler) auditSecurity(r *http.Request, reason string) {
	si := h.sessionInfo(r)
	if si.UserID == 0 || si.Username == "" || reason == "" {
		return
	}
	_ = appdb.LogAuth(h.db, si.Username, r.RemoteAddr, true, auditPrefix+reason)
}

// securityEventLabel returns a short human-readable label for a security log
// entry based on the success flag and the reason string.
func securityEventLabel(success bool, reason string) string {
	if !success {
		return "Denied"
	}
	switch {
	case strings.HasPrefix(reason, auditPrefix):
		return "Audit"
	case strings.HasPrefix(reason, "2FA"):
		return "2FA"
	case strings.Contains(strings.ToLower(reason), "recovery code"):
		return "2FA"
	case strings.HasPrefix(reason, "Password reset"):
		return "Reset"
	case reason == "Logout":
		return "Logout"
	default:
		return "Login"
	}
}

// securityEventBadge returns the CSS badge class for a security log entry.
func securityEventBadge(success bool, reason string) string {
	if !success {
		return "danger"
	}
	if strings.HasPrefix(reason, auditPrefix) {
		return "warn"
	}
	if strings.HasPrefix(reason, "Password reset") {
		return "warn"
	}
	return "ok"
}
