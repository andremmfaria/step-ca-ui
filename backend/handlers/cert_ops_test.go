package handlers

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"step-ui/config"
	"step-ui/stepca"
)

// countingFakeCA wraps FakeCA to record whether IssueCertificate was
// actually invoked, so tests can assert validateIdentifier short-circuits
// before any CA call for a malicious domain.
type countingFakeCA struct {
	FakeCA
	issueCalls int
}

func (c *countingFakeCA) IssueCertificate(ctx context.Context, req stepca.IssueRequest) ([]byte, []byte, error) {
	c.issueCalls++
	return c.FakeCA.IssueCertificate(ctx, req)
}

// TestIssueCert_ValidDomain confirms a valid domain reaches the CA client and
// the returned cert/key PEM bytes are written to certPath/keyPath — the
// direct replacement for the deleted stepRunner-based TestIssueCert_ValidIdentifier.
func TestIssueCert_ValidDomain(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "certificate.crt")
	keyPath := filepath.Join(dir, "private.key")

	ca := &countingFakeCA{}
	ca.IssueResult.Cert = []byte("cert-bytes")
	ca.IssueResult.Key = []byte("key-bytes")

	err := issueCert(context.Background(), ca, "example.com", certPath, keyPath, "8760h", "EC:P-256", &config.Config{
		CertsDir:     dir,
		Provisioner:  "admin",
		PasswordFile: "/pw",
	})
	if err != nil {
		t.Fatalf("issueCert: %v", err)
	}
	if ca.issueCalls != 1 {
		t.Fatalf("expected exactly 1 IssueCertificate call, got %d", ca.issueCalls)
	}
	got, err := os.ReadFile(certPath) //nolint:gosec // G304: t.TempDir()-derived path
	if err != nil || string(got) != "cert-bytes" {
		t.Errorf("certPath contents = %q, %v; want %q, nil", got, err, "cert-bytes")
	}
	got, err = os.ReadFile(keyPath) //nolint:gosec // G304: t.TempDir()-derived path
	if err != nil || string(got) != "key-bytes" {
		t.Errorf("keyPath contents = %q, %v; want %q, nil", got, err, "key-bytes")
	}
}

// TestIssueCert_InvalidDomain confirms a malicious domain is rejected by
// validateIdentifier before the CA client is ever touched — the direct
// replacement for the deleted stepRunner-based TestIssueCert_InvalidDomain.
func TestIssueCert_InvalidDomain(t *testing.T) {
	ca := &countingFakeCA{}
	ca.IssueErr = errors.New("CA should never be called for an invalid domain")

	err := issueCert(context.Background(), ca, "--foo", "/tmp/cert.crt", "/tmp/key.key", "8760h", "EC:P-256", &config.Config{})
	if err == nil {
		t.Fatal("expected error for domain starting with '--'")
	}
	if errors.Is(err, ca.IssueErr) {
		t.Errorf("issueCert returned the CA's error — validateIdentifier did not short-circuit: %v", err)
	}
	if ca.issueCalls != 0 {
		t.Errorf("expected 0 IssueCertificate calls, got %d", ca.issueCalls)
	}
}

// TestIssueCert_PathOutsideCertsDir confirms a cert path that escapes
// cfg.CertsDir — a tampered or legacy database row on the Renew path — is
// refused before anything is written to disk.
func TestIssueCert_PathOutsideCertsDir(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "escape.crt")

	ca := &countingFakeCA{}
	ca.IssueResult.Cert = []byte("cert-bytes")
	ca.IssueResult.Key = []byte("key-bytes")

	err := issueCert(context.Background(), ca, "example.com", outside, filepath.Join(dir, "private.key"),
		"8760h", "EC:P-256", &config.Config{CertsDir: dir})
	if err == nil {
		t.Fatal("expected error for a certificate path outside CertsDir")
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Error("issueCert wrote outside CertsDir")
	}
}

// TestIssueCert_DomainOutsideNamePolicy confirms the suffix policy is enforced
// inside issueCert, which is what puts the renew path behind it too (V6).
func TestIssueCert_DomainOutsideNamePolicy(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{CertsDir: dir, AllowedDomainSuffixes: []string{"example.com"}}

	ca := &countingFakeCA{}
	ca.IssueResult.Cert = []byte("cert-bytes")
	ca.IssueResult.Key = []byte("key-bytes")

	certPath := filepath.Join(dir, "certificate.crt")
	keyPath := filepath.Join(dir, "private.key")

	if err := issueCert(context.Background(), ca, "evil-example.com", certPath, keyPath, "8760h", "EC:P-256", cfg); err == nil {
		t.Fatal("expected a policy error for a lookalike domain")
	}
	if ca.issueCalls != 0 {
		t.Errorf("expected 0 IssueCertificate calls, got %d", ca.issueCalls)
	}
	if err := issueCert(context.Background(), ca, "www.example.com", certPath, keyPath, "8760h", "EC:P-256", cfg); err != nil {
		t.Fatalf("covered domain: %v", err)
	}
	if ca.issueCalls != 1 {
		t.Errorf("covered domain: expected 1 IssueCertificate call, got %d", ca.issueCalls)
	}
}

// TestRevokeStep_PropagatesCAError confirms a FakeCA.RevokeErr is propagated
// by revokeStep — the direct replacement for the deleted stepRunner-based
// TestRevokeStep_RunnerError.
func TestRevokeStep_PropagatesCAError(t *testing.T) {
	wantErr := errors.New("revoke failed")
	ca := &FakeCA{RevokeErr: wantErr}

	err := revokeStep(context.Background(), ca, "/tmp/cert.crt", "/tmp/key.key", &config.Config{})
	if err == nil {
		t.Fatal("expected error from revokeStep")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("revokeStep error = %v; want it to wrap %v", err, wantErr)
	}
}
