package handlers

import (
	"testing"
	"time"
)

// ─── normalizeIssuePolicy ─────────────────────────────────────────────────────

func TestNormalizeIssuePolicy(t *testing.T) {
	cases := []struct {
		name      string
		template  string
		duration  string
		keyType   string
		domain    string
		wantErr   bool
		wantTempl string
		wantDur   string
		wantKT    string
	}{
		{
			name:      "default template server",
			template:  "",
			duration:  "",
			keyType:   "",
			domain:    "example.com",
			wantTempl: "server",
			wantDur:   "8760h",
			wantKT:    "EC:P-256",
		},
		{
			name:      "explicit server template",
			template:  "server",
			duration:  "8760h",
			keyType:   "EC:P-256",
			domain:    "example.com",
			wantTempl: "server",
			wantDur:   "8760h",
			wantKT:    "EC:P-256",
		},
		{
			name:      "internal template with custom duration and key type",
			template:  "internal",
			duration:  "720h",
			keyType:   "RSA:2048",
			domain:    "internal.example.com",
			wantTempl: "internal",
			wantDur:   "720h",
			wantKT:    "RSA:2048",
		},
		{
			name:      "wildcard template requires wildcard domain",
			template:  "wildcard",
			duration:  "",
			keyType:   "",
			domain:    "*.example.com",
			wantTempl: "wildcard",
			wantDur:   "8760h",
			wantKT:    "EC:P-256",
		},
		{
			name:     "wildcard template with non-wildcard domain errors",
			template: "wildcard",
			domain:   "example.com",
			wantErr:  true,
		},
		{
			name:     "unknown template errors",
			template: "bogus",
			domain:   "example.com",
			wantErr:  true,
		},
		{
			name:      "client template",
			template:  "client",
			domain:    "client.example.com",
			wantTempl: "client",
			wantDur:   "8760h",
		},
		{
			name:      "disallowed duration is ignored, falls back to template default",
			template:  "server",
			duration:  "1h",
			domain:    "example.com",
			wantTempl: "server",
			wantDur:   "8760h",
		},
		{
			name:      "disallowed key type is ignored, falls back to template default",
			template:  "server",
			keyType:   "DSA:1024",
			domain:    "example.com",
			wantTempl: "server",
			wantKT:    "EC:P-256",
		},
		{
			name:      "case-insensitive template name",
			template:  "SERVER",
			domain:    "example.com",
			wantTempl: "server",
		},
		{
			name:     "RSA 4096 key type accepted",
			template: "server",
			keyType:  "RSA:4096",
			domain:   "example.com",
			wantKT:   "RSA:4096",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			policy, err := normalizeIssuePolicy(tc.template, tc.duration, tc.keyType, tc.domain)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got policy=%+v", policy)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantTempl != "" && policy.Template != tc.wantTempl {
				t.Errorf("Template: got %q want %q", policy.Template, tc.wantTempl)
			}
			if tc.wantDur != "" && policy.Duration != tc.wantDur {
				t.Errorf("Duration: got %q want %q", policy.Duration, tc.wantDur)
			}
			if tc.wantKT != "" && policy.KeyType != tc.wantKT {
				t.Errorf("KeyType: got %q want %q", policy.KeyType, tc.wantKT)
			}
		})
	}
}

// ─── parsePostgresURL ─────────────────────────────────────────────────────────

func TestParsePostgresURL(t *testing.T) {
	cases := []struct {
		name     string
		raw      string
		wantErr  bool
		wantHost string
		wantPort string
		wantUser string
		wantDB   string
		wantPw   string
	}{
		{ //nolint:gosec // G101: test-only credential URL
			name:     "full postgres URL",
			raw:      "postgres://stepui:secret@localhost:5432/mydb",
			wantHost: "localhost",
			wantPort: "5432",
			wantUser: "stepui",
			wantDB:   "mydb",
			wantPw:   "secret",
		},
		{ //nolint:gosec // G101: test-only credential URL
			name:     "postgresql scheme",
			raw:      "postgresql://user:pw@db.example.com:5433/dbname",
			wantHost: "db.example.com",
			wantPort: "5433",
			wantUser: "user",
			wantDB:   "dbname",
		},
		{ //nolint:gosec // G101: test-only credential URL
			name:     "port defaults to 5432 when missing",
			raw:      "postgres://u:p@host/db",
			wantHost: "host",
			wantPort: "5432",
		},
		{ //nolint:gosec // G101: test-only credential URL for error path
			name:    "wrong scheme errors",
			raw:     "mysql://user:pass@host/db",
			wantErr: true,
		},
		{
			name:    "missing host errors",
			raw:     "postgres:///db",
			wantErr: true,
		},
		{
			name:    "missing user errors",
			raw:     "postgres://host/db",
			wantErr: true,
		},
		{ //nolint:gosec // G101: test-only credential URL for error path
			name:    "missing db errors",
			raw:     "postgres://user:pw@host",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			info, err := parsePostgresURL(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got info=%+v", info)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tc.wantHost != "" && info.host != tc.wantHost {
				t.Errorf("host: got %q want %q", info.host, tc.wantHost)
			}
			if tc.wantPort != "" && info.port != tc.wantPort {
				t.Errorf("port: got %q want %q", info.port, tc.wantPort)
			}
			if tc.wantUser != "" && info.user != tc.wantUser {
				t.Errorf("user: got %q want %q", info.user, tc.wantUser)
			}
			if tc.wantDB != "" && info.db != tc.wantDB {
				t.Errorf("db: got %q want %q", info.db, tc.wantDB)
			}
			if tc.wantPw != "" && info.password != tc.wantPw {
				t.Errorf("password: got %q want %q", info.password, tc.wantPw)
			}
		})
	}
}

// ─── validateIdentifier ───────────────────────────────────────────────────────

func TestValidateIdentifier(t *testing.T) {
	cases := []struct {
		name    string
		id      string
		wantErr bool
	}{
		{name: "simple hostname", id: "example.com"},
		{name: "subdomain", id: "sub.example.com"},
		{name: "wildcard", id: "*.example.com"},
		{name: "single label", id: "localhost"},
		{name: "empty errors", id: "", wantErr: true},
		{name: "leading dash errors", id: "--foo", wantErr: true},
		{name: "flag injection", id: "-x", wantErr: true},
		{name: "shell metachar", id: "ex;ample.com", wantErr: true},
		{name: "space in id", id: "ex ample.com", wantErr: true},
		{name: "path traversal", id: "../etc/passwd", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateIdentifier(tc.id)
			if tc.wantErr && err == nil {
				t.Errorf("validateIdentifier(%q): expected error", tc.id)
			}
			if !tc.wantErr && err != nil {
				t.Errorf("validateIdentifier(%q): unexpected error: %v", tc.id, err)
			}
		})
	}
}

// ─── redactArgs ───────────────────────────────────────────────────────────────

func TestRedactArgs(t *testing.T) {
	args := []string{
		"ca", "certificate",
		"--ca-url", "https://ca:9443",
		"--root", "/path/to/root.crt",
		"--provisioner", "admin",
		"--provisioner-password-file", "/secret/pw",
		"--", "example.com",
	}
	out := redactArgs(args)
	// sensitive flags' values must be replaced.
	sensitiveFlags := map[string]bool{
		"--ca-url": true, "--root": true, "--provisioner-password-file": true,
	}
	for i, a := range out {
		if sensitiveFlags[a] && i+1 < len(out) {
			if out[i+1] != "<redacted>" {
				t.Errorf("value after %q should be <redacted>, got %q", a, out[i+1])
			}
		}
	}
	// Non-sensitive args should be unchanged.
	if out[0] != "ca" || out[1] != "certificate" {
		t.Errorf("non-sensitive args changed: %v", out[:2])
	}
	// Original must not be mutated.
	if args[3] != "https://ca:9443" {
		t.Error("redactArgs mutated original slice")
	}
}

// ─── fmtUptime ────────────────────────────────────────────────────────────────

func TestFmtUptime(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{2*time.Minute + 30*time.Second, "2m"},
		{90 * time.Minute, "1h 30m"},
		{25*time.Hour + 10*time.Minute, "1d 1h 10m"},
	}
	for _, tc := range cases {
		got := fmtUptime(tc.d)
		if got != tc.want {
			t.Errorf("fmtUptime(%v)=%q want %q", tc.d, got, tc.want)
		}
	}
}

// ─── daysLeftVal ──────────────────────────────────────────────────────────────

func TestDaysLeftVal(t *testing.T) {
	now := time.Now()
	// Use 72h so the int truncation (48h could give 1 if a few nanoseconds
	// of test execution elapsed) reliably rounds to ≥ 2.
	farFuture := now.Add(72 * time.Hour)
	past := now.Add(-24 * time.Hour)

	if d := daysLeftVal(&farFuture); d < 2 {
		t.Errorf("future 72h: got %d want >=2", d)
	}
	if d := daysLeftVal(&past); d >= 0 {
		t.Errorf("past: got %d, expected negative", d)
	}
	if d := daysLeftVal(nil); d != 999 {
		t.Errorf("nil: got %d want 999", d)
	}
}
