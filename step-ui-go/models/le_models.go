package models

import "time"

// LECertificate represents an ACME (Let's Encrypt) certificate tracked by the system.
type LECertificate struct {
	ID        int
	Domain    string
	Email     string
	Provider  string // http01, cloudflare, route53, manual
	CertPath  string
	KeyPath   string
	IssuedAt  *time.Time
	ExpiresAt *time.Time
	AutoRenew bool
	Status    string // active, expired, pending, error
	LastError string
	CreatedAt *time.Time
	UpdatedAt *time.Time
}

// LESettings holds the ACME provider configuration (Cloudflare, Route53, etc.).
type LESettings struct {
	ID           int
	Email        string
	Provider     string
	CFToken      string // Cloudflare API token
	CFZoneID     string // Cloudflare Zone ID
	R53KeyID     string // AWS Route53 Access Key ID
	R53SecretKey string // AWS Route53 Secret Key
	R53Region    string // AWS Region
	UpdatedAt    *time.Time
}

// LELog is a single ACME lifecycle event log entry.
type LELog struct {
	ID        int
	Domain    string
	Action    string // issue, renew, revoke, error
	Message   string
	CreatedAt time.Time
}
