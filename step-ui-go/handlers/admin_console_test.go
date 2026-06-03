package handlers

import "testing"

// TestFindAdminConsoleCommand verifies that every declared command_id in the
// allowlist resolves to the expected binary name and that an unknown id is
// correctly rejected.
func TestFindAdminConsoleCommand(t *testing.T) {
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
			wantArgs:  []string{"-h", "postgres", "-U", "stepui", "-d", "stepui"},
		},
		// unknown command_id must be rejected
		{id: "unknown.cmd", wantFound: false},
		{id: "", wantFound: false},
		{id: "system.date; rm -rf /", wantFound: false},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			c, ok := findAdminConsoleCommand(tc.id)
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
	got := len(adminConsoleCommands())
	if got != want {
		t.Errorf("allowlist has %d commands; want %d", got, want)
	}
}
