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

	appdb.AddLELog(r.Context(), h.db, domain, "issue", "Certificate issuance started")

	// Issue in the background — use Background so the goroutine outlives the request.
	bgCtx := context.Background()
	safeGo("le-issue:"+domain, func() {
		result, err := le.IssueCert(&le.LEConfig{
			Email:     email,
			Domain:    domain,
			Provider:  provider,
			CFToken:   settings.CFToken,
			CFZoneID:  settings.CFZoneID,
			R53KeyID:  settings.R53KeyID,
			R53Secret: settings.R53SecretKey,
			R53Region: settings.R53Region,
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
	id := cert.ID

	settings, _ := appdb.GetLESettings(r.Context(), h.db)
	_ = appdb.UpdateLECertStatus(r.Context(), h.db, id, "pending", "")
	appdb.AddLELog(r.Context(), h.db, cert.Domain, "renew", "Manual renewal started")

	// Use Background so the goroutine outlives the request.
	bgCtx := context.Background()
	safeGo("le-renew:"+cert.Domain, func() {
		result, err := le.IssueCert(&le.LEConfig{
			Email:    cert.Email,
			Domain:   cert.Domain,
			Provider: cert.Provider,
			CFToken:  settings.CFToken,
			CFZoneID: settings.CFZoneID,
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
	settings := &models.LESettings{
		Email:        trimStr(r.FormValue("email")),
		Provider:     r.FormValue("provider"),
		CFToken:      trimStr(r.FormValue("cf_token")),
		CFZoneID:     trimStr(r.FormValue("cf_zone_id")),
		R53KeyID:     trimStr(r.FormValue("r53_key_id")),
		R53SecretKey: trimStr(r.FormValue("r53_secret")),
		R53Region:    trimStr(r.FormValue("r53_region")),
	}
	if settings.R53Region == "" {
		settings.R53Region = "us-east-1"
	}
	if err := appdb.SaveLESettings(r.Context(), h.db, settings); err != nil {
		h.flash(w, r, "err", "Save error: "+err.Error())
	} else {
		h.flash(w, r, "ok", "Settings saved")
	}
	http.Redirect(w, r, "/le/settings", http.StatusFound)
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
