package handlers

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

type backupManifest struct {
	Format     string            `json:"format"`
	CreatedAt  string            `json:"created_at"`
	Version    string            `json:"version"`
	BuildDate  string            `json:"build_date"`
	GitCommit  string            `json:"git_commit"`
	Hostname   string            `json:"hostname"`
	Database   map[string]string `json:"database"`
	Components []backupComponent `json:"components"`
	Warnings   []string          `json:"warnings,omitempty"`
	RestoreDoc string            `json:"restore_doc"`
}

type backupComponent struct {
	Name   string `json:"name"`
	Path   string `json:"path"`
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
	Status string `json:"status"`
	Detail string `json:"detail,omitempty"`
}

// AdminBackupGet renders the backup download page.
func (h *Handler) AdminBackupGet(w http.ResponseWriter, r *http.Request) {
	h.render(w, "admin_backup", h.base(w, r, "admin_backup"))
}

// AdminBackupDownload builds a backup bundle and streams it as a .tgz download.
func (h *Handler) AdminBackupDownload(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/admin/backup") {
		return
	}

	bundle, filename, err := h.buildBackupBundle(r.Context())
	if err != nil {
		h.flash(w, r, "err", "Failed to create backup: "+err.Error())
		http.Redirect(w, r, "/admin/backup", http.StatusSeeOther)
		return
	}
	defer func() { _ = os.RemoveAll(filepath.Dir(bundle)) }()

	w.Header().Set("Content-Type", "application/gzip")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="%s"`, filename))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	http.ServeFile(w, r, bundle)
}

func (h *Handler) buildBackupBundle(ctx context.Context) (string, string, error) {
	ts := time.Now().UTC().Format("20060102T150405Z")
	tmp, err := os.MkdirTemp("", "step-ui-backup-*")
	if err != nil {
		return "", "", err
	}

	manifest := backupManifest{
		Format:     "step-ca-ui-backup-v1",
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
		Version:    Version,
		BuildDate:  BuildDate,
		GitCommit:  GitCommit,
		Database:   h.backupDBInfo(),
		RestoreDoc: "README.md (Backup & restore section)",
	}
	if host, err := os.Hostname(); err == nil {
		manifest.Hostname = host
	}

	addFile := func(name, path, detail string) {
		c := backupComponent{Name: name, Path: filepath.Base(path), Status: "ok", Detail: detail}
		if size, sum, err := fileStatSHA256(path); err == nil {
			c.Size = size
			c.SHA256 = sum
		} else {
			c.Status = "error"
			c.Detail = err.Error()
			manifest.Warnings = append(manifest.Warnings, fmt.Sprintf("%s: %v", name, err))
		}
		manifest.Components = append(manifest.Components, c)
	}

	sqlPath := filepath.Join(tmp, "postgres-stepui.sql")
	if err := h.writePGDump(ctx, sqlPath); err != nil {
		manifest.Warnings = append(manifest.Warnings, "postgres dump failed: "+err.Error())
	} else {
		addFile("postgres", sqlPath, "pg_dump custom-free plain SQL")
	}

	for _, item := range []struct {
		name   string
		source string
		target string
	}{
		{"step-ca-data", "/home/step", "step-ca-data.tgz"},
		{"step-ui-data", "/opt/step-ui/data", "step-ui-data.tgz"},
		{"step-ui-certs", h.cfg.CertsDir, "step-ui-certs.tgz"},
		{"step-ui-uploads", h.cfg.UploadDir, "step-ui-uploads.tgz"},
	} {
		target := filepath.Join(tmp, item.target)
		if err := writeDirTGZ(item.source, target); err != nil {
			manifest.Warnings = append(manifest.Warnings, fmt.Sprintf("%s failed: %v", item.name, err))
			continue
		}
		addFile(item.name, target, item.source)
	}

	manifestPath := filepath.Join(tmp, "manifest.json")
	if err := writeManifest(manifestPath, &manifest); err != nil {
		return "", "", err
	}

	bundleName := fmt.Sprintf("step-ca-ui-backup-%s.tgz", ts)
	bundlePath := filepath.Join(tmp, bundleName)
	if err := writeBundleTGZ(bundlePath, []string{
		manifestPath,
		filepath.Join(tmp, "postgres-stepui.sql"),
		filepath.Join(tmp, "step-ca-data.tgz"),
		filepath.Join(tmp, "step-ui-data.tgz"),
		filepath.Join(tmp, "step-ui-certs.tgz"),
		filepath.Join(tmp, "step-ui-uploads.tgz"),
	}); err != nil {
		return "", "", err
	}
	return bundlePath, bundleName, nil
}

func (h *Handler) writePGDump(ctx context.Context, path string) error {
	info, err := parsePostgresURL(h.cfg.DatabaseURL)
	if err != nil {
		return err
	}

	// Write a temporary .pgpass file so the password is never visible in the
	// process environment (readable via /proc on Linux).  pg_dump honours
	// PGPASSFILE pointing at a 0600 file.
	pgpassFile, err := os.CreateTemp("", "pgpass-*")
	if err != nil {
		return fmt.Errorf("creating pgpass tempfile: %w", err)
	}
	pgpassPath := pgpassFile.Name()
	defer func() { _ = os.Remove(pgpassPath) }()

	if err := pgpassFile.Chmod(0o600); err != nil {
		_ = pgpassFile.Close()
		return fmt.Errorf("chmod pgpass: %w", err)
	}
	// Format: hostname:port:database:username:password
	pgpassLine := fmt.Sprintf("%s:%s:%s:%s:%s\n", info.host, info.port, info.db, info.user, info.password)
	if _, err := pgpassFile.WriteString(pgpassLine); err != nil {
		_ = pgpassFile.Close()
		return fmt.Errorf("writing pgpass: %w", err)
	}
	if err := pgpassFile.Close(); err != nil {
		return fmt.Errorf("closing pgpass: %w", err)
	}

	timeoutCtx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	args := []string{"-h", info.host, "-p", info.port, "-U", info.user, "-d", info.db, "--no-owner", "--no-privileges"}
	//nolint:gosec // G204: pg_dump is a known binary; args are DB connection params from config, not user input
	cmd := exec.CommandContext(timeoutCtx, "pg_dump", args...)
	// Pass PGPASSFILE rather than PGPASSWORD to keep the password off /proc.
	env := make([]string, 0, len(os.Environ())+1)
	for _, e := range os.Environ() {
		if !strings.HasPrefix(e, "PGPASSWORD=") {
			env = append(env, e)
		}
	}
	env = append(env, "PGPASSFILE="+pgpassPath)
	cmd.Env = env
	//nolint:gosec // G304: path is constructed from a validated admin-specified backup directory
	out, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	cmd.Stdout = out
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("pg_dump: %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
}

func (h *Handler) backupDBInfo() map[string]string {
	info, err := parsePostgresURL(h.cfg.DatabaseURL)
	if err != nil {
		return map[string]string{"error": err.Error()}
	}
	return map[string]string{"host": info.host, "port": info.port, "database": info.db, "user": info.user}
}

type postgresInfo struct {
	host     string
	port     string
	user     string
	password string
	db       string
}

func parsePostgresURL(raw string) (postgresInfo, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return postgresInfo{}, err
	}
	if u.Scheme != "postgres" && u.Scheme != "postgresql" {
		return postgresInfo{}, fmt.Errorf("unsupported database URL scheme: %s", u.Scheme)
	}
	host, port, err := net.SplitHostPort(u.Host)
	if err != nil {
		host = u.Hostname()
		port = u.Port()
	}
	if port == "" {
		port = "5432"
	}
	user := u.User.Username()
	password, _ := u.User.Password()
	db := strings.TrimPrefix(u.Path, "/")
	if host == "" || user == "" || db == "" {
		return postgresInfo{}, fmt.Errorf("database URL must include host, user and database")
	}
	return postgresInfo{host: host, port: port, user: user, password: password, db: db}, nil
}

func writeManifest(path string, manifest *backupManifest) error {
	//nolint:gosec // G304: path is within the validated backup tmp directory
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()
	enc := json.NewEncoder(f)
	enc.SetIndent("", "  ")
	return enc.Encode(manifest)
}

func fileStatSHA256(path string) (int64, string, error) {
	//nolint:gosec // G304: path comes from config-dir file listing (admin-controlled)
	f, err := os.Open(path)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	size, err := io.Copy(h, f)
	if err != nil {
		return 0, "", err
	}
	return size, hex.EncodeToString(h.Sum(nil)), nil
}

func writeDirTGZ(source, target string) error {
	info, err := os.Stat(source)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", source)
	}

	//nolint:gosec // G304: target is the output bundle path constructed from a validated temp directory
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	gw := gzip.NewWriter(out)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	return filepath.WalkDir(source, func(path string, d os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == source {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(source, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			rel += "/"
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		//nolint:gosec // G304: path comes from filepath.WalkDir of validated source directory
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(tw, f)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

func writeBundleTGZ(target string, files []string) error {
	//nolint:gosec // G304: target is the output path constructed from a validated temp directory
	out, err := os.Create(target)
	if err != nil {
		return err
	}
	defer func() { _ = out.Close() }()
	gw := gzip.NewWriter(out)
	defer func() { _ = gw.Close() }()
	tw := tar.NewWriter(gw)
	defer func() { _ = tw.Close() }()

	for _, path := range files {
		info, err := os.Stat(path)
		if err != nil || info.IsDir() {
			continue
		}
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = filepath.Base(path)
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		//nolint:gosec // G304: path comes from a caller-supplied file list validated before this call
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		if _, err := io.Copy(tw, f); err != nil {
			_ = f.Close()
			return err
		}
		_ = f.Close()
	}
	return nil
}
