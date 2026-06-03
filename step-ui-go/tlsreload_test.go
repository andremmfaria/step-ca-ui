package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"testing"
	"time"
)

// generateSelfSignedCert writes a fresh ECDSA P-256 self-signed cert+key pair
// to certPath/keyPath.  Returns the DER serial so callers can distinguish certs.
func generateSelfSignedCert(t *testing.T, certPath, keyPath string) *big.Int {
	t.Helper()

	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey: %v", err)
	}

	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	tmpl := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate: %v", err)
	}

	//nolint:gosec // G304: certPath comes from t.TempDir() — operator-controlled test helper
	certF, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("create cert file: %v", err)
	}
	_ = pem.Encode(certF, &pem.Block{Type: "CERTIFICATE", Bytes: der})
	if err := certF.Close(); err != nil {
		t.Fatalf("close cert file: %v", err)
	}

	//nolint:gosec // G304: keyPath comes from t.TempDir() — operator-controlled test helper
	keyF, err := os.Create(keyPath)
	if err != nil {
		t.Fatalf("create key file: %v", err)
	}
	keyDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		t.Fatalf("marshal EC key: %v", err)
	}
	_ = pem.Encode(keyF, &pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})
	if err := keyF.Close(); err != nil {
		t.Fatalf("close key file: %v", err)
	}

	return serial
}

// certSerial extracts the serial from a loaded tls.Certificate for identity
// comparison without re-reading from disk.
func certSerial(t *testing.T, c *tls.Certificate) *big.Int {
	t.Helper()
	leaf, err := x509.ParseCertificate(c.Certificate[0])
	if err != nil {
		t.Fatalf("parse leaf cert: %v", err)
	}
	return leaf.SerialNumber
}

func TestCertReloader_InitialLoad(t *testing.T) {
	dir := t.TempDir()
	certPath := dir + "/server.crt"
	keyPath := dir + "/server.key"

	serial1 := generateSelfSignedCert(t, certPath, keyPath)

	r := newCertReloader(certPath, keyPath)
	got, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("GetCertificate: %v", err)
	}
	if got == nil {
		t.Fatal("GetCertificate returned nil cert")
	}
	if certSerial(t, got).Cmp(serial1) != 0 {
		t.Errorf("serial mismatch: got %v want %v", certSerial(t, got), serial1)
	}
}

func TestCertReloader_ReloadOnMtimeChange(t *testing.T) {
	dir := t.TempDir()
	certPath := dir + "/server.crt"
	keyPath := dir + "/server.key"

	serial1 := generateSelfSignedCert(t, certPath, keyPath)

	r := newCertReloader(certPath, keyPath)
	got1, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("initial GetCertificate: %v", err)
	}
	if certSerial(t, got1).Cmp(serial1) != 0 {
		t.Errorf("initial serial mismatch")
	}

	// Write a new cert with a different serial.
	serial2 := generateSelfSignedCert(t, certPath, keyPath)

	// Force mtime forward so the reloader sees a change even on coarse
	// (1-second resolution) filesystems.
	future := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(certPath, future, future); err != nil {
		t.Fatalf("Chtimes cert: %v", err)
	}
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("Chtimes key: %v", err)
	}

	got2, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("second GetCertificate: %v", err)
	}
	if certSerial(t, got2).Cmp(serial2) != 0 {
		t.Errorf("reloaded cert has wrong serial: got %v want %v",
			certSerial(t, got2), serial2)
	}
	if certSerial(t, got1).Cmp(certSerial(t, got2)) == 0 {
		t.Error("cert was not reloaded — serial unchanged")
	}
}

func TestCertReloader_CorruptedKeyReturnsCachedCert(t *testing.T) {
	dir := t.TempDir()
	certPath := dir + "/server.crt"
	keyPath := dir + "/server.key"

	generateSelfSignedCert(t, certPath, keyPath)

	r := newCertReloader(certPath, keyPath)
	_, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("initial load: %v", err)
	}

	// Corrupt the key file and bump its mtime so the reloader attempts reload.
	if err := os.WriteFile(keyPath, []byte("not-a-valid-key"), 0o600); err != nil {
		t.Fatalf("corrupt key: %v", err)
	}
	future := time.Now().Add(4 * time.Second)
	if err := os.Chtimes(keyPath, future, future); err != nil {
		t.Fatalf("Chtimes: %v", err)
	}

	// Must return last good cert, not an error.
	got, err := r.GetCertificate(nil)
	if err != nil {
		t.Fatalf("expected last-good-cert fallback, got error: %v", err)
	}
	if got == nil {
		t.Fatal("expected non-nil cert on corrupted key fallback")
	}
}

func TestCertReloader_MissingFileNoCache(t *testing.T) {
	r := newCertReloader("/nonexistent/server.crt", "/nonexistent/server.key")
	_, err := r.GetCertificate(nil)
	if err == nil {
		t.Fatal("expected error when cert file does not exist and no cache")
	}
}
