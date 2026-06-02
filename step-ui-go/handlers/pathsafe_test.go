package handlers

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestContainedPath exercises containedPath with valid and adversarial inputs.
func TestContainedPath(t *testing.T) {
	// Create a temporary root directory for tests that need a real filesystem path.
	root := t.TempDir()

	// Create a sibling directory to test the boundary check.
	sibling := root + "-sibling"
	if err := os.MkdirAll(sibling, 0o750); err != nil {
		t.Fatalf("setup sibling dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(sibling) })

	tests := []struct {
		name      string
		root      string
		candidate string
		wantErr   bool
		wantSub   string // expected substring in the returned path (when no error)
	}{
		{
			name:      "valid direct child",
			root:      root,
			candidate: "foo.crt",
			wantSub:   "foo.crt",
		},
		{
			name:      "valid nested child",
			root:      root,
			candidate: "subdir/bar.key",
			wantSub:   filepath.Join("subdir", "bar.key"),
		},
		{
			name:      "traversal via ..",
			root:      root,
			candidate: "../escape.txt",
			wantErr:   true,
		},
		{
			name:      "traversal via encoded double-dot",
			root:      root,
			candidate: "../../etc/passwd",
			wantErr:   true,
		},
		{
			name:      "sibling directory boundary",
			root:      root,
			candidate: "../" + filepath.Base(sibling) + "/evil",
			wantErr:   true,
		},
		{
			name:      "absolute path outside root",
			root:      root,
			candidate: "/etc/passwd",
			wantErr:   true,
		},
		{
			name:      "slash-only candidate treated as root itself",
			root:      root,
			candidate: "/",
			wantErr:   true, // root itself — not strictly inside
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := containedPath(tc.root, tc.candidate)
			if tc.wantErr {
				if err == nil {
					t.Errorf("containedPath(%q, %q) = %q; want error", tc.root, tc.candidate, got)
				}
				return
			}
			if err != nil {
				t.Errorf("containedPath(%q, %q) unexpected error: %v", tc.root, tc.candidate, err)
				return
			}
			if tc.wantSub != "" && !strings.HasSuffix(got, tc.wantSub) {
				t.Errorf("containedPath result %q does not end with %q", got, tc.wantSub)
			}
		})
	}
}

// TestSafeName exercises safeName with valid and adversarial inputs.
func TestSafeName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		want    string
	}{
		{name: "simple alphanumeric", input: "mycert", want: "mycert"},
		{name: "with dash and dot", input: "my-cert.pem", want: "my-cert.pem"},
		{name: "with underscore", input: "server_cert", want: "server_cert"},
		{name: "numbers", input: "cert123", want: "cert123"},
		{name: "traversal via ..", input: "../etc", wantErr: true},
		{name: "slash in name", input: "a/b", wantErr: true},
		{name: "backslash in name", input: `a\b`, wantErr: true},
		{name: "empty string", input: "", wantErr: true},
		{name: "null-byte", input: "abc\x00def", wantErr: true},
		{name: "space in name", input: "my cert", wantErr: true},
		{name: "leading dot preserved", input: ".hidden", want: ".hidden"},
		{name: "just dots (filepath.Base changes it)", input: "..", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := safeName(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Errorf("safeName(%q) = %q; want error", tc.input, got)
				}
				return
			}
			if err != nil {
				t.Errorf("safeName(%q) unexpected error: %v", tc.input, err)
				return
			}
			if got != tc.want {
				t.Errorf("safeName(%q) = %q; want %q", tc.input, got, tc.want)
			}
		})
	}
}
