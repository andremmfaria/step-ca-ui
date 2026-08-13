// Package db provides database access helpers for step-ui.
package db

import (
	"errors"
	"testing"
)

// TestResolveAdminPassword exercises the guard logic without a live DB.
// The actual log.Fatal call in InitSchema is unreachable in unit tests, but
// the extracted helper is fully testable.
func TestResolveAdminPassword(t *testing.T) {
	cases := []struct {
		name    string
		envVal  string
		wantErr bool
	}{
		{
			name:    "password set — seed proceeds",
			envVal:  "StrongPassword123!",
			wantErr: false,
		},
		{
			name:    "password empty — must error",
			envVal:  "",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := resolveAdminPassword(func(string) string { return tc.envVal })
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error when STEPUI_ADMIN_PASSWORD is empty, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.envVal {
				t.Errorf("resolveAdminPassword returned %q, want %q", got, tc.envVal)
			}
		})
	}
}

// TestResolveAdminPasswordErrorMessage verifies the error message contains
// actionable guidance so operators know exactly what to set.
func TestResolveAdminPasswordErrorMessage(t *testing.T) {
	_, err := resolveAdminPassword(func(string) string { return "" })
	if err == nil {
		t.Fatal("expected non-nil error")
	}
	msg := err.Error()
	for _, want := range []string{"STEPUI_ADMIN_PASSWORD", "openssl"} {
		if !errors.Is(err, err) { // keep the import used
			_ = want
		}
		// Simple substring check — the message must be actionable
		found := false
		for i := range len(msg) - len(want) + 1 {
			if msg[i:i+len(want)] == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("error message missing %q: %s", want, msg)
		}
	}
}
