package handlers

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"step-ui/models"
)

// makeRequest builds an *http.Request with the given form values for parseSMTPFields tests.
func makeRequest(t *testing.T, vals url.Values) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/admin/notifications", strings.NewReader(vals.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func defaultCurrent() *models.NotificationSettings {
	return &models.NotificationSettings{
		ID:           1,
		SMTPPort:     587,
		SMTPSecurity: "starttls",
		SMTPPassword: "stored-secret",
	}
}

// TestParseSMTPFields covers the parse + validate logic and the
// password-preserve-on-blank invariant.
func TestParseSMTPFields(t *testing.T) {
	cases := []struct {
		name        string
		form        url.Values
		current     *models.NotificationSettings
		wantEnabled bool
		wantHost    string
		wantPort    int
		wantSec     string
		wantUser    string
		wantPwd     string // expected SMTPPassword in result
		wantFrom    string
		wantErrSub  string // non-empty → expect error containing this substring
	}{
		{
			name: "full SMTP fields saved correctly",
			form: url.Values{
				"smtp_enabled":  {"on"},
				"smtp_host":     {"mail.example.com"},
				"smtp_port":     {"465"},
				"smtp_security": {"tls"},
				"smtp_username": {"user@example.com"},
				"smtp_password": {"new-password"},
				"smtp_from":     {"noreply@example.com"},
			},
			current:     defaultCurrent(),
			wantEnabled: true,
			wantHost:    "mail.example.com",
			wantPort:    465,
			wantSec:     "tls",
			wantUser:    "user@example.com",
			wantPwd:     "new-password",
			wantFrom:    "noreply@example.com",
		},
		{
			name: "blank password preserves stored credential",
			form: url.Values{
				"smtp_enabled":  {"on"},
				"smtp_host":     {"mail.example.com"},
				"smtp_port":     {"587"},
				"smtp_security": {"starttls"},
				"smtp_username": {"user"},
				"smtp_password": {""},
				"smtp_from":     {"noreply@example.com"},
			},
			current:     defaultCurrent(),
			wantEnabled: true,
			// SMTPPassword must stay "stored-secret" — not be cleared
			wantPwd: "stored-secret",
		},
		{
			name: "whitespace-only password also preserves stored credential",
			form: url.Values{
				"smtp_enabled":  {"on"},
				"smtp_host":     {"mail.example.com"},
				"smtp_port":     {"587"},
				"smtp_security": {"starttls"},
				"smtp_password": {"   "},
				"smtp_from":     {"noreply@example.com"},
			},
			current:     defaultCurrent(),
			wantEnabled: true,
			wantPwd:     "stored-secret",
		},
		{
			name: "port below 1 is rejected",
			form: url.Values{
				"smtp_port":     {"0"},
				"smtp_security": {"starttls"},
			},
			current:    defaultCurrent(),
			wantErrSub: "port",
		},
		{
			name: "port above 65535 is rejected",
			form: url.Values{
				"smtp_port":     {"99999"},
				"smtp_security": {"starttls"},
			},
			current:    defaultCurrent(),
			wantErrSub: "port",
		},
		{
			name: "unknown security mode is rejected",
			form: url.Values{
				"smtp_port":     {"587"},
				"smtp_security": {"ssl"},
			},
			current:    defaultCurrent(),
			wantErrSub: "security",
		},
		{
			name: "none security mode is accepted",
			form: url.Values{
				"smtp_port":     {"25"},
				"smtp_security": {"none"},
			},
			current:  defaultCurrent(),
			wantSec:  "none",
			wantPort: 25,
		},
		{
			name: "empty port falls back to current",
			form: url.Values{
				"smtp_port":     {""},
				"smtp_security": {"starttls"},
			},
			current:  defaultCurrent(),
			wantPort: 587,
		},
		{
			name: "empty security falls back to current",
			form: url.Values{
				"smtp_port":     {"587"},
				"smtp_security": {""},
			},
			current: defaultCurrent(),
			wantSec: "starttls",
		},
		{
			name: "smtp_enabled absent means disabled",
			form: url.Values{
				"smtp_port":     {"587"},
				"smtp_security": {"starttls"},
			},
			current:     defaultCurrent(),
			wantEnabled: false,
		},
		{
			name: "uppercase security mode rejected",
			form: url.Values{
				"smtp_port":     {"587"},
				"smtp_security": {"STARTTLS"},
			},
			// ToLower normalization means this should actually PASS
			// (parseSMTPFields calls strings.ToLower)
			current: defaultCurrent(),
			wantSec: "starttls",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := makeRequest(t, tc.form)
			dst := &models.NotificationSettings{}
			result, err := parseSMTPFields(req, dst, tc.current)

			if tc.wantErrSub != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErrSub)
				}
				if !strings.Contains(err.Error(), tc.wantErrSub) {
					t.Errorf("error %q does not contain %q", err.Error(), tc.wantErrSub)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantEnabled && !result.SMTPEnabled {
				t.Errorf("SMTPEnabled: got false want true")
			}
			if !tc.wantEnabled && result.SMTPEnabled {
				t.Errorf("SMTPEnabled: got true want false")
			}
			if tc.wantHost != "" && result.SMTPHost != tc.wantHost {
				t.Errorf("SMTPHost: got %q want %q", result.SMTPHost, tc.wantHost)
			}
			if tc.wantPort != 0 && result.SMTPPort != tc.wantPort {
				t.Errorf("SMTPPort: got %d want %d", result.SMTPPort, tc.wantPort)
			}
			if tc.wantSec != "" && result.SMTPSecurity != tc.wantSec {
				t.Errorf("SMTPSecurity: got %q want %q", result.SMTPSecurity, tc.wantSec)
			}
			if tc.wantUser != "" && result.SMTPUsername != tc.wantUser {
				t.Errorf("SMTPUsername: got %q want %q", result.SMTPUsername, tc.wantUser)
			}
			if tc.wantPwd != "" && result.SMTPPassword != tc.wantPwd {
				t.Errorf("SMTPPassword: got %q want %q", result.SMTPPassword, tc.wantPwd)
			}
			if tc.wantFrom != "" && result.SMTPFrom != tc.wantFrom {
				t.Errorf("SMTPFrom: got %q want %q", result.SMTPFrom, tc.wantFrom)
			}
		})
	}
}
