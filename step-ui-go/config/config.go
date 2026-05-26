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
	SecretKey     string
	SessionSecure bool
	Port          int
	CertsDir      string
	UploadDir     string
	SSLCert       string
	SSLKey        string
}

func Load() *Config {
	port, _ := strconv.Atoi(getEnv("PORT", "8443"))
	return &Config{
		DatabaseURL:   getEnv("DATABASE_URL", "postgres://stepui:stepui@postgres:5432/stepui?sslmode=disable"),
		CAURL:         getEnv("CA_URL", "https://step-ca:9443"),
		RootCert:      getEnv("ROOT_CERT", "/home/step/certs/root_ca.crt"),
		Provisioner:   getEnv("PROVISIONER", "admin"),
		PasswordFile:  getEnv("PASSWORD_FILE", "/opt/step-ui/data/provisioner_password"),
		SecretKey:     getEnv("SECRET_KEY", "change-me-in-production-32chars!"),
		SessionSecure: getEnvBool("SESSION_SECURE", true),
		Port:          port,
		CertsDir:      "/opt/step-ui/certs",
		UploadDir:     "/opt/step-ui/uploads",
		SSLCert:       "/opt/step-ui/ssl/server.crt",
		SSLKey:        "/opt/step-ui/ssl/server.key",
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
