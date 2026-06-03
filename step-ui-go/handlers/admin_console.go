package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"strings"
	"time"

	"step-ui/config"
	appdb "step-ui/db"
)

const (
	adminConsoleTimeout = 8 * time.Second
	adminConsoleMaxOut  = 16 * 1024
)

// adminConsoleCommand describes a single allowlisted diagnostic command.
type adminConsoleCommand struct {
	ID          string
	Label       string
	Description string
	Name        string
	Args        []string
}

// adminConsoleResult carries the output of a completed command run.
type adminConsoleResult struct {
	CommandLine string
	Output      string
	ExitCode    int
	Duration    string
	TimedOut    bool
	Truncated   bool
	Success     bool
}

// pgIsReadyArgs parses a postgres DSN and returns the pg_isready flag list
// (-h, -p, -U, -d).  The password is deliberately excluded — pg_isready does
// not accept one and it must never appear on the command line.
// On a malformed or empty DSN the function returns safe defaults.
func pgIsReadyArgs(dsn string) []string {
	const (
		defaultHost   = "postgres"
		defaultPort   = "5432"
		defaultUser   = "stepui"
		defaultDBName = "stepui"
	)

	host, port, user, dbname := defaultHost, defaultPort, defaultUser, defaultDBName

	if dsn != "" {
		if u, err := url.Parse(dsn); err == nil && u.Host != "" {
			if h := u.Hostname(); h != "" {
				host = h
			}
			if p := u.Port(); p != "" {
				port = p
			}
			if u.User != nil {
				if n := u.User.Username(); n != "" {
					user = n
				}
			}
			// Path is "/dbname"; strip the leading slash.
			if p := strings.TrimPrefix(u.Path, "/"); p != "" {
				dbname = p
			}
		}
	}

	return []string{"-h", host, "-p", port, "-U", user, "-d", dbname}
}

// adminConsoleCommands returns the allowlist of diagnostic commands built from
// runtime config.  The user can only supply a command_id; the binary and all
// arguments are server-controlled.  This is the only place they are defined.
func adminConsoleCommands(cfg *config.Config) []adminConsoleCommand {
	return []adminConsoleCommand{
		{
			ID:          "system.date",
			Label:       "Date & time",
			Description: "Current time inside the step-ui container",
			Name:        "date",
		},
		{
			ID:          "system.hostname",
			Label:       "Hostname",
			Description: "Container hostname",
			Name:        "hostname",
		},
		{
			ID:          "system.identity",
			Label:       "Current user",
			Description: "UID/GID of the application process",
			Name:        "id",
		},
		{
			ID:          "system.disk",
			Label:       "Disk usage",
			Description: "Free space for application and CA directories",
			Name:        "df",
			Args:        []string{"-h", "/opt/step-ui", "/home/step"},
		},
		{
			ID:          "system.processes",
			Label:       "Processes",
			Description: "Process list inside the container",
			Name:        "ps",
		},
		{
			ID:          "app.files",
			Label:       "Application directory",
			Description: "Top-level listing of /opt/step-ui",
			Name:        "ls",
			Args:        []string{"-la", "/opt/step-ui"},
		},
		{
			ID:          "step.version",
			Label:       "step version",
			Description: "Smallstep CLI version inside the container",
			Name:        "step",
			Args:        []string{"version"},
		},
		{
			ID:          "step.ca.health",
			Label:       "step-ca health",
			Description: "Reachability check for the CA from the UI container",
			Name:        "step",
			Args:        []string{"ca", "health", "--ca-url", cfg.CAURL, "--root", cfg.RootCert},
		},
		{
			ID:          "openssl.version",
			Label:       "OpenSSL version",
			Description: "OpenSSL build information",
			Name:        "openssl",
			Args:        []string{"version", "-a"},
		},
		{
			ID:          "postgres.ready",
			Label:       "PostgreSQL readiness",
			Description: "Reachability check for the PostgreSQL service",
			Name:        "pg_isready",
			Args:        pgIsReadyArgs(cfg.DatabaseURL),
		},
	}
}

// findAdminConsoleCommand looks up a command by its ID in the allowlist.
// Returns the command and true on a hit; zero value and false on a miss.
func findAdminConsoleCommand(cfg *config.Config, id string) (adminConsoleCommand, bool) {
	for _, c := range adminConsoleCommands(cfg) {
		if c.ID == id {
			return c, true
		}
	}

	return adminConsoleCommand{}, false
}

// AdminConsoleGet renders the diagnostics console form.
func (h *Handler) AdminConsoleGet(w http.ResponseWriter, r *http.Request) {
	h.render(w, "admin_console", h.adminConsolePageData(w, r, "", nil))
}

// AdminConsolePost runs the selected allowlisted command and renders the result.
func (h *Handler) AdminConsolePost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/admin/console") {
		return
	}

	commandID := strings.TrimSpace(r.FormValue("command_id"))

	c, ok := findAdminConsoleCommand(h.cfg, commandID)
	if !ok {
		h.auditSecurity(r, "console.denied command_id="+commandID)
		data := h.adminConsolePageData(w, r, commandID, nil)
		data["ConsoleError"] = "Unknown command. Only allowlisted commands may be run."
		h.render(w, "admin_console", data)

		return
	}

	result := runAdminConsoleCommand(r.Context(), &c)
	h.auditSecurity(r, fmt.Sprintf(
		"console.run id=%s command=%q exit=%d timeout=%t duration=%s",
		c.ID, result.CommandLine, result.ExitCode, result.TimedOut, result.Duration,
	))

	h.render(w, "admin_console", h.adminConsolePageData(w, r, commandID, &result))
}

// adminConsolePageData builds the template data map for the console page.
func (h *Handler) adminConsolePageData(
	w http.ResponseWriter,
	r *http.Request,
	selectedID string,
	result *adminConsoleResult,
) map[string]interface{} {
	data := h.base(w, r, "admin_console")
	data["Commands"] = adminConsoleCommands(h.cfg)
	data["Timeout"] = adminConsoleTimeout.String()
	data["MaxOutputKB"] = adminConsoleMaxOut / 1024
	data["SelectedCommandID"] = selectedID

	if result != nil {
		data["Result"] = result
	}

	si := h.sessionInfo(r)
	if u, err := appdb.GetUserByID(h.db, si.UserID); err == nil && u != nil {
		data["TOTPEnabled"] = u.TOTPEnabled
	}

	return data
}

// runAdminConsoleCommand executes a single allowlisted command under a fixed
// timeout and returns its combined output capped at adminConsoleMaxOut bytes.
func runAdminConsoleCommand(ctx context.Context, c *adminConsoleCommand) adminConsoleResult {
	cctx, cancel := context.WithTimeout(ctx, adminConsoleTimeout)
	defer cancel()

	start := time.Now()
	//nolint:gosec // command name+args come from a fixed server-side allowlist; user only supplies an id
	cmd := exec.CommandContext(cctx, c.Name, c.Args...)
	cmd.Dir = "/opt/step-ui"

	out, err := cmd.CombinedOutput()
	duration := time.Since(start).Round(time.Millisecond)

	exitCode := 0
	if err != nil {
		exitCode = 1

		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}
	}

	timedOut := cctx.Err() == context.DeadlineExceeded
	if timedOut {
		exitCode = -1
	}

	truncated := false
	if len(out) > adminConsoleMaxOut {
		out = append(out[:adminConsoleMaxOut], []byte("\n\n[output truncated]\n")...)
		truncated = true
	}

	text := strings.TrimRight(string(bytes.ToValidUTF8(out, []byte("?"))), "\r\n")
	if text == "" && err != nil {
		text = err.Error()
	}

	if timedOut {
		text = strings.TrimSpace(text + "\ncommand timed out")
	}

	return adminConsoleResult{
		CommandLine: adminCommandLine(c),
		Output:      text,
		ExitCode:    exitCode,
		Duration:    duration.String(),
		TimedOut:    timedOut,
		Truncated:   truncated,
		Success:     err == nil && !timedOut,
	}
}

// adminCommandLine formats c as the shell string shown in the result UI.
func adminCommandLine(c *adminConsoleCommand) string {
	parts := append([]string{c.Name}, c.Args...)
	return strings.Join(parts, " ")
}
