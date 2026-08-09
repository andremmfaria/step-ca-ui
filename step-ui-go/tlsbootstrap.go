package main

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"

	"step-ui/config"
	"step-ui/handlers"
	"step-ui/stepca"
)

// caBootstrapRetries/caBootstrapInterval bound the root-fetch and
// leaf-issuance retry loops below (entrypoint.sh's former 30×1s loops).
// Overridable so tests don't take 30 seconds.
var (
	caBootstrapRetries  = 30
	caBootstrapInterval = time.Second
)

// uiCertRenewFailureBackoff is how long startUICertRenewer waits before
// retrying after a failed renewal attempt (renewUICertOnce returning an
// error) — distinct from the normal ~2/3-of-validity sleep on success.
const uiCertRenewFailureBackoff = 5 * time.Minute

// uiIssueDuration/uiIssueKeyType are the certificate parameters requested
// for the UI's own leaf cert — matching cert_ops.go's "server" template
// default (issueTemplates["server"]) and step ca certificate's default key
// type, since entrypoint.sh's old `step ca certificate` call passed neither
// --not-after nor --kty/--curve and relied on the CA's defaults, which this
// mirrors.
const (
	uiIssueDuration = 8760 * time.Hour
	uiIssueKeyType  = "EC:P-256"
)

// writeInlineRootCert writes cfg.CARootCertPEM to cfg.RootCert when set,
// replacing entrypoint.sh's CA_ROOT_CERT_PEM branch (ECS/Kubernetes
// deployments where a volume mount is impractical). No-op when unset.
func writeInlineRootCert(cfg *config.Config) error {
	if cfg.CARootCertPEM == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(cfg.RootCert), 0o750); err != nil { //nolint:gosec // G301: cfg.RootCert's parent dir, not user input
		return fmt.Errorf("creating root cert directory: %w", err)
	}
	if err := os.WriteFile(cfg.RootCert, []byte(cfg.CARootCertPEM), 0o644); err != nil { //nolint:gosec // G306: root cert is public, not a secret
		return fmt.Errorf("writing inline root cert: %w", err)
	}
	slog.Info("wrote root CA certificate from CA_ROOT_CERT_PEM", "path", cfg.RootCert)
	return nil
}

// ensureRootCert is a no-op if cfg.RootCert already exists and is non-empty
// (e.g. written by writeInlineRootCert above, or present via a volume
// mount). Otherwise, if cfg.CAFingerprint is set, it retries
// stepca.FetchRootByFingerprint up to caBootstrapRetries times and writes
// the result. On exhaustion it logs a warning and returns nil (non-fatal —
// matches entrypoint.sh's old behavior; CA connectivity is still surfaced
// later via /ready).
func ensureRootCert(ctx context.Context, cfg *config.Config) error {
	if info, err := os.Stat(cfg.RootCert); err == nil && info.Size() > 0 {
		return nil
	}
	if cfg.CAFingerprint == "" {
		return nil
	}

	slog.Info("fetching root CA certificate via CA_FINGERPRINT", "caURL", cfg.CAURL)
	var lastErr error
	for attempt := 1; attempt <= caBootstrapRetries; attempt++ {
		rootPEM, err := stepca.FetchRootByFingerprint(ctx, cfg.CAURL, cfg.CAFingerprint)
		if err == nil {
			if mkErr := os.MkdirAll(filepath.Dir(cfg.RootCert), 0o750); mkErr != nil { //nolint:gosec // G301: cfg.RootCert's parent dir, not user input
				return fmt.Errorf("creating root cert directory: %w", mkErr)
			}
			if writeErr := os.WriteFile(cfg.RootCert, rootPEM, 0o644); writeErr != nil { //nolint:gosec // G306: root cert is public, not a secret
				return fmt.Errorf("writing fetched root cert: %w", writeErr)
			}
			slog.Info("root CA certificate fetched and verified", "path", cfg.RootCert, "attempt", attempt)
			return nil
		}
		lastErr = err
		slog.Debug("waiting for step-ca root", "attempt", attempt, "of", caBootstrapRetries, "err", err)
		select {
		case <-ctx.Done():
			slog.Warn("root cert fetch aborted by context", "err", ctx.Err())
			return nil
		case <-time.After(caBootstrapInterval):
		}
	}
	slog.Warn("could not fetch root CA certificate after retries — continuing without it",
		"attempts", caBootstrapRetries, "lastErr", lastErr)
	return nil
}

// generateSelfSignedCert creates a 10-year self-signed EC P-256 certificate
// at certPath/keyPath with SAN IP:hostIP, DNS:localhost, and DNS:hostname
// when set — replacing entrypoint.sh's shelled
// `openssl req -x509 -nodes -days 3650 -newkey rsa:2048 ...`. The key
// algorithm changes from RSA-2048 to EC-P256: a self-signed dev/fallback
// cert, not attacker-facing in any meaningful way beyond what it already
// was — an intentional, low-risk deviation, not an oversight.
func generateSelfSignedCert(certPath, keyPath, hostname, hostIP string) error {
	return generateSelfSignedCertWithValidity(certPath, keyPath, hostname, hostIP, 10*365*24*time.Hour)
}

// generateSelfSignedCertWithValidity is generateSelfSignedCert with an
// explicit validity window — factored out so tests can produce short-lived
// fake certs (e.g. to exercise renewUICertOnce's next-sleep calculation)
// without waiting a decade.
func generateSelfSignedCertWithValidity(certPath, keyPath, hostname, hostIP string, validity time.Duration) error {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return fmt.Errorf("generating self-signed key: %w", err)
	}

	cn := hostname
	if cn == "" {
		cn = hostIP
	}
	if cn == "" {
		cn = "localhost"
	}

	dnsNames := []string{"localhost"}
	if hostname != "" {
		dnsNames = append(dnsNames, hostname)
	}
	var ips []net.IP
	if ip := net.ParseIP(hostIP); ip != nil {
		ips = append(ips, ip)
	}

	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return fmt.Errorf("generating serial number: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: cn},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(validity),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IsCA:                  true,
		DNSNames:              dnsNames,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return fmt.Errorf("creating self-signed certificate: %w", err)
	}
	keyDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		return fmt.Errorf("marshaling self-signed key: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(certPath), 0o750); err != nil { //nolint:gosec // G301: cfg.SSLCert's parent dir, not user input
		return fmt.Errorf("creating cert directory: %w", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("writing self-signed cert: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER})
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("writing self-signed key: %w", err)
	}
	slog.Info("generated self-signed TLS certificate", "certPath", certPath, "cn", cn)
	return nil
}

// resolveUIHostname returns cfg.UIHostname if set, else the OS-reported
// hostname, else "localhost" — approximating entrypoint.sh's
// `${UI_HOSTNAME:-$(hostname -f 2>/dev/null || hostname)}`.
func resolveUIHostname(cfg *config.Config) string {
	if cfg.UIHostname != "" {
		return cfg.UIHostname
	}
	if h, err := os.Hostname(); err == nil && h != "" {
		return h
	}
	return "localhost"
}

// ensureUICert implements the three-way UITLSMode switch entrypoint.sh used
// to perform: "provided" is a no-op (operator supplies the cert out of
// band); "stepca" retries issuance via caClient up to caBootstrapRetries
// times and falls back to generateSelfSignedCert on exhaustion; anything
// else (including the default "self-signed") calls generateSelfSignedCert
// only if cfg.SSLCert is absent.
func ensureUICert(ctx context.Context, cfg *config.Config, caClient stepca.CA) error {
	switch cfg.UITLSMode {
	case "provided":
		return nil

	case "stepca":
		hostname := resolveUIHostname(cfg)
		slog.Info("obtaining UI leaf certificate from step-ca", "hostname", hostname)
		var lastErr error
		for attempt := 1; attempt <= caBootstrapRetries; attempt++ {
			err := issueUICert(ctx, cfg, caClient, hostname)
			if err == nil {
				slog.Info("UI leaf certificate obtained", "hostname", hostname, "attempt", attempt)
				return nil
			}
			lastErr = err
			slog.Debug("waiting for step-ca", "attempt", attempt, "of", caBootstrapRetries, "err", lastErr)
			select {
			case <-ctx.Done():
				slog.Warn("UI cert issuance aborted by context — falling back to self-signed", "err", ctx.Err())
				return generateSelfSignedCert(cfg.SSLCert, cfg.SSLKey, cfg.UIHostname, cfg.HostIP)
			case <-time.After(caBootstrapInterval):
			}
		}
		slog.Warn("step-ca certificate issuance failed after retries — falling back to self-signed",
			"attempts", caBootstrapRetries, "lastErr", lastErr)
		return generateSelfSignedCert(cfg.SSLCert, cfg.SSLKey, cfg.UIHostname, cfg.HostIP)

	default: // "self-signed" and any unrecognized value
		if _, err := os.Stat(cfg.SSLCert); err == nil {
			return nil
		}
		return generateSelfSignedCert(cfg.SSLCert, cfg.SSLKey, cfg.UIHostname, cfg.HostIP)
	}
}

// issueUICert issues one UI leaf certificate via caClient and writes it to
// cfg.SSLCert/cfg.SSLKey — the single-issuance step shared by ensureUICert's
// retry loop and renewUICertOnce.
func issueUICert(ctx context.Context, cfg *config.Config, caClient stepca.CA, hostname string) error {
	certPEM, keyPEM, err := caClient.IssueCertificate(ctx, stepca.IssueRequest{
		Domain:       hostname,
		Duration:     uiIssueDuration,
		KeyType:      uiIssueKeyType,
		Provisioner:  cfg.Provisioner,
		PasswordFile: cfg.PasswordFile,
	})
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(cfg.SSLCert), 0o750); err != nil { //nolint:gosec // G301: cfg.SSLCert's parent dir, not user input
		return fmt.Errorf("creating ssl directory: %w", err)
	}
	if err := os.WriteFile(cfg.SSLCert, certPEM, 0o600); err != nil {
		return fmt.Errorf("writing UI certificate: %w", err)
	}
	if err := os.WriteFile(cfg.SSLKey, keyPEM, 0o600); err != nil {
		return fmt.Errorf("writing UI key: %w", err)
	}
	return nil
}

// renewUICertOnce performs a single renewal iteration: re-issues via
// caClient, rewrites cfg.SSLCert/cfg.SSLKey (picked up with zero downtime by
// tlsreload.go's certReloader), and returns a next-sleep duration of
// roughly 2/3 of the newly-issued cert's validity window.
func renewUICertOnce(ctx context.Context, cfg *config.Config, caClient stepca.CA) (time.Duration, error) {
	hostname := resolveUIHostname(cfg)
	if err := issueUICert(ctx, cfg, caClient, hostname); err != nil {
		return 0, fmt.Errorf("renewing UI certificate: %w", err)
	}

	raw, err := os.ReadFile(cfg.SSLCert) //nolint:gosec // G304: cfg.SSLCert is a config-derived path, not user input
	if err != nil {
		return 0, fmt.Errorf("reading renewed certificate: %w", err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return 0, fmt.Errorf("renewed certificate file has no PEM block")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return 0, fmt.Errorf("parsing renewed certificate: %w", err)
	}

	validity := cert.NotAfter.Sub(cert.NotBefore)
	nextSleep := validity * 2 / 3
	if nextSleep <= 0 {
		nextSleep = uiCertRenewFailureBackoff
	}
	return nextSleep, nil
}

// startUICertRenewer wraps an infinite renewUICertOnce loop in a panic-safe
// background goroutine, mirroring h.StartRenewer()'s shape
// (handlers/le_renewer.go:18-28) exactly: fire-and-forget, panic-safe, no
// explicit shutdown hook — the same lifecycle already accepted for the LE
// renewer and the temp-users-expiry ticker. Only called when
// cfg.UITLSMode == "stepca".
func startUICertRenewer(cfg *config.Config, caClient stepca.CA) {
	handlers.SafeGoExported("ui-cert-renewer", func() {
		slog.Info("UI cert auto-renewer started")
		for {
			nextSleep, err := renewUICertOnce(context.Background(), cfg, caClient)
			if err != nil {
				slog.Error("UI cert renewal failed — will retry", "err", err, "retryIn", uiCertRenewFailureBackoff)
				time.Sleep(uiCertRenewFailureBackoff)
				continue
			}
			slog.Info("UI cert renewed", "nextRenewalIn", nextSleep)
			time.Sleep(nextSleep)
		}
	})
}
