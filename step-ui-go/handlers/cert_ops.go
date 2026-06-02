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
	c, err := appdb.GetLECert(h.db, id)
	if err != nil {
		slog.Error("leCertFromURL: DB error", "id", id, "err", err)
		http.Error(w, "Database error", http.StatusInternalServerError)
		return nil, false
	}
	return c, true
}

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

func issueCert(ctx context.Context, domain, certPath, keyPath, duration, keyType string, cfg *config.Config) error {
	extraFlags := []string{
		"--provisioner", cfg.Provisioner,
		"--provisioner-password-file", cfg.PasswordFile,
		"--not-after", duration,
		"--force",
	}
	if strings.HasPrefix(keyType, "EC:") {
		extraFlags = append(extraFlags, "--kty", "EC", "--curve", strings.TrimPrefix(keyType, "EC:"))
	} else if strings.HasPrefix(keyType, "RSA:") {
		extraFlags = append(extraFlags, "--kty", "RSA", "--size", strings.TrimPrefix(keyType, "RSA:"))
	}
	// Validate domain before passing to the shell.  certPath and keyPath are
	// server-generated paths and do not need hostname validation.
	// We include the "--" separator so step cannot interpret domain as a flag.
	if err := validateIdentifier(domain); err != nil {
		return err
	}
	extraFlags = append(extraFlags, "--", domain, certPath, keyPath)
	out, err := runStep(ctx, cfg, execRunner, []string{"ca", "certificate"}, extraFlags, nil)
	if err != nil {
		return fmt.Errorf("%w: %s", err, string(out))
	}
	return nil
}

// revokeStep revokes a certificate via the step CLI and returns any error so
// callers can decide whether to mark the cert as revoked in the database.
func revokeStep(ctx context.Context, certPath, keyPath string, cfg *config.Config) error {
	out, err := runStep(
		ctx, cfg, execRunner,
		[]string{"ca", "revoke"},
		[]string{"--cert", certPath, "--key", keyPath},
		nil,
	)
	if err != nil {
		return fmt.Errorf("step ca revoke: %w: %s", err, string(out))
	}
	return nil
}

func parseCertDates(certPath string) (issued, expires *time.Time, serial string, err error) {
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
			// Проверяем не в базе ли уже
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
