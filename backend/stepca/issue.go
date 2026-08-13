package stepca

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/smallstep/certificates/api"
	"github.com/smallstep/certificates/ca"
)

// IssueRequest is the input to Client.IssueCertificate.
type IssueRequest struct {
	Domain       string
	Duration     time.Duration
	KeyType      string // "EC:P-256", "EC:P-384", "RSA:2048", "RSA:4096" — same vocabulary as cert_ops.go
	Provisioner  string
	PasswordFile string
}

// IssueCertificate mints a certificate for req.Domain, replicating what
// `step ca certificate <domain>` did: local key generation + CSR + direct
// api.SignRequest construction (v0.30.2 has no bring-your-own-key primitive
// at the ca.CreateSignRequest level — R4, doc.go), single-SAN issuance
// (subject == the one SAN, matching the CLI's default), and leaf+chain PEM
// bundling in the cert file so parseCertDates/getCertKeyType (which read
// only the first PEM block) keep working unchanged.
func (c *Client) IssueCertificate(ctx context.Context, req IssueRequest) (certPEM, keyPEM []byte, err error) {
	// R9: read the provisioner password fresh for this call only; never store
	// it or the resulting *ca.Provisioner on the Client, zero the buffer once
	// the token is minted.
	passwordRaw, err := os.ReadFile(req.PasswordFile)
	if err != nil {
		return nil, nil, fmt.Errorf("stepca: reading provisioner password file: %w", err)
	}
	password := []byte(strings.TrimSpace(string(passwordRaw)))
	defer zero(passwordRaw)
	defer zero(password)

	// kid is left empty: ca.NewProvisioner resolves the named provisioner's
	// key from the CA itself when kid == "" (loadProvisionerJWKByName in the
	// library source — see doc.go), so no separate Provisioners()-based kid
	// lookup is needed here.
	prov, err := ca.NewProvisioner(req.Provisioner, "", c.cfg.CAURL, password, ca.WithRootFile(c.cfg.RootCert))
	if err != nil {
		return nil, nil, fmt.Errorf("stepca: loading provisioner %q: %w", req.Provisioner, err)
	}
	ott, err := prov.Token(req.Domain, req.Domain)
	if err != nil {
		return nil, nil, fmt.Errorf("stepca: minting provisioner token: %w", err)
	}

	priv, err := generateKey(req.KeyType)
	if err != nil {
		return nil, nil, err
	}

	csrDER, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		Subject:  pkix.Name{CommonName: req.Domain},
		DNSNames: []string{req.Domain},
	}, priv)
	if err != nil {
		return nil, nil, fmt.Errorf("stepca: creating CSR: %w", err)
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil {
		return nil, nil, fmt.Errorf("stepca: parsing CSR: %w", err)
	}

	signReq := &api.SignRequest{
		CsrPEM:   api.NewCertificateRequest(csr),
		OTT:      ott,
		NotAfter: api.NewTimeDuration(time.Now().Add(req.Duration)),
	}

	cctx, cancel := withTimeout(ctx)
	defer cancel()
	signResp, err := c.ca.SignWithContext(cctx, signReq)
	if err != nil {
		return nil, nil, timeoutErr(cctx, fmt.Errorf("stepca: sign request: %w", err))
	}

	certPEM, err = bundleChainPEM(signResp.CertChainPEM)
	if err != nil {
		return nil, nil, err
	}
	keyPEM, err = marshalKeyPEM(priv)
	if err != nil {
		return nil, nil, err
	}
	return certPEM, keyPEM, nil
}

// generateKey creates a fresh private key for one of the four key types the
// UI has always offered (cert_ops.go's allowedIssueKeyTypes), replacing the
// CLI's --kty/--curve/--size flags.
func generateKey(keyType string) (any, error) {
	switch {
	case strings.HasPrefix(keyType, "EC:"):
		var curve elliptic.Curve
		switch strings.TrimPrefix(keyType, "EC:") {
		case "P-256":
			curve = elliptic.P256()
		case "P-384":
			curve = elliptic.P384()
		default:
			return nil, fmt.Errorf("stepca: unsupported EC curve in key type %q", keyType)
		}
		return ecdsa.GenerateKey(curve, rand.Reader)
	case strings.HasPrefix(keyType, "RSA:"):
		bits, convErr := strconv.Atoi(strings.TrimPrefix(keyType, "RSA:"))
		if convErr != nil {
			return nil, fmt.Errorf("stepca: invalid RSA key size in key type %q: %w", keyType, convErr)
		}
		return rsa.GenerateKey(rand.Reader, bits)
	default:
		return nil, fmt.Errorf("stepca: unsupported key type %q", keyType)
	}
}

// bundleChainPEM PEM-encodes the certificate chain in the order the CA
// returned it — leaf first, then any intermediates — matching what
// `step ca certificate` wrote into the cert file by default (confirmed
// against api/sign.go: certChainPEM[0] is the leaf, certChainPEM[1] the
// intermediate). Writing the leaf first is what keeps
// parseCertDates/getCertKeyType (which read only the first PEM block)
// working unchanged.
func bundleChainPEM(chain []api.Certificate) ([]byte, error) {
	if len(chain) == 0 {
		return nil, fmt.Errorf("stepca: sign response contained no certificates")
	}
	var out []byte
	for _, c := range chain {
		if c.Certificate == nil {
			continue
		}
		out = append(out, pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.Raw})...)
	}
	return out, nil
}

// marshalKeyPEM PKCS8-encodes priv, unencrypted, matching what
// tls.LoadX509KeyPair (and Client.Revoke's transport) need.
func marshalKeyPEM(priv any) ([]byte, error) {
	der, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return nil, fmt.Errorf("stepca: marshaling private key: %w", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), nil
}

// zero overwrites b in place so a secret does not linger in the heap any
// longer than necessary (R9).
func zero(b []byte) {
	for i := range b {
		b[i] = 0
	}
}
