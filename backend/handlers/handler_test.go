package handlers

import (
	"testing"

	"step-ui/config"
)

// TestCAClient_ConstructionFailureNotCached confirms Handler.caClient() never
// permanently poisons itself on a construction error (R2): a cfg.RootCert
// pointing at a nonexistent file makes ca.NewClient fail every time (the
// underlying library reads the root file eagerly — see stepca/doc.go), so
// repeated calls must each return a fresh, non-nil error without panicking
// or fataling the process.
func TestCAClient_ConstructionFailureNotCached(t *testing.T) {
	h := newMinimalHandler(&config.Config{
		CAURL:    "https://ca.invalid:9443",
		RootCert: "/does/not/exist/root.crt",
	})

	if _, err := h.caClient(); err == nil {
		t.Fatal("expected error from caClient() with a nonexistent root cert, got nil")
	}
	if _, err := h.caClient(); err == nil {
		t.Fatal("expected error from caClient() on second call too — a construction failure must not be cached")
	}
	if h.ca != nil {
		t.Error("h.ca must remain nil after repeated construction failures")
	}
}
