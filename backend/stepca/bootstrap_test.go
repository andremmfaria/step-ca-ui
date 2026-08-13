package stepca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/smallstep/certificates/api"
)

// newTestRootCert generates a self-signed cert to stand in for a CA root.
func newTestRootCert(t *testing.T) *x509.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create cert: %v", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatalf("parse cert: %v", err)
	}
	return cert
}

func fingerprintOf(cert *x509.Certificate) string {
	sum := sha256.Sum256(cert.Raw)
	return hex.EncodeToString(sum[:])
}

// TestFetchRootByFingerprint_Success confirms a matching fingerprint returns
// the PEM-encoded root certificate.
func TestFetchRootByFingerprint_Success(t *testing.T) {
	rootCert := newTestRootCert(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/root/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RootResponse{RootPEM: api.NewCertificate(rootCert)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	got, err := FetchRootByFingerprint(context.Background(), srv.URL, fingerprintOf(rootCert))
	if err != nil {
		t.Fatalf("FetchRootByFingerprint: %v", err)
	}
	block, _ := pem.Decode(got)
	if block == nil || block.Type != "CERTIFICATE" {
		t.Fatalf("expected a CERTIFICATE PEM block, got %q", got)
	}
	parsed, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		t.Fatalf("parse returned cert: %v", err)
	}
	if parsed.SerialNumber.Cmp(rootCert.SerialNumber) != 0 {
		t.Errorf("returned cert serial = %v, want %v", parsed.SerialNumber, rootCert.SerialNumber)
	}
}

// TestFetchRootByFingerprint_Mismatch confirms a wrong fingerprint is
// rejected — this is the trust-on-first-use verification the library
// performs before returning, per doc.go's re-confirmed R findings.
func TestFetchRootByFingerprint_Mismatch(t *testing.T) {
	rootCert := newTestRootCert(t)
	mux := http.NewServeMux()
	mux.HandleFunc("/root/", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RootResponse{RootPEM: api.NewCertificate(rootCert)})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	wrongFingerprint := "0000000000000000000000000000000000000000000000000000000000000000"
	if _, err := FetchRootByFingerprint(context.Background(), srv.URL, wrongFingerprint); err == nil {
		t.Fatal("expected an error for a mismatched fingerprint, got nil")
	}
}

// TestFetchRootByFingerprint_Unreachable confirms an unreachable CA returns
// an error rather than hanging or panicking.
func TestFetchRootByFingerprint_Unreachable(t *testing.T) {
	srv := httptest.NewServer(http.NewServeMux())
	srv.Close() // close immediately so the URL is guaranteed unreachable

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := FetchRootByFingerprint(ctx, srv.URL, "irrelevant"); err == nil {
		t.Fatal("expected an error for an unreachable CA, got nil")
	}
}
