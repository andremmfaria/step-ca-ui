package handlers

import (
	"context"
	"fmt"
	"net/http"

	"step-ui/le"
	"step-ui/models"

	appdb "step-ui/db"
)

// ─── Dashboard ────────────────────────────────────────────────────────────────

// LEDashboard renders the ACME/Let's Encrypt certificate management dashboard.
func (h *Handler) LEDashboard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	certs, _ := appdb.GetLECerts(ctx, h.db)
	total, active, expiringSoon, expired := appdb.GetLEStats(ctx, h.db)
	logs, _ := appdb.GetLELogs(ctx, h.db, "", 20)
	settings, _ := appdb.GetLESettings(ctx, h.db)
	data := h.base(w, r, "le")
	data["LECerts"] = certs
	data["LETotal"] = total
	data["LEActive"] = active
	data["LEExpiring"] = expiringSoon
	data["LEExpired"] = expired
	data["LELogs"] = logs
	data["LESettings"] = settings
	h.render(w, "le_dashboard", data)
}

// ─── Issue ────────────────────────────────────────────────────────────────────

// LEIssueGet renders the ACME certificate issuance form.
func (h *Handler) LEIssueGet(w http.ResponseWriter, r *http.Request) {
	settings, _ := appdb.GetLESettings(r.Context(), h.db)
	data := h.base(w, r, "le-issue")
	data["LESettings"] = settings
	h.render(w, "le_issue", data)
}

// LEIssuePost handles ACME certificate issuance form submission.
func (h *Handler) LEIssuePost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/le/issue") {
		return
	}
	domain := trimStr(r.FormValue("domain"))
	email := trimStr(r.FormValue("email"))
	provider := r.FormValue("provider")
	autoRenew := r.FormValue("auto_renew") == "on"

	if domain == "" || email == "" {
		h.flash(w, r, "err", "Please enter the domain and email")
		http.Redirect(w, r, "/le/issue", http.StatusFound)
		return
	}

	// A different issuer, but the same question of authority over the name (V6).
	if err := checkDomainPolicy(domain, h.cfg.AllowedDomainSuffixes); err != nil {
		h.flash(w, r, "err", "Policy error: "+err.Error())
		http.Redirect(w, r, "/le/issue", http.StatusFound)
		return
	}

	if appdb.LECertExists(r.Context(), h.db, domain) {
		h.flash(w, r, "err", "A certificate for this domain already exists")
		http.Redirect(w, r, "/le/issue", http.StatusFound)
		return
	}

	settings, _ := appdb.GetLESettings(r.Context(), h.db)

	// Create a DB record with status pending
	id, err := appdb.CreateLECert(r.Context(), h.db, domain, email, provider, autoRenew)
	if err != nil {
		h.flash(w, r, "err", "Failed to create record: "+err.Error())
		http.Redirect(w, r, "/le/issue", http.StatusFound)
		return
	}

	// The directory URL is recorded per issuance so the value actually in
	// effect is DB-visible, not only a startup log line.
	appdb.AddLELog(r.Context(), h.db, domain, "issue",
		fmt.Sprintf("Certificate issuance started (directory: %s)", h.cfg.LEACMEDirectoryURL))

	// Issue in the background — use Background so the goroutine outlives the request.
	bgCtx := context.Background()
	safeGo("le-issue:"+domain, func() {
		result, err := le.IssueCert(&le.LEConfig{
			Email:        email,
			Domain:       domain,
			Provider:     provider,
			CFToken:      settings.CFToken,
			CFZoneID:     settings.CFZoneID,
			R53KeyID:     settings.R53KeyID,
			R53Secret:    settings.R53SecretKey,
			R53Region:    settings.R53Region,
			DirectoryURL: h.cfg.LEACMEDirectoryURL,
		})
		if err != nil {
			_ = appdb.UpdateLECertStatus(bgCtx, h.db, id, "error", err.Error())
			appdb.AddLELog(bgCtx, h.db, domain, "error", fmt.Sprintf("Error: %v", err))
			return
		}
		_ = appdb.UpdateLECertPaths(bgCtx, h.db, id, result.CertPath, result.KeyPath, result.IssuedAt, result.ExpiresAt)
		appdb.AddLELog(bgCtx, h.db, domain, "issue", "Certificate issued successfully")
	})

	h.flash(w, r, "ok", fmt.Sprintf("Certificate issuance for %s started. Status will update in about a minute.", domain))
	http.Redirect(w, r, "/le", http.StatusFound)
}

// ─── Renew ────────────────────────────────────────────────────────────────────

// LERenew handles a manual ACME certificate renewal POST request.
func (h *Handler) LERenew(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/le") {
		return
	}
	cert, ok := h.leCertFromURL(w, r)
	if !ok {
		return
	}
	if cert == nil {
		http.Redirect(w, r, "/le", http.StatusFound)
		return
	}
	// A name that has fallen outside policy must not renew its way around it.
	if err := checkDomainPolicy(cert.Domain, h.cfg.AllowedDomainSuffixes); err != nil {
		h.flash(w, r, "err", "Policy error: "+err.Error())
		http.Redirect(w, r, "/le", http.StatusFound)
		return
	}
	id := cert.ID

	settings, _ := appdb.GetLESettings(r.Context(), h.db)
	_ = appdb.UpdateLECertStatus(r.Context(), h.db, id, "pending", "")
	appdb.AddLELog(r.Context(), h.db, cert.Domain, "renew", "Manual renewal started")

	// Use Background so the goroutine outlives the request.
	bgCtx := context.Background()
	safeGo("le-renew:"+cert.Domain, func() {
		result, err := le.IssueCert(&le.LEConfig{
			Email:        cert.Email,
			Domain:       cert.Domain,
			Provider:     cert.Provider,
			CFToken:      settings.CFToken,
			CFZoneID:     settings.CFZoneID,
			DirectoryURL: h.cfg.LEACMEDirectoryURL,
		})
		if err != nil {
			_ = appdb.UpdateLECertStatus(bgCtx, h.db, id, "error", err.Error())
			appdb.AddLELog(bgCtx, h.db, cert.Domain, "error", fmt.Sprintf("Renewal error: %v", err))
			return
		}
		_ = appdb.UpdateLECertPaths(bgCtx, h.db, id, result.CertPath, result.KeyPath, result.IssuedAt, result.ExpiresAt)
		appdb.AddLELog(bgCtx, h.db, cert.Domain, "renew", "Certificate renewed successfully")
	})

	h.flash(w, r, "ok", "Renewal started!")
	http.Redirect(w, r, "/le", http.StatusFound)
}

// ─── Delete ───────────────────────────────────────────────────────────────────

// LEDelete handles deletion of an ACME certificate record.
func (h *Handler) LEDelete(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/le") {
		return
	}
	cert, ok := h.leCertFromURL(w, r)
	if !ok {
		return
	}
	if cert != nil {
		appdb.AddLELog(r.Context(), h.db, cert.Domain, "delete", "Certificate removed from the system")
		_ = appdb.DeleteLECert(r.Context(), h.db, cert.ID)
	}
	h.flash(w, r, "ok", "Certificate deleted")
	http.Redirect(w, r, "/le", http.StatusFound)
}

// ─── Toggle AutoRenew ─────────────────────────────────────────────────────────

// LEToggleAutoRenew toggles the auto-renewal flag for an ACME certificate.
func (h *Handler) LEToggleAutoRenew(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/le") {
		return
	}
	cert, ok := h.leCertFromURL(w, r)
	if !ok {
		return
	}
	if cert != nil {
		_ = appdb.UpdateLECertAutoRenew(r.Context(), h.db, cert.ID, !cert.AutoRenew)
		if !cert.AutoRenew {
			h.flash(w, r, "ok", "Auto-renewal enabled")
		} else {
			h.flash(w, r, "ok", "Auto-renewal disabled")
		}
	}
	http.Redirect(w, r, "/le", http.StatusFound)
}

// ─── Download ─────────────────────────────────────────────────────────────────

// LEDownloadCert serves the certificate file for an ACME certificate.
func (h *Handler) LEDownloadCert(w http.ResponseWriter, r *http.Request) {
	cert, ok := h.leCertFromURL(w, r)
	if !ok {
		return
	}
	if cert == nil || cert.CertPath == "" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.crt", cert.Domain))
	http.ServeFile(w, r, cert.CertPath)
}

// LEDownloadKey serves the private key file for an ACME certificate.
func (h *Handler) LEDownloadKey(w http.ResponseWriter, r *http.Request) {
	cert, ok := h.leCertFromURL(w, r)
	if !ok {
		return
	}
	if cert == nil || cert.KeyPath == "" {
		http.NotFound(w, r)
		return
	}
	h.auditSecurity(r, fmt.Sprintf("le.key_download id=%d domain=%s", cert.ID, cert.Domain))
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s.key", cert.Domain))
	http.ServeFile(w, r, cert.KeyPath)
}

// ─── Settings ─────────────────────────────────────────────────────────────────

// LESettingsGet renders the ACME provider settings page.
func (h *Handler) LESettingsGet(w http.ResponseWriter, r *http.Request) {
	settings, _ := appdb.GetLESettings(r.Context(), h.db)
	data := h.base(w, r, "le-settings")
	data["LESettings"] = settings
	h.render(w, "le_settings", data)
}

// LESettingsPost handles saving ACME provider settings.
func (h *Handler) LESettingsPost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/le/settings") {
		return
	}
	current, err := appdb.GetLESettings(r.Context(), h.db)
	if err != nil {
		h.flash(w, r, "err", "Load error: "+err.Error())
		http.Redirect(w, r, "/le/settings", http.StatusFound)
		return
	}
	settings := parseLESettingsFields(r, current)
	if err := appdb.SaveLESettings(r.Context(), h.db, settings); err != nil {
		h.flash(w, r, "err", "Save error: "+err.Error())
	} else {
		h.auditSecurity(r, fmt.Sprintf("le.settings.save provider=%s email=%s cf_configured=%t r53_configured=%t",
			settings.Provider, settings.Email,
			settings.CFToken != "" || settings.CFZoneID != "",
			settings.R53KeyID != "" || settings.R53SecretKey != ""))
		h.flash(w, r, "ok", "Settings saved")
	}
	http.Redirect(w, r, "/le/settings", http.StatusFound)
}

// parseLESettingsFields extracts the ACME settings form fields from r into a
// new LESettings. current is the live DB row and supplies the provider secrets
// when their fields come back blank.
func parseLESettingsFields(r *http.Request, current *models.LESettings) *models.LESettings {
	s := &models.LESettings{
		Email:     trimStr(r.FormValue("email")),
		Provider:  r.FormValue("provider"),
		CFZoneID:  trimStr(r.FormValue("cf_zone_id")),
		R53KeyID:  trimStr(r.FormValue("r53_key_id")),
		R53Region: trimStr(r.FormValue("r53_region")),
	}
	if s.R53Region == "" {
		s.R53Region = "us-east-1"
	}

	// Secret-preserve-on-blank: only update when a new non-empty value is
	// submitted. The form no longer echoes these back (V2), so blank means
	// "unchanged" and must not erase a stored credential.
	if v := trimStr(r.FormValue("cf_token")); v == "" {
		s.CFToken = current.CFToken
	} else {
		s.CFToken = v
	}
	if v := trimStr(r.FormValue("r53_secret")); v == "" {
		s.R53SecretKey = current.R53SecretKey
	} else {
		s.R53SecretKey = v
	}

	return s
}

// ─── Logs ─────────────────────────────────────────────────────────────────────

// LELogs renders the ACME operation log page with optional domain filter.
func (h *Handler) LELogs(w http.ResponseWriter, r *http.Request) {
	domain := r.URL.Query().Get("domain")
	logs, _ := appdb.GetLELogs(r.Context(), h.db, domain, 100)
	data := h.base(w, r, "le-logs")
	data["LELogs"] = logs
	data["FilterDomain"] = domain
	h.render(w, "le_logs", data)
}
