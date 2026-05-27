package handlers

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/ed25519"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"net"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	appdb "step-ui/db"
	"step-ui/models"
)

type CertDetail struct {
	ID              int
	Name            string
	Domain          string
	Status          string
	CertPath        string
	KeyPath         string
	Subject         string
	Issuer          string
	Serial          string
	SerialHex       string
	FingerprintSHA1 string
	Fingerprint256  string
	NotBefore       time.Time
	NotAfter        time.Time
	DaysLeft        int
	PublicKey       string
	Signature       string
	DNSNames        []string
	IPAddresses     []string
	KeyUsage        []string
	ExtKeyUsage     []string
}

type CertValidation struct {
	Name   string
	Status string
	Detail string
}

func (h *Handler) CertificateDetails(w http.ResponseWriter, r *http.Request) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	c, _ := appdb.GetCert(h.db, id)
	if c == nil {
		http.NotFound(w, r)
		return
	}

	detail, validations := h.buildCertDetail(c)
	data := h.base(w, r, "certs")
	data["Cert"] = c
	data["Detail"] = detail
	data["Validations"] = validations
	h.render(w, "certificate_detail", data)
}

func (h *Handler) buildCertDetail(c *models.Certificate) (*CertDetail, []CertValidation) {
	var checks []CertValidation
	add := func(name, status, detail string) {
		checks = append(checks, CertValidation{Name: name, Status: status, Detail: detail})
	}

	cert, err := readPEMCert(c.CertPath)
	if err != nil {
		add("Certificate file", "err", err.Error())
		return nil, checks
	}
	add("Certificate file", "ok", c.CertPath)

	now := time.Now()
	if now.Before(cert.NotBefore) {
		add("Validity window", "warn", "certificate is not valid yet")
	} else if now.After(cert.NotAfter) {
		add("Validity window", "err", "certificate is expired")
	} else if cert.NotAfter.Sub(now) <= 30*24*time.Hour {
		add("Validity window", "warn", fmt.Sprintf("expires in %d days", int(time.Until(cert.NotAfter).Hours()/24)))
	} else {
		add("Validity window", "ok", fmt.Sprintf("valid until %s", cert.NotAfter.Format("2006-01-02 15:04")))
	}

	if err := validateNameMatch(cert, c.Domain); err != nil {
		add("Hostname/IP match", "warn", err.Error())
	} else {
		add("Hostname/IP match", "ok", c.Domain+" matches certificate SAN/CN")
	}

	if c.KeyPath == "" {
		add("Private key pair", "warn", "private key path is empty")
	} else if err := validateKeyPair(cert, c.KeyPath); err != nil {
		add("Private key pair", "err", err.Error())
	} else {
		add("Private key pair", "ok", "private key matches certificate public key")
	}

	if err := h.validateCertificateChain(cert); err != nil {
		add("CA chain", "err", err.Error())
	} else {
		add("CA chain", "ok", "certificate verifies against intermediate/root CA")
	}

	sha1fp, sha256fp := certFingerprints(cert)
	detail := &CertDetail{
		ID:              c.ID,
		Name:            c.Name,
		Domain:          c.Domain,
		Status:          c.Status,
		CertPath:        c.CertPath,
		KeyPath:         c.KeyPath,
		Subject:         cert.Subject.String(),
		Issuer:          cert.Issuer.String(),
		Serial:          cert.SerialNumber.String(),
		SerialHex:       strings.ToUpper(cert.SerialNumber.Text(16)),
		FingerprintSHA1: sha1fp,
		Fingerprint256:  sha256fp,
		NotBefore:       cert.NotBefore,
		NotAfter:        cert.NotAfter,
		DaysLeft:        int(time.Until(cert.NotAfter).Hours() / 24),
		PublicKey:       publicKeyLabel(cert),
		Signature:       cert.SignatureAlgorithm.String(),
		DNSNames:        append([]string{}, cert.DNSNames...),
		IPAddresses:     ipStrings(cert.IPAddresses),
		KeyUsage:        keyUsageLabels(cert.KeyUsage),
		ExtKeyUsage:     extKeyUsageLabels(cert.ExtKeyUsage),
	}
	sort.Strings(detail.DNSNames)
	sort.Strings(detail.IPAddresses)
	return detail, checks
}

func validateNameMatch(cert *x509.Certificate, name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return fmt.Errorf("stored domain is empty")
	}
	if ip := net.ParseIP(name); ip != nil {
		if err := cert.VerifyHostname(ip.String()); err != nil {
			return fmt.Errorf("%s is not present in IP SANs", name)
		}
		return nil
	}
	if err := cert.VerifyHostname(name); err != nil {
		return fmt.Errorf("%s does not match DNS SAN/CN: %w", name, err)
	}
	return nil
}

func validateKeyPair(cert *x509.Certificate, keyPath string) error {
	raw, err := os.ReadFile(keyPath)
	if err != nil {
		return fmt.Errorf("%s is not readable: %w", keyPath, err)
	}
	block, _ := pem.Decode(raw)
	if block == nil {
		return fmt.Errorf("%s does not contain a PEM private key", keyPath)
	}
	key, err := parsePrivateKey(block.Bytes)
	if err != nil {
		return fmt.Errorf("%s is not a supported private key: %w", keyPath, err)
	}
	if !publicKeysEqual(cert.PublicKey, key.Public()) {
		return fmt.Errorf("private key does not match certificate public key")
	}
	return nil
}

func parsePrivateKey(der []byte) (crypto.Signer, error) {
	if key, err := x509.ParsePKCS8PrivateKey(der); err == nil {
		return signerFromKey(key)
	}
	if key, err := x509.ParseECPrivateKey(der); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(der); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("unknown private key format")
}

func signerFromKey(key interface{}) (crypto.Signer, error) {
	switch k := key.(type) {
	case *rsa.PrivateKey:
		return k, nil
	case *ecdsa.PrivateKey:
		return k, nil
	case ed25519.PrivateKey:
		return k, nil
	default:
		return nil, fmt.Errorf("unsupported private key type %T", key)
	}
}

func publicKeysEqual(a, b interface{}) bool {
	switch x := a.(type) {
	case *rsa.PublicKey:
		y, ok := b.(*rsa.PublicKey)
		return ok && x.E == y.E && x.N.Cmp(y.N) == 0
	case *ecdsa.PublicKey:
		y, ok := b.(*ecdsa.PublicKey)
		return ok && x.Curve == y.Curve && x.X.Cmp(y.X) == 0 && x.Y.Cmp(y.Y) == 0
	case ed25519.PublicKey:
		y, ok := b.(ed25519.PublicKey)
		return ok && string(x) == string(y)
	default:
		return false
	}
}

func (h *Handler) validateCertificateChain(cert *x509.Certificate) error {
	root, err := readPEMCert(h.cfg.RootCert)
	if err != nil {
		return err
	}
	intermediate, err := readPEMCert(h.intermediateCertPath())
	if err != nil {
		return err
	}
	roots := x509.NewCertPool()
	roots.AddCert(root)
	intermediates := x509.NewCertPool()
	intermediates.AddCert(intermediate)
	_, err = cert.Verify(x509.VerifyOptions{
		Roots:         roots,
		Intermediates: intermediates,
		CurrentTime:   time.Now(),
		KeyUsages:     []x509.ExtKeyUsage{x509.ExtKeyUsageAny},
	})
	return err
}

func certFingerprints(cert *x509.Certificate) (string, string) {
	sha1sum := x509SHA1(cert.Raw)
	sha256sum := x509SHA256(cert.Raw)
	return colonHex(sha1sum[:]), colonHex(sha256sum[:])
}

func x509SHA1(raw []byte) [20]byte {
	return sha1.Sum(raw)
}

func x509SHA256(raw []byte) [32]byte {
	return sha256.Sum256(raw)
}

func colonHex(raw []byte) string {
	s := strings.ToUpper(hex.EncodeToString(raw))
	var parts []string
	for i := 0; i < len(s); i += 2 {
		parts = append(parts, s[i:i+2])
	}
	return strings.Join(parts, ":")
}

func publicKeyLabel(cert *x509.Certificate) string {
	switch k := cert.PublicKey.(type) {
	case *rsa.PublicKey:
		return fmt.Sprintf("RSA %d bits", k.N.BitLen())
	case *ecdsa.PublicKey:
		return "ECDSA " + k.Curve.Params().Name
	case ed25519.PublicKey:
		return "Ed25519"
	default:
		return cert.PublicKeyAlgorithm.String()
	}
}

func ipStrings(ips []net.IP) []string {
	out := make([]string, 0, len(ips))
	for _, ip := range ips {
		out = append(out, ip.String())
	}
	return out
}

func keyUsageLabels(usage x509.KeyUsage) []string {
	items := []struct {
		bit   x509.KeyUsage
		label string
	}{
		{x509.KeyUsageDigitalSignature, "Digital Signature"},
		{x509.KeyUsageContentCommitment, "Content Commitment"},
		{x509.KeyUsageKeyEncipherment, "Key Encipherment"},
		{x509.KeyUsageDataEncipherment, "Data Encipherment"},
		{x509.KeyUsageKeyAgreement, "Key Agreement"},
		{x509.KeyUsageCertSign, "Cert Sign"},
		{x509.KeyUsageCRLSign, "CRL Sign"},
		{x509.KeyUsageEncipherOnly, "Encipher Only"},
		{x509.KeyUsageDecipherOnly, "Decipher Only"},
	}
	var out []string
	for _, item := range items {
		if usage&item.bit != 0 {
			out = append(out, item.label)
		}
	}
	return out
}

func extKeyUsageLabels(usages []x509.ExtKeyUsage) []string {
	labels := map[x509.ExtKeyUsage]string{
		x509.ExtKeyUsageAny:                            "Any",
		x509.ExtKeyUsageServerAuth:                     "Server Auth",
		x509.ExtKeyUsageClientAuth:                     "Client Auth",
		x509.ExtKeyUsageCodeSigning:                    "Code Signing",
		x509.ExtKeyUsageEmailProtection:                "Email Protection",
		x509.ExtKeyUsageIPSECEndSystem:                 "IPSec End System",
		x509.ExtKeyUsageIPSECTunnel:                    "IPSec Tunnel",
		x509.ExtKeyUsageIPSECUser:                      "IPSec User",
		x509.ExtKeyUsageTimeStamping:                   "Time Stamping",
		x509.ExtKeyUsageOCSPSigning:                    "OCSP Signing",
		x509.ExtKeyUsageMicrosoftServerGatedCrypto:     "Microsoft SGC",
		x509.ExtKeyUsageNetscapeServerGatedCrypto:      "Netscape SGC",
		x509.ExtKeyUsageMicrosoftCommercialCodeSigning: "Microsoft Commercial Code Signing",
		x509.ExtKeyUsageMicrosoftKernelCodeSigning:     "Microsoft Kernel Code Signing",
	}
	var out []string
	for _, usage := range usages {
		if label, ok := labels[usage]; ok {
			out = append(out, label)
		} else {
			out = append(out, fmt.Sprintf("Unknown (%d)", usage))
		}
	}
	return out
}
