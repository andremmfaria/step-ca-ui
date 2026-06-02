package handlers

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"image/png"
	"log"
	"net/http"
	"strings"
	"time"

	"step-ui/security"

	"github.com/pquerna/otp/totp"
	appdb "step-ui/db"
)

const (
	totpIssuer     = "Step-CA UI"
	totpPendingTTL = 5 * time.Minute
)

func (h *Handler) Profile2FAGet(w http.ResponseWriter, r *http.Request) {
	si := h.sessionInfo(r)
	u, _ := appdb.GetUserByID(h.db, si.UserID)
	data := h.base(w, r, "profile")
	data["U"] = u
	h.render(w, "profile_2fa", data)
}

func (h *Handler) Profile2FAStart(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/profile/2fa") {
		return
	}
	si := h.sessionInfo(r)
	u, _ := appdb.GetUserByID(h.db, si.UserID)
	if u == nil {
		http.Redirect(w, r, "/profile", http.StatusFound)
		return
	}
	if u.TOTPEnabled {
		h.flash(w, r, "err", "2FA уже включена")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: u.Username,
	})
	if err != nil {
		h.flash(w, r, "err", "Не удалось создать TOTP secret: "+err.Error())
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	if err := appdb.UpdateUserTOTPPending(h.db, u.ID, key.Secret()); err != nil {
		h.flash(w, r, "err", "Не удалось сохранить TOTP secret")
	} else {
		h.flash(w, r, "ok", "Отсканируйте QR-код и подтвердите 6-значным кодом")
	}
	http.Redirect(w, r, "/profile/2fa", http.StatusFound)
}

func (h *Handler) Profile2FAQR(w http.ResponseWriter, r *http.Request) {
	si := h.sessionInfo(r)
	u, _ := appdb.GetUserByID(h.db, si.UserID)
	if u == nil || u.TOTPPendingSecret == "" || u.TOTPEnabled {
		http.NotFound(w, r)
		return
	}
	key, err := totp.Generate(totp.GenerateOpts{
		Issuer:      totpIssuer,
		AccountName: u.Username,
		Secret:      []byte(u.TOTPPendingSecret),
	})
	if err != nil {
		http.NotFound(w, r)
		return
	}
	img, err := key.Image(220, 220)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	_ = png.Encode(w, img)
}

func (h *Handler) Profile2FAConfirm(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/profile/2fa") {
		return
	}
	si := h.sessionInfo(r)
	u, _ := appdb.GetUserByID(h.db, si.UserID)
	if u == nil || u.TOTPPendingSecret == "" || u.TOTPEnabled {
		h.flash(w, r, "err", "Нет активной настройки 2FA")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	code := strings.TrimSpace(r.FormValue("totp_code"))
	// Use the pending secret (not yet promoted to totp_secret); replay
	// protection is not applied here because enabling 2FA is a one-shot
	// confirmation, not a repeated authentication path.
	if !totp.Validate(code, u.TOTPPendingSecret) {
		h.flash(w, r, "err", "Неверный TOTP код")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	recoveryCodes, hashes := generateRecoveryCodes(8)
	if err := appdb.EnableUserTOTP(h.db, u.ID, u.TOTPPendingSecret); err != nil {
		h.flash(w, r, "err", "Не удалось включить 2FA")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	if err := appdb.ReplaceRecoveryCodes(h.db, u.ID, hashes); err != nil {
		h.flash(w, r, "err", "2FA включена, но recovery-коды не сохранены")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	u.TOTPEnabled = true
	u.TOTPSecret = u.TOTPPendingSecret
	u.TOTPPendingSecret = ""
	_ = appdb.LogAuth(h.db, u.Username, r.RemoteAddr, true, "2FA enabled")
	data := h.base(w, r, "profile")
	data["U"] = u
	data["RecoveryCodes"] = recoveryCodes
	h.render(w, "profile_2fa", data)
}

func (h *Handler) Profile2FADisable(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/profile/2fa") {
		return
	}
	si := h.sessionInfo(r)
	u, _ := appdb.GetUserByID(h.db, si.UserID)
	if u == nil || !u.TOTPEnabled {
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	password := r.FormValue("current_password")
	code := strings.TrimSpace(r.FormValue("totp_code"))
	if !security.VerifyPassword(password, u.PasswordHash) {
		h.flash(w, r, "err", "Неверный текущий пароль")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	// Disable is a privileged action: require a fresh (non-replayed) TOTP code.
	if !h.validateTOTPWithReplayCtx(r.Context(), u.ID, u.TOTPSecret, code) {
		h.flash(w, r, "err", "Неверный TOTP код")
		http.Redirect(w, r, "/profile/2fa", http.StatusFound)
		return
	}
	if err := appdb.DisableUserTOTP(h.db, u.ID); err != nil {
		h.flash(w, r, "err", "Не удалось отключить 2FA")
	} else {
		_ = appdb.LogAuth(h.db, u.Username, r.RemoteAddr, true, "2FA disabled")
		h.flash(w, r, "ok", "2FA отключена")
	}
	http.Redirect(w, r, "/profile/2fa", http.StatusFound)
}

func generateRecoveryCodes(n int) ([]string, []string) {
	codes := make([]string, 0, n)
	hashes := make([]string, 0, n)
	for i := 0; i < n; i++ {
		raw := make([]byte, 9)
		_, _ = rand.Read(raw)
		hexed := strings.ToUpper(hex.EncodeToString(raw))
		code := fmt.Sprintf("%s-%s-%s", hexed[:6], hexed[6:12], hexed[12:18])
		codes = append(codes, code)
		hashes = append(hashes, security.HashPassword(code))
	}
	return codes, hashes
}

func (h *Handler) verifyRecoveryCode(userID int, code string) bool {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return false
	}
	hashes, err := appdb.GetUnusedRecoveryCodes(h.db, userID)
	if err != nil {
		return false
	}
	for id, hash := range hashes {
		if security.VerifyPassword(code, hash) {
			_ = appdb.UseRecoveryCode(h.db, id)
			return true
		}
	}
	return false
}

// validateTOTPWithReplayCtx validates the TOTP code and rejects replay attacks
// by tracking the last accepted timestep per user.  The TOTP period is 30 s,
// so the current step is floor(unix / 30).  A code is only accepted when the
// current timestep is strictly greater than the last accepted one, preventing
// the same physical code from being used a second time within its validity
// window.
func (h *Handler) validateTOTPWithReplayCtx(ctx context.Context, userID int, secret, code string) bool {
	if !totp.Validate(code, secret) {
		return false
	}
	currentStep := time.Now().Unix() / 30
	lastStep, err := appdb.GetTOTPLastStep(ctx, h.db, userID)
	if err != nil {
		// Fail open on DB read error — we already validated the code, and
		// blocking valid users on a DB glitch is worse than the narrow replay
		// window left open.  The error is logged for operator visibility.
		log.Printf("[warn] TOTP: GetTOTPLastStep user=%d err=%T", userID, err) //nolint:gosec // G706: userID is int, err type is logged without value
		return true
	}
	if currentStep <= lastStep {
		log.Printf("[warn] TOTP replay rejected for user=%d", userID) //nolint:gosec // G706: only int userID logged
		return false
	}
	if err := appdb.SetTOTPLastStep(ctx, h.db, userID, currentStep); err != nil {
		// Write failure: accept the code but log — the step was valid and
		// the worst-case outcome is a single replay opportunity before the
		// window expires.
		log.Printf("[warn] TOTP: SetTOTPLastStep user=%d err=%T", userID, err) //nolint:gosec // G706: only int userID and error type logged
	}
	return true
}
