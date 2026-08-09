package stepca

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"step-ui/config"

	"github.com/smallstep/certificates/api"
	"github.com/smallstep/certificates/authority/provisioner"
	stepjose "go.step.sm/crypto/jose"
)

// caFixture holds the CA signing key and provisioner credentials shared by
// every fake-CA test in this package (issue_test.go, revoke_test.go), so
// each test only has to register the HTTP routes it actually needs.
type caFixture struct {
	caCert          *x509.Certificate
	caKey           *ecdsa.PrivateKey
	provisionerName string
	password        []byte
	provPubJWK      *stepjose.JSONWebKey
	encryptedKey    string
}

// newCAFixture generates a self-signed CA signing key and an encrypted
// provisioner JWK (the same JWE format step-ca stores and decryptProvisionerJWK
// parses — see stepca/doc.go), without starting any server.
func newCAFixture(t *testing.T) *caFixture {
	t.Helper()

	const provisionerName = "admin"
	password := []byte("test-password") //nolint:gosec // G101: test-only fixture password

	// Provisioner signing key: decrypted client-side to mint OTTs.
	provPriv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate provisioner key: %v", err)
	}
	provPrivJWK := &stepjose.JSONWebKey{Key: provPriv, KeyID: "test-kid", Algorithm: "ES256", Use: "sig"}
	jwe, err := stepjose.EncryptJWK(provPrivJWK, password)
	if err != nil {
		t.Fatalf("encrypt provisioner JWK: %v", err)
	}
	encryptedKey, err := jwe.CompactSerialize()
	if err != nil {
		t.Fatalf("serialize encrypted provisioner JWK: %v", err)
	}
	provPubJWK := &stepjose.JSONWebKey{Key: provPriv.Public(), KeyID: "test-kid", Algorithm: "ES256", Use: "sig"}

	// CA signing key: issues leaf certs for whatever CSR is presented.
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate CA key: %v", err)
	}
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Root CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("create CA cert: %v", err)
	}
	caCert, err := x509.ParseCertificate(caDER)
	if err != nil {
		t.Fatalf("parse CA cert: %v", err)
	}

	return &caFixture{
		caCert:          caCert,
		caKey:           caKey,
		provisionerName: provisionerName,
		password:        password,
		provPubJWK:      provPubJWK,
		encryptedKey:    encryptedKey,
	}
}

// baseMux registers /health, /provisioners, and /sign — the three endpoints
// every fake-CA test needs regardless of what else it exercises.
func (f *caFixture) baseMux() *http.ServeMux {
	mux := http.NewServeMux()
	mux.HandleFunc("/health", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.HealthResponse{Status: "ok"})
	})
	mux.HandleFunc("/provisioners", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(&api.ProvisionersResponse{
			Provisioners: provisioner.List{&provisioner.JWK{
				Type:         "JWK",
				Name:         f.provisionerName,
				Key:          f.provPubJWK,
				EncryptedKey: f.encryptedKey,
			}},
		})
	})
	mux.HandleFunc("/sign", func(w http.ResponseWriter, r *http.Request) {
		var req api.SignRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		leafCert, err := f.issue(req.CsrPEM.CertificateRequest, req.NotAfter.RelativeTime(time.Now()))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SignResponse{
			ServerPEM:    api.NewCertificate(leafCert),
			CaPEM:        api.NewCertificate(f.caCert),
			CertChainPEM: []api.Certificate{api.NewCertificate(leafCert), api.NewCertificate(f.caCert)},
		})
	})
	return mux
}

// issue signs a leaf certificate for csr, valid until notAfter.
func (f *caFixture) issue(csr *x509.CertificateRequest, notAfter time.Time) (*x509.Certificate, error) {
	leafTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject:      csr.Subject,
		DNSNames:     csr.DNSNames,
		NotBefore:    time.Now().Add(-time.Minute),
		NotAfter:     notAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, leafTemplate, f.caCert, csr.PublicKey, f.caKey)
	if err != nil {
		return nil, err
	}
	return x509.ParseCertificate(leafDER)
}

// start launches mux behind an httptest.NewTLSServer and returns a
// *config.Config pointing at it — RootCert trusts the server's own TLS leaf
// (self-signed, so RootFingerprintWithContext's chain-of-one verifies), and
// PasswordFile/Provisioner match this fixture's encrypted provisioner key.
func (f *caFixture) start(t *testing.T, mux *http.ServeMux) *config.Config {
	t.Helper()

	// RequestClientCert (not Require) so /revoke and /renew can read
	// r.TLS.PeerCertificates when a caller presents an mTLS cert, without
	// breaking the plain client.NewClient calls (/health, /provisioners,
	// /sign) that never present one.
	srv := httptest.NewUnstartedServer(mux)
	srv.TLS = &tls.Config{ClientAuth: tls.RequestClientCert}
	srv.StartTLS()
	t.Cleanup(srv.Close)

	dir := t.TempDir()
	rootFile := filepath.Join(dir, "root.crt")
	rootPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: srv.Certificate().Raw})
	if err := os.WriteFile(rootFile, rootPEM, 0o600); err != nil {
		t.Fatalf("write root file: %v", err)
	}

	passwordFile := filepath.Join(dir, "password")
	if err := os.WriteFile(passwordFile, f.password, 0o600); err != nil {
		t.Fatalf("write password file: %v", err)
	}

	return &config.Config{
		CAURL:        srv.URL,
		RootCert:     rootFile,
		Provisioner:  f.provisionerName,
		PasswordFile: passwordFile,
	}
}

// newTestCA is the issue_test.go-only convenience wrapper: a fixture with
// just /health, /provisioners, and /sign, started immediately.
func newTestCA(t *testing.T) *config.Config {
	t.Helper()
	f := newCAFixture(t)
	return f.start(t, f.baseMux())
}

// TestIssueCertificate_AllKeyTypes covers the 4 key types the UI has always
// offered (cert_ops.go's allowedIssueKeyTypes) end-to-end against a fake CA,
// asserting the resulting cert/key pair parses and DNSNames matches exactly
// what was requested (R4's "what would have to be true for this to fail
// silently" concern).
func TestIssueCertificate_AllKeyTypes(t *testing.T) {
	for _, keyType := range []string{"EC:P-256", "EC:P-384", "RSA:2048", "RSA:4096"} {
		t.Run(keyType, func(t *testing.T) {
			cfg := newTestCA(t)
			client, err := New(cfg)
			if err != nil {
				t.Fatalf("New: %v", err)
			}

			const domain = "example.com"
			certPEM, keyPEM, err := client.IssueCertificate(t.Context(), IssueRequest{
				Domain:       domain,
				Duration:     time.Hour,
				KeyType:      keyType,
				Provisioner:  cfg.Provisioner,
				PasswordFile: cfg.PasswordFile,
			})
			if err != nil {
				t.Fatalf("IssueCertificate: %v", err)
			}

			pair, err := tls.X509KeyPair(certPEM, keyPEM)
			if err != nil {
				t.Fatalf("X509KeyPair: %v", err)
			}
			leaf, err := x509.ParseCertificate(pair.Certificate[0])
			if err != nil {
				t.Fatalf("ParseCertificate: %v", err)
			}
			if len(leaf.DNSNames) != 1 || leaf.DNSNames[0] != domain {
				t.Errorf("DNSNames = %v, want [%s]", leaf.DNSNames, domain)
			}
			if leaf.Subject.CommonName != domain {
				t.Errorf("CommonName = %q, want %q", leaf.Subject.CommonName, domain)
			}
		})
	}
}
