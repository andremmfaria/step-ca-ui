package handlers

import (
	"testing"

	"step-ui/config"
)

// defaultTestCfg returns a *config.Config that matches the docker-compose
// defaults so existing allowlist tests stay stable.
func defaultTestCfg() *config.Config {
	return &config.Config{ //nolint:gosec // G101: test-only cfg with fake credentials
		CAURL:       "https://step-ca:9443",
		RootCert:    "/home/step/certs/root_ca.crt",
		DatabaseURL: "postgres://stepui:stepui@postgres:5432/stepui?sslmode=disable",
	}
}

// TestFindAdminConsoleCommand verifies that every declared command_id in the
// allowlist resolves to the expected binary name and that an unknown id is
// correctly rejected.
func TestFindAdminConsoleCommand(t *testing.T) {
	cfg := defaultTestCfg()

	cases := []struct {
		id        string
		wantFound bool
		wantName  string
		wantArgs  []string
	}{
		{id: "system.date", wantFound: true, wantName: "date", wantArgs: nil},
		{id: "system.hostname", wantFound: true, wantName: "hostname", wantArgs: nil},
		{id: "system.identity", wantFound: true, wantName: "id", wantArgs: nil},
		{id: "system.disk", wantFound: true, wantName: "df", wantArgs: []string{"-h", "/opt/step-ui", "/home/step"}},
		{id: "system.processes", wantFound: true, wantName: "ps", wantArgs: nil},
		{id: "app.files", wantFound: true, wantName: "ls", wantArgs: []string{"-la", "/opt/step-ui"}},
		{id: "step.version", wantFound: true, wantName: "step", wantArgs: []string{"version"}},
		{
			id:        "step.ca.health",
			wantFound: true,
			wantName:  "step",
			wantArgs:  []string{"ca", "health", "--ca-url", "https://step-ca:9443", "--root", "/home/step/certs/root_ca.crt"},
		},
		{id: "openssl.version", wantFound: true, wantName: "openssl", wantArgs: []string{"version", "-a"}},
		{
			id:        "postgres.ready",
			wantFound: true,
			wantName:  "pg_isready",
			wantArgs:  []string{"-h", "postgres", "-p", "5432", "-U", "stepui", "-d", "stepui"},
		},
		// unknown command_id must be rejected
		{id: "unknown.cmd", wantFound: false},
		{id: "", wantFound: false},
		{id: "system.date; rm -rf /", wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			c, ok := findAdminConsoleCommand(cfg, tc.id)
			if ok != tc.wantFound {
				t.Fatalf("findAdminConsoleCommand(%q) found=%v; want %v", tc.id, ok, tc.wantFound)
			}
			if !tc.wantFound {
				return
			}
			if c.Name != tc.wantName {
				t.Errorf("Name: got %q want %q", c.Name, tc.wantName)
			}
			if len(c.Args) != len(tc.wantArgs) {
				t.Errorf("Args length: got %d want %d (%v vs %v)", len(c.Args), len(tc.wantArgs), c.Args, tc.wantArgs)
				return
			}
			for i, a := range tc.wantArgs {
				if c.Args[i] != a {
					t.Errorf("Args[%d]: got %q want %q", i, c.Args[i], a)
				}
			}
		})
	}
}

// TestAdminCommandLine verifies that the display string is assembled correctly.
func TestAdminCommandLine(t *testing.T) {
	cases := []struct {
		c    adminConsoleCommand
		want string
	}{
		{
			c:    adminConsoleCommand{Name: "date"},
			want: "date",
		},
		{
			c:    adminConsoleCommand{Name: "df", Args: []string{"-h", "/opt/step-ui"}},
			want: "df -h /opt/step-ui",
		},
		{
			c:    adminConsoleCommand{Name: "step", Args: []string{"ca", "health", "--ca-url", "https://step-ca:9443"}},
			want: "step ca health --ca-url https://step-ca:9443",
		},
	}

	for _, tc := range cases {
		got := adminCommandLine(&tc.c)
		if got != tc.want {
			t.Errorf("adminCommandLine(%q %v) = %q; want %q", tc.c.Name, tc.c.Args, got, tc.want)
		}
	}
}

// TestAdminConsoleAllowlistCount verifies the expected number of commands is
// declared so an accidental deletion is caught.
func TestAdminConsoleAllowlistCount(t *testing.T) {
	const want = 10
	got := len(adminConsoleCommands(defaultTestCfg()))
	if got != want {
		t.Errorf("allowlist has %d commands; want %d", got, want)
	}
}

// TestPgIsReadyArgs verifies DATABASE_URL parsing for pg_isready arg building.
func TestPgIsReadyArgs(t *testing.T) {
	cases := []struct {
		name string
		dsn  string
		want []string
	}{
		{ //nolint:gosec // G101: fake test credential
			name: "full docker-compose default",
			dsn:  "postgres://stepui:stepui@postgres:5432/stepui?sslmode=disable",
			want: []string{"-h", "postgres", "-p", "5432", "-U", "stepui", "-d", "stepui"},
		},
		{ //nolint:gosec // G101: fake test credential
			name: "ECS RDS endpoint with password",
			dsn:  "postgres://appuser:s3cr3t@db.cluster.us-east-1.rds.amazonaws.com:5432/stepui",
			want: []string{"-h", "db.cluster.us-east-1.rds.amazonaws.com", "-p", "5432", "-U", "appuser", "-d", "stepui"},
		},
		{ //nolint:gosec // G101: fake test credential
			name: "no port in DSN defaults to 5432",
			dsn:  "postgres://myuser:pass@myhost/mydb",
			want: []string{"-h", "myhost", "-p", "5432", "-U", "myuser", "-d", "mydb"},
		},
		{
			name: "empty DSN falls back to defaults",
			dsn:  "",
			want: []string{"-h", "postgres", "-p", "5432", "-U", "stepui", "-d", "stepui"},
		},
		{
			name: "malformed DSN falls back to defaults",
			dsn:  "not-a-url",
			want: []string{"-h", "postgres", "-p", "5432", "-U", "stepui", "-d", "stepui"},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := pgIsReadyArgs(tc.dsn)
			if len(got) != len(tc.want) {
				t.Fatalf("len(args)=%d; want %d — got %v", len(got), len(tc.want), got)
			}
			for i, v := range tc.want {
				if got[i] != v {
					t.Errorf("args[%d]: got %q want %q", i, got[i], v)
				}
			}
		})
	}
}

// TestAdminConsoleCommandsReflectConfig verifies that step.ca.health picks up
// cfg.CAURL / cfg.RootCert and that postgres.ready reflects the parsed DSN.
func TestAdminConsoleCommandsReflectConfig(t *testing.T) {
	cfg := &config.Config{ //nolint:gosec // G101: test-only cfg
		CAURL:       "https://ca.sec.waratek.org",
		RootCert:    "/etc/ssl/step/root.crt",
		DatabaseURL: "postgres://app:secret@rds.example.com:5432/proddb",
	}

	cmds := adminConsoleCommands(cfg)

	var caHealth, pgReady *adminConsoleCommand
	for i := range cmds {
		switch cmds[i].ID {
		case "step.ca.health":
			caHealth = &cmds[i]
		case "postgres.ready":
			pgReady = &cmds[i]
		}
	}

	if caHealth == nil {
		t.Fatal("step.ca.health not found in allowlist")
	}
	if pgReady == nil {
		t.Fatal("postgres.ready not found in allowlist")
	}

	// step.ca.health must use cfg values, not hardcoded literals.
	wantCAArgs := []string{"ca", "health", "--ca-url", "https://ca.sec.waratek.org", "--root", "/etc/ssl/step/root.crt"}
	if len(caHealth.Args) != len(wantCAArgs) {
		t.Fatalf("step.ca.health args len=%d; want %d — %v", len(caHealth.Args), len(wantCAArgs), caHealth.Args)
	}
	for i, v := range wantCAArgs {
		if caHealth.Args[i] != v {
			t.Errorf("step.ca.health args[%d]: got %q want %q", i, caHealth.Args[i], v)
		}
	}

	// postgres.ready must parse the DSN; password must NOT appear.
	wantPGArgs := []string{"-h", "rds.example.com", "-p", "5432", "-U", "app", "-d", "proddb"}
	if len(pgReady.Args) != len(wantPGArgs) {
		t.Fatalf("postgres.ready args len=%d; want %d — %v", len(pgReady.Args), len(wantPGArgs), pgReady.Args)
	}
	for i, v := range wantPGArgs {
		if pgReady.Args[i] != v {
			t.Errorf("postgres.ready args[%d]: got %q want %q", i, pgReady.Args[i], v)
		}
	}
	// Paranoia: no arg should contain the password.
	for _, a := range pgReady.Args {
		if a == "secret" {
			t.Error("postgres.ready args contain the database password — must not")
		}
	}
}
