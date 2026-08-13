package config

import (
	"os"
	"testing"
)

// TestLoad_Defaults verifies that Load returns sane defaults when no env vars
// are set.  Each field exercises the getEnv / getEnvBool path.
func TestLoad_Defaults(t *testing.T) {
	// Clear any env vars that might leak from CI or the shell.
	clearEnvVars(t)

	cfg := Load()

	if cfg.Port != 8443 {
		t.Errorf("Port: got %d want 8443", cfg.Port)
	}
	if cfg.Provisioner != "admin" {
		t.Errorf("Provisioner: got %q want %q", cfg.Provisioner, "admin")
	}
	if cfg.OIDCGroupClaim != "groups" {
		t.Errorf("OIDCGroupClaim: got %q want %q", cfg.OIDCGroupClaim, "groups")
	}
	if cfg.OIDCEnabled {
		t.Error("OIDCEnabled: got true want false")
	}
	if !cfg.OIDCSyncRole {
		t.Error("OIDCSyncRole: got false want true")
	}
	if !cfg.LocalLoginEnabled {
		t.Error("LocalLoginEnabled: got false want true")
	}
	if cfg.EnableHSTS {
		t.Error("EnableHSTS: got true want false")
	}
	if cfg.TrustProxy {
		t.Error("TrustProxy: got true want false")
	}
	if cfg.TrustedProxyCIDRs != "" {
		t.Errorf("TrustedProxyCIDRs: got %q want empty", cfg.TrustedProxyCIDRs)
	}
	if len(cfg.AllowedDomainSuffixes) != 0 {
		t.Errorf("AllowedDomainSuffixes: got %v want empty (issuance unrestricted by default)", cfg.AllowedDomainSuffixes)
	}
	if cfg.UITLSMode != "self-signed" {
		t.Errorf("UITLSMode: got %q want %q", cfg.UITLSMode, "self-signed")
	}
	if cfg.CAFingerprint != "" {
		t.Errorf("CAFingerprint: got %q want empty", cfg.CAFingerprint)
	}
	if cfg.CARootCertPEM != "" {
		t.Errorf("CARootCertPEM: got %q want empty", cfg.CARootCertPEM)
	}
	if cfg.UIHostname != "" {
		t.Errorf("UIHostname: got %q want empty", cfg.UIHostname)
	}
	if cfg.HostIP != "127.0.0.1" {
		t.Errorf("HostIP: got %q want %q", cfg.HostIP, "127.0.0.1")
	}
}

// TestLoad_EnvOverrides verifies that env vars override every default.
func TestLoad_EnvOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("PROVISIONER", "myprovisioner")
	t.Setenv("OIDC_ENABLED", "true")
	t.Setenv("OIDC_ISSUER_URL", "https://idp.example.com")
	t.Setenv("OIDC_CLIENT_ID", "client123")
	t.Setenv("OIDC_CLIENT_SECRET", "secret456")
	t.Setenv("OIDC_GROUP_CLAIM", "roles")
	t.Setenv("OIDC_GROUP_ADMIN", "admins")
	t.Setenv("OIDC_GROUP_MANAGER", "managers")
	t.Setenv("OIDC_GROUP_VIEWER", "viewers")
	t.Setenv("OIDC_DEFAULT_ROLE", "viewer")
	t.Setenv("OIDC_SYNC_ROLE", "false")
	t.Setenv("LOCAL_LOGIN_ENABLED", "false")
	t.Setenv("ENABLE_HSTS", "true")
	t.Setenv("TRUST_PROXY", "true")
	t.Setenv("TRUSTED_PROXY_CIDRS", "10.0.0.7/32,192.168.0.0/16")
	t.Setenv("ALLOWED_DOMAIN_SUFFIXES", " Example.COM , .internal.example.net ,, ")
	t.Setenv("USE_HTTPS", "true")
	t.Setenv("SECRET_KEY", "overridden-secret-key-32chars!!")
	t.Setenv("CA_URL", "https://myca:9443")
	t.Setenv("ROOT_CERT", "/my/root.crt")
	t.Setenv("UI_TLS_MODE", "stepca")
	t.Setenv("CA_FINGERPRINT", "deadbeef")
	t.Setenv("CA_ROOT_CERT_PEM", "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----")
	t.Setenv("UI_HOSTNAME", "ui.internal.example.com")
	t.Setenv("HOST_IP", "10.0.0.5")

	cfg := Load()

	if cfg.Port != 9090 {
		t.Errorf("Port: got %d want 9090", cfg.Port)
	}
	if cfg.Provisioner != "myprovisioner" {
		t.Errorf("Provisioner: got %q", cfg.Provisioner)
	}
	if !cfg.OIDCEnabled {
		t.Error("OIDCEnabled: got false want true")
	}
	if cfg.OIDCIssuerURL != "https://idp.example.com" {
		t.Errorf("OIDCIssuerURL: got %q", cfg.OIDCIssuerURL)
	}
	if cfg.OIDCGroupClaim != "roles" {
		t.Errorf("OIDCGroupClaim: got %q want roles", cfg.OIDCGroupClaim)
	}
	if cfg.OIDCSyncRole {
		t.Error("OIDCSyncRole: got true want false")
	}
	if cfg.LocalLoginEnabled {
		t.Error("LocalLoginEnabled: got true want false")
	}
	if !cfg.EnableHSTS {
		t.Error("EnableHSTS: got false want true")
	}
	if !cfg.TrustProxy {
		t.Error("TrustProxy: got false want true")
	}
	if cfg.TrustedProxyCIDRs != "10.0.0.7/32,192.168.0.0/16" {
		t.Errorf("TrustedProxyCIDRs: got %q", cfg.TrustedProxyCIDRs)
	}
	// Entries are trimmed, lower-cased and stripped of a leading dot so that
	// ".example.net" and "example.net" cannot disagree.
	if got := cfg.AllowedDomainSuffixes; len(got) != 2 || got[0] != "example.com" || got[1] != "internal.example.net" {
		t.Errorf("AllowedDomainSuffixes: got %q want [example.com internal.example.net]", got)
	}
	if !cfg.UseHTTPS {
		t.Error("UseHTTPS: got false want true")
	}
	if cfg.SecretKey != "overridden-secret-key-32chars!!" {
		t.Errorf("SecretKey: got %q", cfg.SecretKey)
	}
	if cfg.CAURL != "https://myca:9443" {
		t.Errorf("CAURL: got %q", cfg.CAURL)
	}
	if cfg.RootCert != "/my/root.crt" {
		t.Errorf("RootCert: got %q", cfg.RootCert)
	}
	if cfg.UITLSMode != "stepca" {
		t.Errorf("UITLSMode: got %q want %q", cfg.UITLSMode, "stepca")
	}
	if cfg.CAFingerprint != "deadbeef" {
		t.Errorf("CAFingerprint: got %q want %q", cfg.CAFingerprint, "deadbeef")
	}
	if cfg.CARootCertPEM != "-----BEGIN CERTIFICATE-----\nabc\n-----END CERTIFICATE-----" {
		t.Errorf("CARootCertPEM: got %q", cfg.CARootCertPEM)
	}
	if cfg.UIHostname != "ui.internal.example.com" {
		t.Errorf("UIHostname: got %q want %q", cfg.UIHostname, "ui.internal.example.com")
	}
	if cfg.HostIP != "10.0.0.5" {
		t.Errorf("HostIP: got %q want %q", cfg.HostIP, "10.0.0.5")
	}
}

// TestGetEnvBool_InvalidValue verifies that an unparseable boolean value falls
// back to the default.
func TestGetEnvBool_InvalidValue(t *testing.T) {
	t.Setenv("ENABLE_HSTS", "notabool")
	cfg := Load()
	// Default for ENABLE_HSTS is false; invalid value must not flip it.
	if cfg.EnableHSTS {
		t.Error("invalid bool env var should fall back to default false")
	}
}

// TestGetEnvBool_EmptyValue verifies that an empty env var returns the default.
func TestGetEnvBool_EmptyValue(t *testing.T) {
	t.Setenv("ENABLE_HSTS", "")
	cfg := Load()
	if cfg.EnableHSTS {
		t.Error("empty env var should return default false for EnableHSTS")
	}
}

// TestLoad_InvalidPort verifies that an invalid PORT falls back to 0
// (strconv.Atoi returns 0 on error; the server would fail to bind, which is
// observable behavior worth documenting).
func TestLoad_InvalidPort(t *testing.T) {
	t.Setenv("PORT", "notanumber")
	cfg := Load()
	if cfg.Port != 0 {
		t.Errorf("invalid PORT: got %d want 0", cfg.Port)
	}
}

// clearEnvVars unsets all env vars that Load reads so tests start from a clean
// slate.  t.Setenv handles restoration automatically via t.Cleanup.
func clearEnvVars(t *testing.T) {
	t.Helper()
	vars := []string{
		"PORT", "DATABASE_URL", "CA_URL", "ROOT_CERT", "PROVISIONER",
		"PASSWORD_FILE", "STEP_CA_IMAGE", "SECRET_KEY", "SECRET_KEY_FILE",
		"SESSION_SECURE", "ENABLE_HSTS", "TRUST_PROXY", "TRUSTED_PROXY_CIDRS",
		"ALLOWED_DOMAIN_SUFFIXES", "OIDC_ENABLED",
		"OIDC_ISSUER_URL", "OIDC_CLIENT_ID", "OIDC_CLIENT_SECRET",
		"OIDC_REDIRECT_URL", "OIDC_GROUP_CLAIM", "OIDC_GROUP_ADMIN",
		"OIDC_GROUP_MANAGER", "OIDC_GROUP_VIEWER", "OIDC_DEFAULT_ROLE",
		"OIDC_SYNC_ROLE", "LOCAL_LOGIN_ENABLED", "USE_HTTPS",
		"UI_TLS_MODE", "CA_FINGERPRINT", "CA_ROOT_CERT_PEM", "UI_HOSTNAME", "HOST_IP",
	}
	for _, v := range vars {
		t.Setenv(v, "")
	}
}

// TestGetEnvOrFile covers all four branches of getEnvOrFile:
//   - plain env var set → returns the var value
//   - _FILE variant pointing at a temp file → returns trimmed file contents
//   - neither set → returns the default
//   - _FILE set but file unreadable → returns the default
func TestGetEnvOrFile(t *testing.T) {
	t.Run("plain_env_var", func(t *testing.T) {
		clearEnvVars(t)
		t.Setenv("SECRET_KEY", "mysecretkey")
		cfg := Load()
		if cfg.SecretKey != "mysecretkey" {
			t.Errorf("SecretKey: got %q want %q", cfg.SecretKey, "mysecretkey")
		}
	})

	t.Run("file_variant", func(t *testing.T) {
		clearEnvVars(t)
		dir := t.TempDir()
		// Write the secret with a trailing newline, as docker secrets typically do.
		f := dir + "/secret.txt"
		if err := os.WriteFile(f, []byte("file-based-secret\n"), 0o600); err != nil {
			t.Fatalf("write temp file: %v", err)
		}
		t.Setenv("SECRET_KEY_FILE", f)
		cfg := Load()
		if cfg.SecretKey != "file-based-secret" {
			t.Errorf("SecretKey via _FILE: got %q want %q", cfg.SecretKey, "file-based-secret")
		}
	})

	t.Run("neither_set_returns_default", func(t *testing.T) {
		clearEnvVars(t)
		cfg := Load()
		if cfg.SecretKey != "change-me-in-production-32chars!" {
			t.Errorf("SecretKey default: got %q", cfg.SecretKey)
		}
	})

	t.Run("file_unreadable_returns_default", func(t *testing.T) {
		clearEnvVars(t)
		t.Setenv("SECRET_KEY_FILE", "/nonexistent/path/secret.txt")
		cfg := Load()
		if cfg.SecretKey != "change-me-in-production-32chars!" {
			t.Errorf("SecretKey on bad path: got %q", cfg.SecretKey)
		}
	})
}
