package handlers

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os/exec"
	"runtime/debug"
	"strings"
	"time"

	"step-ui/config"
	"step-ui/stepca"

	appdb "step-ui/db"
)

const (
	adminConsoleTimeout = 8 * time.Second
	adminConsoleMaxOut  = 16 * 1024
)

// adminConsoleCommand describes a single allowlisted diagnostic command.
// A command is either an OS subprocess (Name/Args, run via
// exec.CommandContext) or Native (NativeFn, run as a plain Go call) — never
// both. This is the only place either kind is defined (see
// runAdminConsoleCommand's shared timeout/truncation/audit wrapper).
type adminConsoleCommand struct {
	ID          string
	Label       string
	Description string
	Name        string
	Args        []string
	Native      bool
	NativeFn    func(ctx context.Context, ca stepca.CA) (string, error)
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
// arguments (or NativeFn) are server-controlled.  This is the only place
// they are defined. ca may be nil (Handler.caClient() failed to construct a
// client) — both native commands below handle that without panicking.
func adminConsoleCommands(cfg *config.Config, ca stepca.CA) []adminConsoleCommand {
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
			ID:          "app.version",
			Label:       "step-ui version",
			Description: "step-ui build info and pinned certificates-library version",
			Native:      true,
			NativeFn:    appVersionNativeFn,
		},
		{
			ID:          "ca.health",
			Label:       "step-ca health",
			Description: "Reachability check for the CA from the UI container",
			Native:      true,
			NativeFn:    caHealthNativeFn,
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

// appVersionNativeFn reports step-ui's own version/build metadata plus the
// pinned smallstep/certificates library version, replacing "step version"'s
// former exec.CommandContext("step", "version") call.
func appVersionNativeFn(_ context.Context, _ stepca.CA) (string, error) {
	libVersion := "unknown"
	if info, ok := debug.ReadBuildInfo(); ok {
		for _, dep := range info.Deps {
			if dep.Path == "github.com/smallstep/certificates" {
				libVersion = dep.Version
				break
			}
		}
	}
	return fmt.Sprintf(
		"step-ui %s (build %s, commit %s)\nsmallstep/certificates %s",
		Version, BuildDate, GitCommit, libVersion,
	), nil
}

// caHealthNativeFn replaces "step ca health --ca-url ... --root ..." with a
// direct stepca.CA.Health call. ca may be nil when Handler.caClient() failed
// to construct a client (e.g. root cert not yet present) — report that as
// the command's result text instead of panicking.
func caHealthNativeFn(ctx context.Context, ca stepca.CA) (string, error) {
	if ca == nil {
		return "", errors.New("CA client unavailable")
	}
	if err := ca.Health(ctx); err != nil {
		return "", err
	}
	return "ok", nil
}

// findAdminConsoleCommand looks up a command by its ID in the allowlist.
// Returns the command and true on a hit; zero value and false on a miss.
func findAdminConsoleCommand(cfg *config.Config, ca stepca.CA, id string) (adminConsoleCommand, bool) {
	for _, c := range adminConsoleCommands(cfg, ca) {
		if c.ID == id {
			return c, true
		}
	}

	return adminConsoleCommand{}, false
}

// AdminConsoleGet renders the diagnostics console form.
func (h *Handler) AdminConsoleGet(w http.ResponseWriter, r *http.Request) {
	caClient, _ := h.caClient() // nil on error is fine — native commands report it as their result text
	h.render(w, "admin_console", h.adminConsolePageData(w, r, "", nil, caClient))
}

// AdminConsolePost runs the selected allowlisted command and renders the result.
func (h *Handler) AdminConsolePost(w http.ResponseWriter, r *http.Request) {
	if !h.requireCSRF(w, r, "/admin/console") {
		return
	}

	commandID := strings.TrimSpace(r.FormValue("command_id"))
	caClient, _ := h.caClient()

	c, ok := findAdminConsoleCommand(h.cfg, caClient, commandID)
	if !ok {
		h.auditSecurity(r, "console.denied command_id="+commandID)
		data := h.adminConsolePageData(w, r, commandID, nil, caClient)
		data["ConsoleError"] = "Unknown command. Only allowlisted commands may be run."
		h.render(w, "admin_console", data)

		return
	}

	result := runAdminConsoleCommand(r.Context(), &c, caClient)
	h.auditSecurity(r, fmt.Sprintf(
		"console.run id=%s command=%q exit=%d timeout=%t duration=%s",
		c.ID, result.CommandLine, result.ExitCode, result.TimedOut, result.Duration,
	))

	h.render(w, "admin_console", h.adminConsolePageData(w, r, commandID, &result, caClient))
}

// adminConsolePageData builds the template data map for the console page.
func (h *Handler) adminConsolePageData(
	w http.ResponseWriter,
	r *http.Request,
	selectedID string,
	result *adminConsoleResult,
	caClient stepca.CA,
) map[string]interface{} {
	data := h.base(w, r, "admin_console")
	data["Commands"] = adminConsoleCommands(h.cfg, caClient)
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
// Native commands (c.Native) run c.NativeFn instead of exec.CommandContext,
// under the same timeout/truncation/duration contract.
func runAdminConsoleCommand(ctx context.Context, c *adminConsoleCommand, ca stepca.CA) adminConsoleResult {
	cctx, cancel := context.WithTimeout(ctx, adminConsoleTimeout)
	defer cancel()

	start := time.Now()

	if c.Native {
		return runNativeAdminConsoleCommand(cctx, c, ca, start)
	}

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

// runNativeAdminConsoleCommand runs c.NativeFn and formats its (text, error)
// result into the same adminConsoleResult shape the exec.CommandContext path
// produces, so the template and audit log don't need to know the difference.
func runNativeAdminConsoleCommand(cctx context.Context, c *adminConsoleCommand, ca stepca.CA, start time.Time) adminConsoleResult {
	text, err := c.NativeFn(cctx, ca)
	duration := time.Since(start).Round(time.Millisecond)

	exitCode := 0
	if err != nil {
		exitCode = 1
		if text == "" {
			text = err.Error()
		}
	}

	timedOut := cctx.Err() == context.DeadlineExceeded
	if timedOut {
		exitCode = -1
		text = strings.TrimSpace(text + "\ncommand timed out")
	}

	truncated := false
	if len(text) > adminConsoleMaxOut {
		text = text[:adminConsoleMaxOut] + "\n\n[output truncated]\n"
		truncated = true
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
	if c.Native {
		return "(native) " + c.ID
	}
	parts := append([]string{c.Name}, c.Args...)
	return strings.Join(parts, " ")
}
