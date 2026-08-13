// Package config loads application configuration from environment variables.
// Sensitive values (SECRET_KEY, DATABASE_URL) are read via *_FILE variants
// when set so that they never appear in the process environment at runtime.
package config

import (
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"
)

// Config holds all runtime configuration loaded from environment variables.
type Config struct {
	DatabaseURL   string
	CAURL         string
	RootCert      string
	Provisioner   string
	PasswordFile  string
	StepCAImage   string
	SecretKey     string
	SessionSecure bool
	EnableHSTS    bool
	TrustProxy    bool

	// Raw value; main.go parses it and refuses to start when TrustProxy is set
	// and it names no usable block (V4).
	TrustedProxyCIDRs string

	// Empty means issuance is unrestricted, the behaviour that predates the key (V6).
	AllowedDomainSuffixes []string

	// PublicBaseURL is the externally reachable origin ("https://ca.example.com").
	// Links sent by email are built from this and never from the inbound
	// request, because Host and X-Forwarded-Proto are attacker controlled.
	PublicBaseURL string
	Port          int
	CertsDir      string
	UploadDir     string
	SSLCert       string
	SSLKey        string
	UseHTTPS      bool // explicit override; defaults to auto-detect via os.Stat

	// OIDC / JumpCloud SSO
	OIDCEnabled      bool
	OIDCIssuerURL    string
	OIDCClientID     string
	OIDCClientSecret string
	OIDCRedirectURL  string
	OIDCGroupClaim   string
	OIDCGroupAdmin   string
	OIDCGroupManager string
	OIDCGroupViewer  string
	OIDCDefaultRole  string
	OIDCSyncRole     bool

	// Local password login (break-glass when OIDC is primary)
	LocalLoginEnabled bool

	// In-process TLS bootstrap (Phase 5) — replaces entrypoint.sh's former
	// root-cert-fetch/leaf-issuance/renew-daemon shell logic.
	UITLSMode     string
	CAFingerprint string
	CARootCertPEM string
	UIHostname    string
	HostIP        string

	// Validity requested for the UI's own leaf when UITLSMode == "stepca".
	// The renewal loop sleeps ~2/3 of it.
	UICertDuration time.Duration

	// Env-only: a manager-editable DB setting must not be able to repoint
	// where LE issuance actually dials.
	LEACMEDirectoryURL string
}

const (
	LEProductionDirectoryURL = "https://acme-v02.api.letsencrypt.org/directory"
	defaultUICertDuration    = 8760 * time.Hour
)

// Load reads configuration from environment variables.
// Sensitive values support a *_FILE variant: if SECRET_KEY_FILE is set its
// contents are used as SECRET_KEY (entrypoint.sh performs the same expansion
// for DATABASE_URL).
func Load() *Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8443"))
	return &Config{
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://stepui:stepui@postgres:5432/stepui?sslmode=disable"),
		CAURL:         getEnv("CA_URL", "https://step-ca:9443"),
		RootCert:      getEnv("ROOT_CERT", "/home/step/certs/root_ca.crt"),
		Provisioner:   getEnv("PROVISIONER", "admin"),
		PasswordFile:  getEnv("PASSWORD_FILE", "/opt/step-ui/data/provisioner_password"),
		StepCAImage:   getEnv("STEP_CA_IMAGE", "smallstep/step-ca:0.30.2"),
		SecretKey:     getEnvOrFile("SECRET_KEY", "SECRET_KEY_FILE", "change-me-in-production-32chars!"),
		SessionSecure: getEnvBool("SESSION_SECURE", true),
		EnableHSTS:    getEnvBool("ENABLE_HSTS", false),
		TrustProxy:    getEnvBool("TRUST_PROXY", false),

		TrustedProxyCIDRs:     getEnv("TRUSTED_PROXY_CIDRS", ""),
		AllowedDomainSuffixes: getEnvList("ALLOWED_DOMAIN_SUFFIXES"),

		PublicBaseURL: strings.TrimRight(strings.TrimSpace(getEnv("PUBLIC_BASE_URL", "")), "/"),
		Port:          port,
		CertsDir:      "/opt/step-ui/certs",
		UploadDir:     "/opt/step-ui/uploads",
		SSLCert:       "/opt/step-ui/ssl/server.crt",
		SSLKey:        "/opt/step-ui/ssl/server.key",

		OIDCEnabled:      getEnvBool("OIDC_ENABLED", false),
		OIDCIssuerURL:    getEnv("OIDC_ISSUER_URL", ""),
		OIDCClientID:     getEnv("OIDC_CLIENT_ID", ""),
		OIDCClientSecret: getEnv("OIDC_CLIENT_SECRET", ""),
		OIDCRedirectURL:  getEnv("OIDC_REDIRECT_URL", ""),
		OIDCGroupClaim:   getEnv("OIDC_GROUP_CLAIM", "groups"),
		OIDCGroupAdmin:   getEnv("OIDC_GROUP_ADMIN", ""),
		OIDCGroupManager: getEnv("OIDC_GROUP_MANAGER", ""),
		OIDCGroupViewer:  getEnv("OIDC_GROUP_VIEWER", ""),
		OIDCDefaultRole:  getEnv("OIDC_DEFAULT_ROLE", ""),
		OIDCSyncRole:     getEnvBool("OIDC_SYNC_ROLE", true),

		LocalLoginEnabled: getEnvBool("LOCAL_LOGIN_ENABLED", true),
		// UseHTTPS: when false (default), the server probes os.Stat(SSLCert) to decide.
		// Set USE_HTTPS=true to force TLS; USE_HTTPS=false to force plain HTTP.
		// The empty env case preserves the existing auto-detect behaviour.
		UseHTTPS: getEnvBool("USE_HTTPS", false),

		UITLSMode:     getEnv("UI_TLS_MODE", "self-signed"),
		CAFingerprint: getEnv("CA_FINGERPRINT", ""),
		CARootCertPEM: getEnv("CA_ROOT_CERT_PEM", ""),
		UIHostname:    getEnv("UI_HOSTNAME", ""),
		HostIP:        getEnv("HOST_IP", "127.0.0.1"),

		UICertDuration:     getEnvDuration("UI_CERT_DURATION", defaultUICertDuration),
		LEACMEDirectoryURL: getEnv("LE_ACME_DIRECTORY_URL", LEProductionDirectoryURL),
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

// getEnvOrFile reads key from the environment; if absent it falls back to
// reading the path stored in fileKey (the *_FILE pattern).  def is returned
// when neither is set.
func getEnvOrFile(key, fileKey, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	if path := os.Getenv(fileKey); path != "" {
		//nolint:gosec // G304: path comes from an operator-controlled env var (*_FILE), same trust level as PasswordFile
		if data, err := os.ReadFile(path); err == nil {
			return strings.TrimRight(string(data), "\r\n")
		}
	}
	return def
}

// getEnvList splits a comma-separated variable into trimmed, lower-cased,
// non-empty entries. A leading dot on an entry is dropped so that
// ".example.com" and "example.com" mean the same thing.
func getEnvList(key string) []string {
	raw := os.Getenv(key)
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	var out []string
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimPrefix(strings.ToLower(strings.TrimSpace(part)), ".")
		if entry != "" {
			out = append(out, entry)
		}
	}
	return out
}

// getEnvDuration parses a Go duration string. An unparseable or non-positive
// value falls back to def with a warning, the posture getEnvBool already uses.
func getEnvDuration(key string, def time.Duration) time.Duration {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d <= 0 {
		slog.Warn("invalid duration, using default", "key", key, "value", v, "default", def)
		return def
	}
	return d
}

func getEnvBool(key string, def bool) bool {
	v := os.Getenv(key)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
