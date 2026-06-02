// Package models defines the data types used across the Step-CA UI application.
package models

import "time"

// User represents an application user account.
type User struct {
	ID                int
	Username          string
	PasswordHash      string
	Role              string
	IsActive          bool
	CreatedAt         *time.Time
	LastLogin         *time.Time
	LastIP            *string
	DisplayName       string
	Email             string
	Theme             string
	TOTPEnabled       bool
	TOTPSecret        string
	TOTPPendingSecret string
}

// AuthLog is a single authentication event record.
type AuthLog struct {
	ID        int
	Username  string
	IP        string
	Success   bool
	Reason    string
	CreatedAt time.Time
}

// Certificate represents a step-ca issued certificate stored in the database.
type Certificate struct {
	ID        int
	Name      string
	Domain    string
	CertPath  string
	KeyPath   string
	IssuedAt  *time.Time
	ExpiresAt *time.Time
	Serial    string
	Status    string
	KeyType   string
	CreatedAt *time.Time
	// IssueDuration stores the duration used at issuance so that Renew can
	// reuse it instead of falling back to the hardcoded 8760h default.
	IssueDuration string
}

// CertHistory records a certificate lifecycle action (issue, renew, revoke, import).
type CertHistory struct {
	ID        int
	Action    string
	CertName  string
	Domain    string
	Details   string
	Username  string
	Role      string
	CreatedAt time.Time
}

// FlashMsg is a one-shot UI notification stored in the session.
type FlashMsg struct {
	Type string // "ok" or "err"
	Text string
}

// SessionInfo holds the authenticated user data stored in the session cookie.
type SessionInfo struct {
	UserID   int
	Username string
	Role     string
	Theme    string
}

// NotificationSettings holds the webhook notification configuration.
type NotificationSettings struct {
	ID              int
	WebhookEnabled  bool
	WebhookURL      string
	NotifyExpiry    bool
	ExpiryDays      int
	NotifyFailures  bool
	NotifyAuthBurst bool
	UpdatedAt       *time.Time
}

// NotificationLog records a sent (or attempted) webhook notification.
type NotificationLog struct {
	ID        int
	EventKey  string
	EventType string
	Severity  string
	Title     string
	Message   string
	Success   bool
	Error     string
	CreatedAt time.Time
}
