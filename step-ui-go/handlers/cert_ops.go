package handlers

import (
	"context"
	"crypto/x509"
	"database/sql"
	"encoding/pem"
	"fmt"
	"io"
	"log/slog"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"step-ui/config"
	"step-ui/models"
	"step-ui/stepca"

	appdb "step-ui/db"
)

// certFromURL parses the chi "id" URL param, fetches the certificate from the
// DB, and writes an appropriate HTTP error if something goes wrong.  It returns
// (cert, true) on success or (nil, false) when the handler should return early.
func (h *Handler) certFromURL(w http.ResponseWriter, r *http.Request) (*models.Certificate, bool) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	c, err := appdb.GetCert(h.db, id)
	if err != nil {
		slog.Error("certFromURL: DB error", "id", id, "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return nil, false
	}
	return c, true
}

// leCertFromURL is the LE-certificate equivalent of certFromURL.
// Returns (cert, true) on success; on DB error it writes 500 and returns (nil, false).
// A missing cert returns (nil, true) — callers redirect to /le.
func (h *Handler) leCertFromURL(w http.ResponseWriter, r *http.Request) (*models.LECertificate, bool) {
	id, _ := strconv.Atoi(chi.URLParam(r, "id"))
	c, err := appdb.GetLECert(r.Context(), h.db, id)
	if err != nil {
		slog.Error("leCertFromURL: DB error", "id", id, "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return nil, false
	}
	return c, true
}

// IssuePolicy holds the resolved certificate template and duration for issuance.
type IssuePolicy struct {
	Template string
	Duration string
	KeyType  string
}

var issueTemplates = map[string]IssuePolicy{
	"server":   {Template: "server", Duration: "8760h", KeyType: "EC:P-256"},
	"internal": {Template: "internal", Duration: "87600h", KeyType: "EC:P-256"},
	"wildcard": {Template: "wildcard", Duration: "8760h", KeyType: "EC:P-256"},
	"client":   {Template: "client", Duration: "8760h", KeyType: "EC:P-256"},
}

var allowedIssueDurations = map[string]bool{
	"720h": true, "4380h": true, "8760h": true, "87600h": true,
}

var allowedIssueKeyTypes = map[string]bool{
	"EC:P-256": true, "EC:P-384": true, "RSA:2048": true, "RSA:4096": true,
}

func normalizeIssuePolicy(template, duration, keyType, domain string) (IssuePolicy, error) {
	template = strings.TrimSpace(strings.ToLower(template))
	if template == "" {
		template = "server"
	}
	policy, ok := issueTemplates[template]
	if !ok {
		return IssuePolicy{}, fmt.Errorf("unknown certificate template: %s", template)
	}
	if allowedIssueDurations[duration] {
		policy.Duration = duration
	}
	if allowedIssueKeyTypes[keyType] {
		policy.KeyType = keyType
	}
	if policy.Template == "wildcard" && !strings.HasPrefix(strings.TrimSpace(domain), "*.") {
		return IssuePolicy{}, fmt.Errorf("wildcard template requires domain like *.example.com")
	}
	return policy, nil
}

// checkDomainPolicy enforces ALLOWED_DOMAIN_SUFFIXES. validateIdentifier asks
// whether a name is well formed; this asks whether the operator has any
// authority over it (V6). An empty policy allows everything, which is what the
// application did before the key existed.
//
// Matching is on label boundaries, so "evil-example.com" does not satisfy
// "example.com", and a wildcard is judged by the name under its "*." prefix.
func checkDomainPolicy(domain string, suffixes []string) error {
	if len(suffixes) == 0 {
		return nil
	}
	name := strings.ToLower(strings.TrimSuffix(strings.TrimPrefix(strings.TrimSpace(domain), "*."), "."))
	for _, suffix := range suffixes {
		if name == suffix || strings.HasSuffix(name, "."+suffix) {
			return nil
		}
	}
	return fmt.Errorf("domain %q is not covered by ALLOWED_DOMAIN_SUFFIXES (%s)", domain, strings.Join(suffixes, ", "))
}

func issueCert(ctx context.Context, caClient stepca.CA, domain, certPath, keyPath, duration, keyType string, cfg *config.Config) error {
	// Validate domain before it reaches the CA library — the same guard that
	// previously protected against flag injection into the step CLI's argv.
	if err := validateIdentifier(domain); err != nil {
		return err
	}
	// Both issuance and renewal reach the CA through here, so the name policy
	// sits here rather than in normalizeIssuePolicy, which Renew never calls.
	if err := checkDomainPolicy(domain, cfg.AllowedDomainSuffixes); err != nil {
		return err
	}
	dur, err := time.ParseDuration(duration)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", duration, err)
	}
	// Renew reaches here with paths loaded from the database, so both are
	// re-checked against the managed directory before anything is written.
	safeCertPath, err := containedAbsPath(cfg.CertsDir, certPath)
	if err != nil {
		return fmt.Errorf("certificate path: %w", err)
	}
	safeKeyPath, err := containedAbsPath(cfg.CertsDir, keyPath)
	if err != nil {
		return fmt.Errorf("key path: %w", err)
	}
	certPEM, keyPEM, err := caClient.IssueCertificate(ctx, stepca.IssueRequest{
		Domain:       domain,
		Duration:     dur,
		KeyType:      keyType,
		Provisioner:  cfg.Provisioner,
		PasswordFile: cfg.PasswordFile,
	})
	if err != nil {
		return fmt.Errorf("issue certificate: %w", err)
	}
	//nolint:gosec // G703: containedAbsPath above proved the path is inside
	// cfg.CertsDir; gosec's taint analysis cannot see that sanitiser, so
	// TestIssueCert_PathOutsideCertsDir asserts the invariant instead.
	if err := os.WriteFile(safeCertPath, certPEM, 0o600); err != nil {
		return fmt.Errorf("write certificate file: %w", err)
	}
	//nolint:gosec // G703: see the certificate write above
	if err := os.WriteFile(safeKeyPath, keyPEM, 0o600); err != nil {
		return fmt.Errorf("write key file: %w", err)
	}
	return nil
}

// revokeStep revokes a certificate via the CA client and returns any error so
// callers can decide whether to mark the cert as revoked in the database.
// cfg is unused now that Client.Revoke carries its own cfg (RootCert) from
// construction, but kept in the signature for symmetry with issueCert and in
// case a future caller needs cfg-derived context here too.
func revokeStep(ctx context.Context, caClient stepca.CA, certPath, keyPath string, _ *config.Config) error {
	if err := caClient.Revoke(ctx, certPath, keyPath); err != nil {
		return fmt.Errorf("revoke certificate: %w", err)
	}
	return nil
}

func parseCertDates(certPath string) (issued, expires *time.Time, serial string, err error) {
	//nolint:gosec // G304: certPath is the stored DB path for a managed certificate — containedPath-checked on import
	data, err := os.ReadFile(certPath)
	if err != nil {
		return issued, expires, serial, err
	}
	block, _ := pem.Decode(data)
	if block == nil {
		err = fmt.Errorf("no PEM block found")
		return issued, expires, serial, err
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return issued, expires, serial, err
	}
	i := cert.NotBefore
	e := cert.NotAfter
	issued = &i
	expires = &e
	serial = cert.SerialNumber.String()
	return issued, expires, serial, err
}

func getCertKeyType(certPath string) string {
	//nolint:gosec // G304: certPath is the stored DB path for a managed certificate — containedPath-checked on import
	data, err := os.ReadFile(certPath)
	if err != nil {
		return ""
	}
	block, _ := pem.Decode(data)
	if block == nil {
		return ""
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return ""
	}
	switch cert.PublicKeyAlgorithm {
	case x509.ECDSA:
		return "EC"
	case x509.RSA:
		return "RSA"
	default:
		return "Unknown"
	}
}

func scanExistingCerts(certsDir string, d *sql.DB) []map[string]string {
	var found []map[string]string
	// best-effort filesystem scan; individual walk errors are handled inside the closure
	_ = filepath.WalkDir(certsDir, func(path string, de os.DirEntry, err error) error {
		if err != nil || de.IsDir() {
			return nil
		}
		if strings.HasSuffix(path, "certificate.crt") {
			dir := filepath.Dir(path)
			name := filepath.Base(dir)
			keyPath := filepath.Join(dir, "private.key")
			if _, e := os.Stat(keyPath); e != nil {
				keyPath = ""
			}
			// Check whether already in the database
			_, _, serial, e := parseCertDates(path)
			if e != nil || serial == "" {
				return nil
			}
			c, _ := appdb.GetCertBySerial(d, serial)
			if c == nil {
				found = append(found, map[string]string{
					"name": name, "cert_path": path, "key_path": keyPath,
				})
			}
		}
		return nil
	})
	return found
}

func saveUploadedFile(file multipart.File, dst string) error {
	//nolint:gosec // G304: dst is constructed from containedPath(cfg.UploadDir, safeName(name))
	f, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	_, err = io.Copy(f, file)
	return err
}

func trimStr(s string) string {
	return strings.TrimSpace(s)
}

func daysLeftVal(t *time.Time) int {
	if t == nil {
		return 999
	}
	return int(time.Until(*t).Hours() / 24)
}

// GetCertBySerial wrapper needed in db
var _ = (*models.Certificate)(nil)
