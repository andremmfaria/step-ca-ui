package stepca

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/smallstep/certificates/api"
)

// TestRevoke_RejectsSubsequentUse is R7's mitigation test: issuing a cert,
// revoking it, and then attempting to use it again (renew) must fail — a
// Go-level "no error" from Revoke() alone does not prove the CA actually
// marked the certificate revoked (see plan's "what would have to be true for
// this to fail silently").
//
// The fake CA's /revoke handler enforces the same two server-side rules R7
// found in the v0.30.2 source: req.Serial must match the mTLS peer
// certificate's serial, and req.Passive must be true — a wrong
// Client.Revoke implementation (e.g. a zero-value RevokeRequest, or the
// wrong transport) is rejected here exactly as it would be by a real CA.
func TestRevoke_RejectsSubsequentUse(t *testing.T) {
	f := newCAFixture(t)
	mux := f.baseMux()

	var mu sync.Mutex
	revoked := map[string]bool{}

	mux.HandleFunc("/revoke", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "no peer certificate presented", http.StatusUnauthorized)
			return
		}
		peerSerial := r.TLS.PeerCertificates[0].SerialNumber.String()

		var req api.RevokeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if req.Serial != peerSerial {
			http.Error(w, "serial does not match peer certificate", http.StatusForbidden)
			return
		}
		if !req.Passive {
			http.Error(w, "non-passive revocation not implemented", http.StatusNotImplemented)
			return
		}

		mu.Lock()
		revoked[peerSerial] = true
		mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.RevokeResponse{Status: "ok"})
	})

	mux.HandleFunc("/renew", func(w http.ResponseWriter, r *http.Request) {
		if r.TLS == nil || len(r.TLS.PeerCertificates) == 0 {
			http.Error(w, "no peer certificate presented", http.StatusUnauthorized)
			return
		}
		peerSerial := r.TLS.PeerCertificates[0].SerialNumber.String()

		mu.Lock()
		isRevoked := revoked[peerSerial]
		mu.Unlock()
		if isRevoked {
			http.Error(w, "certificate has been revoked", http.StatusUnauthorized)
			return
		}

		leaf, err := f.issue(&x509.CertificateRequest{Subject: r.TLS.PeerCertificates[0].Subject, DNSNames: r.TLS.PeerCertificates[0].DNSNames}, time.Now().Add(time.Hour))
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(api.SignResponse{
			ServerPEM: api.NewCertificate(leaf),
			CaPEM:     api.NewCertificate(f.caCert),
		})
	})

	cfg := f.start(t, mux)
	client, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	const domain = "revoke-me.example.com"
	certPEM, keyPEM, err := client.IssueCertificate(t.Context(), IssueRequest{
		Domain:       domain,
		Duration:     time.Hour,
		KeyType:      "EC:P-256",
		Provisioner:  cfg.Provisioner,
		PasswordFile: cfg.PasswordFile,
	})
	if err != nil {
		t.Fatalf("IssueCertificate: %v", err)
	}

	dir := t.TempDir()
	certPath := filepath.Join(dir, "certificate.crt")
	keyPath := filepath.Join(dir, "private.key")
	if err := os.WriteFile(certPath, certPEM, 0o600); err != nil {
		t.Fatalf("write cert: %v", err)
	}
	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		t.Fatalf("write key: %v", err)
	}

	if err := client.Revoke(t.Context(), certPath, keyPath); err != nil {
		t.Fatalf("Revoke: %v", err)
	}

	// Attempt to use the revoked cert again (renew, mTLS auth) and assert
	// rejection — the actual proof the revocation took effect server-side.
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		t.Fatalf("LoadX509KeyPair: %v", err)
	}
	rootPEM, err := os.ReadFile(cfg.RootCert)
	if err != nil {
		t.Fatalf("read root cert: %v", err)
	}
	pool := x509.NewCertPool()
	pool.AppendCertsFromPEM(rootPEM)
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			Certificates: []tls.Certificate{pair},
			RootCAs:      pool,
		},
	}

	renewClient, err := New(cfg)
	if err != nil {
		t.Fatalf("New (renew client): %v", err)
	}
	// Package-internal access to the wrapped *ca.Client — this test lives in
	// package stepca specifically to reach Renew, which stepca.CA does not
	// expose (renewal isn't in this migration's scope; see plan's Out of Scope).
	if _, err := renewClient.ca.RenewWithContext(t.Context(), transport); err == nil {
		t.Error("expected renewal of a revoked certificate to fail, got nil error")
	}
}
