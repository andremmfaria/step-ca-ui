package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"step-ui/le"

	appdb "step-ui/db"
)

// StartRenewer starts the LE auto-renewal loop as a panic-safe background
// goroutine.  It replaces the bare le.StartRenewer call so renewal failures
// can flow through notifyAsync and InsertHistory — matching the synchronous
// manual-renew path.
func (h *Handler) StartRenewer() {
	safeGo("le-renewer", func() {
		slog.Info("LE auto-renewer started", "interval", "24h")
		// First check after 5 min to avoid hammering ACME on startup.
		time.Sleep(5 * time.Minute)
		for {
			h.runRenewal()
			time.Sleep(24 * time.Hour)
		}
	})
}

func (h *Handler) runRenewal() {
	// Background context: this runs outside any HTTP request lifetime.
	ctx := context.Background()

	certs, err := appdb.GetLECertsForRenewal(ctx, h.db)
	if err != nil {
		slog.Error("LE renewal check failed", "err", err)
		return
	}
	if len(certs) == 0 {
		slog.Debug("LE renewal: no certificates need renewal")
		return
	}
	slog.Info("LE renewal: certificates to renew", "count", len(certs))

	settings, err := appdb.GetLESettings(ctx, h.db)
	if err != nil {
		slog.Error("LE renewal: cannot load settings", "err", err)
		return
	}

	for _, cert := range certs {
		slog.Info("LE renewing certificate", "domain", cert.Domain)
		appdb.AddLELog(ctx, h.db, cert.Domain, "renew", "Начало автоматического обновления")

		email := cert.Email
		if email == "" {
			email = settings.Email
		}
		provider := cert.Provider
		if provider == "" {
			provider = settings.Provider
		}

		result, err := le.IssueCert(&le.LEConfig{
			Email:     email,
			Domain:    cert.Domain,
			Provider:  provider,
			CFToken:   settings.CFToken,
			CFZoneID:  settings.CFZoneID,
			R53KeyID:  settings.R53KeyID,
			R53Secret: settings.R53SecretKey,
			R53Region: settings.R53Region,
		})
		if err != nil {
			if dbErr := appdb.UpdateLECertStatus(ctx, h.db, cert.ID, "error", err.Error()); dbErr != nil {
				slog.Error("LE renewal: failed to update cert status", "domain", cert.Domain, "err", dbErr)
			}
			appdb.AddLELog(ctx, h.db, cert.Domain, "error", fmt.Sprintf("Ошибка обновления: %v", err))
			slog.Error("LE renewal failed", "domain", cert.Domain, "err", err)
			// Emit a notification so the failure appears in history like the manual path.
			h.notifyAsync(
				"", "certificate.renew_failed", "error",
				"LE certificate auto-renewal failed",
				fmt.Sprintf("Auto-renewal failed for %s: %v", cert.Domain, err),
				map[string]string{"domain": cert.Domain},
			)
			continue
		}
		if dbErr := appdb.UpdateLECertPaths(ctx, h.db, cert.ID, result.CertPath, result.KeyPath, result.IssuedAt, result.ExpiresAt); dbErr != nil {
			slog.Error("LE renewal: failed to update cert paths", "domain", cert.Domain, "err", dbErr)
		}
		appdb.AddLELog(ctx, h.db, cert.Domain, "renew", "Сертификат успешно обновлён")
		slog.Info("LE certificate renewed successfully", "domain", cert.Domain)
	}
}
