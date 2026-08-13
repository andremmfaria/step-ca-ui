package stepca

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/http"
	"os"

	"github.com/smallstep/certificates/api"
)

// Revoke revokes the certificate at certPath/keyPath, authenticating via the
// leaf cert/key itself (mTLS) — the same auth mode `step ca revoke --cert
// --key` used (no provisioner/OTT). Per R7 (verified against v0.30.2
// source): the server's mTLS revoke path requires req.Serial to exactly
// match the presented peer certificate's serial (else 403), and
// req.Passive == true (non-passive revocation returns NotImplemented) — a
// zero-value api.RevokeRequest fails at runtime, so both fields are set
// explicitly here.
func (c *Client) Revoke(ctx context.Context, certPath, keyPath string) error {
	pair, err := tls.LoadX509KeyPair(certPath, keyPath)
	if err != nil {
		return fmt.Errorf("stepca: loading cert/key pair: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return fmt.Errorf("stepca: parsing leaf certificate: %w", err)
	}

	pool := x509.NewCertPool()
	if rootPEM, rootErr := os.ReadFile(c.cfg.RootCert); rootErr == nil {
		pool.AppendCertsFromPEM(rootPEM)
	}
	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			MinVersion:   tls.VersionTLS12,
			Certificates: []tls.Certificate{pair},
			RootCAs:      pool,
		},
	}

	req := &api.RevokeRequest{
		Serial:  leaf.SerialNumber.String(),
		Passive: true,
	}

	cctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := c.ca.RevokeWithContext(cctx, req, transport); err != nil {
		return timeoutErr(cctx, fmt.Errorf("stepca: revoke request: %w", err))
	}
	return nil
}
