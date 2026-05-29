package config

import (
	"os"
	"strconv"
)

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
	Port          int
	CertsDir      string
	UploadDir     string
	SSLCert       string
	SSLKey        string

	// OIDC / JumpCloud SSO
	OIDCEnabled       bool
	OIDCIssuerURL     string
	OIDCClientID      string
	OIDCClientSecret  string
	OIDCRedirectURL   string
	OIDCGroupClaim    string
	OIDCGroupAdmin    string
	OIDCGroupManager  string
	OIDCGroupViewer   string
	OIDCDefaultRole   string
	OIDCSyncRole      bool

	// Local password login (break-glass when OIDC is primary)
	LocalLoginEnabled bool
}

func Load() *Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8443"))
	return &Config{
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://stepui:stepui@postgres:5432/stepui?sslmode=disable"),
		CAURL:         getEnv("CA_URL", "https://step-ca:9443"),
		RootCert:      getEnv("ROOT_CERT", "/home/step/certs/root_ca.crt"),
		Provisioner:   getEnv("PROVISIONER", "admin"),
		PasswordFile:  getEnv("PASSWORD_FILE", "/opt/step-ui/data/provisioner_password"),
		StepCAImage:   getEnv("STEP_CA_IMAGE", "smallstep/step-ca:0.30.2"),
		SecretKey:     getEnv("SECRET_KEY", "change-me-in-production-32chars!"),
		SessionSecure: getEnvBool("SESSION_SECURE", true),
		EnableHSTS:    getEnvBool("ENABLE_HSTS", false),
		TrustProxy:    getEnvBool("TRUST_PROXY", false),
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
	}
}

func getEnv(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
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
