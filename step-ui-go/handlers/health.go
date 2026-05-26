package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

type HealthCheck struct {
	Name     string
	Status   string
	Detail   string
	Critical bool
}

type HealthSummary struct {
	OK       int
	Warnings int
	Errors   int
}

type SystemInfo struct {
	Version       string
	BuildDate     string
	GitCommit     string
	StartedAt     time.Time
	Uptime        string
	CAURL         string
	RootCert      string
	Provisioner   string
	PasswordFile  string
	CertsDir      string
	UploadDir     string
	SSLCert       string
	SSLKey        string
	SessionSecure bool
}

func (h *Handler) systemInfo() SystemInfo {
	return SystemInfo{
		Version:       Version,
		BuildDate:     BuildDate,
		GitCommit:     GitCommit,
		StartedAt:     StartedAt,
		Uptime:        fmtUptime(time.Since(StartedAt)),
		CAURL:         h.cfg.CAURL,
		RootCert:      h.cfg.RootCert,
		Provisioner:   h.cfg.Provisioner,
		PasswordFile:  h.cfg.PasswordFile,
		CertsDir:      h.cfg.CertsDir,
		UploadDir:     h.cfg.UploadDir,
		SSLCert:       h.cfg.SSLCert,
		SSLKey:        h.cfg.SSLKey,
		SessionSecure: h.cfg.SessionSecure,
	}
}

func (h *Handler) preflight(ctx context.Context) ([]HealthCheck, HealthSummary) {
	var checks []HealthCheck
	add := func(name, status, detail string, critical bool) {
		checks = append(checks, HealthCheck{Name: name, Status: status, Detail: detail, Critical: critical})
	}

	if err := h.db.PingContext(ctx); err != nil {
		add("PostgreSQL", "err", err.Error(), true)
	} else {
		add("PostgreSQL", "ok", "database connection is alive", true)
	}

	if out, err := runCheck(ctx, 5*time.Second, "step", "ca", "health", "--ca-url", h.cfg.CAURL, "--root", h.cfg.RootCert); err != nil {
		add("Step-CA API", "err", cleanCheckOutput(out, err), true)
	} else {
		add("Step-CA API", "ok", "CA health endpoint is reachable", true)
	}

	h.checkFile(&checks, "Root CA certificate", h.cfg.RootCert, true)
	h.checkFile(&checks, "Intermediate CA certificate", h.intermediateCertPath(), true)
	h.checkFile(&checks, "Provisioner password file", h.cfg.PasswordFile, true)
	h.checkFile(&checks, "UI TLS certificate", h.cfg.SSLCert, false)
	h.checkFile(&checks, "UI TLS private key", h.cfg.SSLKey, false)
	h.checkDir(&checks, "Issued certificates directory", h.cfg.CertsDir, true)
	h.checkDir(&checks, "Upload directory", h.cfg.UploadDir, false)

	h.checkCAConfig(&checks)
	h.checkDisk(&checks, h.cfg.CertsDir)
	h.checkDisk(&checks, filepath.Dir(h.cfg.RootCert))
	h.checkDisk(&checks, filepath.Dir(h.cfg.PasswordFile))

	if h.cfg.SessionSecure {
		add("Session cookie", "ok", "SESSION_SECURE=true", true)
	} else {
		add("Session cookie", "warn", "SESSION_SECURE=false; use only for local HTTP development", true)
	}

	summary := summarizeHealth(checks)
	return checks, summary
}

func (h *Handler) checkFile(checks *[]HealthCheck, name, path string, critical bool) {
	info, err := os.Stat(path)
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: name, Status: "err", Detail: path + " is not readable: " + err.Error(), Critical: critical})
		return
	}
	if info.IsDir() {
		*checks = append(*checks, HealthCheck{Name: name, Status: "err", Detail: path + " is a directory", Critical: critical})
		return
	}
	if info.Size() == 0 {
		*checks = append(*checks, HealthCheck{Name: name, Status: "err", Detail: path + " is empty", Critical: critical})
		return
	}
	*checks = append(*checks, HealthCheck{Name: name, Status: "ok", Detail: fmt.Sprintf("%s (%d bytes)", path, info.Size()), Critical: critical})
}

func (h *Handler) checkDir(checks *[]HealthCheck, name, path string, critical bool) {
	info, err := os.Stat(path)
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: name, Status: "err", Detail: path + " is not readable: " + err.Error(), Critical: critical})
		return
	}
	if !info.IsDir() {
		*checks = append(*checks, HealthCheck{Name: name, Status: "err", Detail: path + " is not a directory", Critical: critical})
		return
	}
	f, err := os.CreateTemp(path, ".preflight-*")
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: name, Status: "warn", Detail: path + " is readable but not writable: " + err.Error(), Critical: critical})
		return
	}
	tmp := f.Name()
	f.Close()
	os.Remove(tmp)
	*checks = append(*checks, HealthCheck{Name: name, Status: "ok", Detail: path + " is writable", Critical: critical})
}

func (h *Handler) checkCAConfig(checks *[]HealthCheck) {
	caConfig := filepath.Join(filepath.Dir(filepath.Dir(h.cfg.RootCert)), "config", "ca.json")
	raw, err := os.ReadFile(caConfig)
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: "CA config", Status: "warn", Detail: caConfig + " is not readable: " + err.Error(), Critical: false})
		return
	}

	var cfg struct {
		Authority struct {
			Provisioners []struct {
				Name   string                 `json:"name"`
				Type   string                 `json:"type"`
				Claims map[string]interface{} `json:"claims"`
			} `json:"provisioners"`
		} `json:"authority"`
	}
	if err := json.Unmarshal(raw, &cfg); err != nil {
		*checks = append(*checks, HealthCheck{Name: "CA config", Status: "warn", Detail: "cannot parse ca.json: " + err.Error(), Critical: false})
		return
	}

	for _, p := range cfg.Authority.Provisioners {
		if p.Name != h.cfg.Provisioner {
			continue
		}
		*checks = append(*checks, HealthCheck{Name: "Provisioner", Status: "ok", Detail: fmt.Sprintf("%s (%s)", p.Name, p.Type), Critical: true})
		h.checkDurationClaim(checks, "Default TLS duration", claimString(p.Claims, "defaultTLSCertDuration"), 8760*time.Hour, false)
		h.checkDurationClaim(checks, "Max TLS duration", claimString(p.Claims, "maxTLSCertDuration"), 87600*time.Hour, true)
		return
	}

	*checks = append(*checks, HealthCheck{Name: "Provisioner", Status: "err", Detail: "provisioner " + h.cfg.Provisioner + " not found in ca.json", Critical: true})
}

func claimString(claims map[string]interface{}, key string) string {
	if claims == nil {
		return ""
	}
	v, ok := claims[key]
	if !ok || v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	default:
		return fmt.Sprint(x)
	}
}

func (h *Handler) checkDurationClaim(checks *[]HealthCheck, name, value string, min time.Duration, critical bool) {
	if value == "" {
		*checks = append(*checks, HealthCheck{Name: name, Status: "warn", Detail: "claim is not set", Critical: critical})
		return
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: name, Status: "warn", Detail: value + " is not parseable: " + err.Error(), Critical: critical})
		return
	}
	if d < min {
		*checks = append(*checks, HealthCheck{Name: name, Status: "warn", Detail: fmt.Sprintf("%s is below expected %s", value, min), Critical: critical})
		return
	}
	*checks = append(*checks, HealthCheck{Name: name, Status: "ok", Detail: value, Critical: critical})
}

func (h *Handler) checkDisk(checks *[]HealthCheck, path string) {
	if path == "" {
		return
	}
	out, err := runCheck(context.Background(), 3*time.Second, "df", "-Pk", path)
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: "Disk space", Status: "warn", Detail: path + ": df unavailable", Critical: false})
		return
	}
	lines := strings.Fields(string(out))
	if len(lines) < 11 {
		*checks = append(*checks, HealthCheck{Name: "Disk space", Status: "warn", Detail: path + ": cannot parse df output", Critical: false})
		return
	}
	availableKB, err := strconv.ParseInt(lines[len(lines)-3], 10, 64)
	if err != nil {
		*checks = append(*checks, HealthCheck{Name: "Disk space", Status: "warn", Detail: path + ": cannot parse available space", Critical: false})
		return
	}
	availableMB := availableKB / 1024
	status := "ok"
	if availableMB < 256 {
		status = "err"
	} else if availableMB < 1024 {
		status = "warn"
	}
	*checks = append(*checks, HealthCheck{Name: "Disk space", Status: status, Detail: fmt.Sprintf("%s: %d MB available", path, availableMB), Critical: status == "err"})
}

func summarizeHealth(checks []HealthCheck) HealthSummary {
	var s HealthSummary
	for _, c := range checks {
		switch c.Status {
		case "ok":
			s.OK++
		case "warn":
			s.Warnings++
		case "err":
			s.Errors++
		}
	}
	return s
}

func runCheck(ctx context.Context, timeout time.Duration, name string, args ...string) ([]byte, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(cctx, name, args...)
	out, err := cmd.CombinedOutput()
	if cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("timeout after %s", timeout)
	}
	return out, err
}

func cleanCheckOutput(out []byte, err error) string {
	text := strings.TrimSpace(string(out))
	if text == "" {
		return err.Error()
	}
	return text
}
