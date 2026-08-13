package models

import (
	"testing"
	"time"
)

// TestFlashMsg exercises the FlashMsg struct fields.
func TestFlashMsg(t *testing.T) {
	m := FlashMsg{Type: "ok", Text: "all good"}
	if m.Type != "ok" {
		t.Errorf("Type: got %q want %q", m.Type, "ok")
	}
	if m.Text != "all good" {
		t.Errorf("Text: got %q want %q", m.Text, "all good")
	}
}

// TestSessionInfo exercises the SessionInfo struct fields.
func TestSessionInfo(t *testing.T) {
	si := SessionInfo{UserID: 42, Username: "alice", Role: "admin", Theme: "dark"}
	if si.UserID != 42 {
		t.Errorf("UserID: got %d want 42", si.UserID)
	}
	if si.Role != "admin" {
		t.Errorf("Role: got %q want admin", si.Role)
	}
}

// TestCertificate exercises zero-value sentinel for expired-at.
func TestCertificate(t *testing.T) {
	now := time.Now()
	c := Certificate{
		ID:        1,
		Name:      "test-cert",
		Domain:    "example.com",
		ExpiresAt: &now,
	}
	if c.ExpiresAt == nil {
		t.Error("ExpiresAt should not be nil")
	}
	if c.Name != "test-cert" {
		t.Errorf("Name: got %q", c.Name)
	}
}

// TestLECertificate exercises the LECertificate model fields.
func TestLECertificate(t *testing.T) {
	c := LECertificate{
		Domain:    "acme.example.com",
		Provider:  "cloudflare",
		AutoRenew: true,
		Status:    "active",
	}
	if c.Provider != "cloudflare" {
		t.Errorf("Provider: got %q", c.Provider)
	}
	if !c.AutoRenew {
		t.Error("AutoRenew should be true")
	}
}

// TestLESettings exercises the LESettings model.
func TestLESettings(t *testing.T) {
	s := LESettings{
		Email:    "user@example.com",
		Provider: "http01",
	}
	if s.Email != "user@example.com" {
		t.Errorf("Email: got %q", s.Email)
	}
}

// TestNotificationSettings exercises NotificationSettings.
func TestNotificationSettings(t *testing.T) {
	ns := NotificationSettings{
		WebhookEnabled: true,
		WebhookURL:     "https://hook.example.com",
		NotifyExpiry:   true,
		ExpiryDays:     30,
	}
	if !ns.WebhookEnabled {
		t.Error("WebhookEnabled should be true")
	}
	if ns.ExpiryDays != 30 {
		t.Errorf("ExpiryDays: got %d want 30", ns.ExpiryDays)
	}
}

// TestUser exercises the User model fields including TOTP.
func TestUser(t *testing.T) {
	u := User{ //nolint:gosec // G101: test-only TOTP seed value
		ID:          1,
		Username:    "bob",
		Role:        "manager",
		IsActive:    true,
		TOTPEnabled: true,
		TOTPSecret:  "JBSWY3DPEHPK3PXP",
	}
	if !u.IsActive {
		t.Error("IsActive should be true")
	}
	if u.TOTPSecret != "JBSWY3DPEHPK3PXP" {
		t.Errorf("TOTPSecret: got %q", u.TOTPSecret)
	}
}

// TestAuthLog exercises the AuthLog struct.
func TestAuthLog(t *testing.T) {
	al := AuthLog{
		Username:  "charlie",
		IP:        "192.0.2.1",
		Success:   false,
		Reason:    "bad password",
		CreatedAt: time.Now(),
	}
	if al.Success {
		t.Error("Success should be false")
	}
	if al.IP != "192.0.2.1" {
		t.Errorf("IP: got %q", al.IP)
	}
}
