package handlers

import (
	"fmt"
	"log"
	"step-ui/le"
	"time"

	appdb "step-ui/db"
)

// StartRenewer starts the LE auto-renewal loop as a panic-safe background
// goroutine.  It replaces the bare le.StartRenewer call so renewal failures
// can flow through notifyAsync and InsertHistory — matching the synchronous
// manual-renew path.
func (h *Handler) StartRenewer() {
	safeGo("le-renewer", func() {
		log.Println("[LE] Auto-renewer started (checks every 24h)")
		// First check after 5 min to avoid hammering ACME on startup.
		time.Sleep(5 * time.Minute)
		for {
			h.runRenewal()
			time.Sleep(24 * time.Hour)
		}
	})
}

func (h *Handler) runRenewal() {
	certs, err := appdb.GetLECertsForRenewal(h.db)
	if err != nil {
		log.Printf("[LE] Renewal check error: %v", err)
		return
	}
	if len(certs) == 0 {
		log.Println("[LE] No certificates need renewal")
		return
	}
	log.Printf("[LE] Found %d certificate(s) to renew", len(certs))

	settings, err := appdb.GetLESettings(h.db)
	if err != nil {
		log.Printf("[LE] Cannot load settings: %v", err)
		return
	}

	for _, cert := range certs {
		log.Printf("[LE] Renewing %s...", cert.Domain)
		appdb.AddLELog(h.db, cert.Domain, "renew", "Начало автоматического обновления")

		email := cert.Email
		if email == "" {
			email = settings.Email
		}
		provider := cert.Provider
		if provider == "" {
			provider = settings.Provider
		}

		result, err := le.IssueCert(le.LEConfig{
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
			if dbErr := appdb.UpdateLECertStatus(h.db, cert.ID, "error", err.Error()); dbErr != nil {
				log.Printf("[LE] Failed to update cert status for %s: %v", cert.Domain, dbErr)
			}
			appdb.AddLELog(h.db, cert.Domain, "error", fmt.Sprintf("Ошибка обновления: %v", err))
			log.Printf("[LE] Renewal failed for %s: %v", cert.Domain, err)
			// Emit a notification so the failure appears in history like the manual path.
			h.notifyAsync("", "certificate.renew_failed", "error",
				"LE certificate auto-renewal failed",
				fmt.Sprintf("Auto-renewal failed for %s: %v", cert.Domain, err),
				map[string]string{"domain": cert.Domain},
			)
			continue
		}
		if dbErr := appdb.UpdateLECertPaths(h.db, cert.ID, result.CertPath, result.KeyPath, result.IssuedAt, result.ExpiresAt); dbErr != nil {
			log.Printf("[LE] Failed to update cert paths for %s: %v", cert.Domain, dbErr)
		}
		appdb.AddLELog(h.db, cert.Domain, "renew", "Сертификат успешно обновлён")
		log.Printf("[LE] Successfully renewed %s", cert.Domain)
	}
}
