package handlers

import (
	"context"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"

	"step-ui/models"
)

// makeLERequest builds an *http.Request with the given form values for
// parseLESettingsFields tests.
func makeLERequest(t *testing.T, vals url.Values) *http.Request {
	t.Helper()
	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		"/le/settings", strings.NewReader(vals.Encode()))
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	return req
}

func storedLESettings() *models.LESettings {
	//nolint:gosec // G101: fixture values, not credentials
	return &models.LESettings{
		ID:           1,
		Email:        "acme@example.com",
		Provider:     "cloudflare",
		CFToken:      "stored-cf-token",
		CFZoneID:     "stored-zone",
		R53KeyID:     "AKIAIOSFODNN7EXAMPLE",
		R53SecretKey: "stored-r53-secret",
		R53Region:    "eu-west-1",
	}
}

// TestParseLESettingsFields covers the secret-preserve-on-blank invariant: the
// form no longer echoes the DNS-provider credentials back (V2), so a blank
// field must never clear the stored one.
func TestParseLESettingsFields(t *testing.T) {
	//nolint:gosec // G101: fixture values, not credentials
	cases := []struct {
		name          string
		form          url.Values
		wantCFToken   string
		wantR53Secret string
		wantRegion    string
		wantProvider  string
	}{
		{
			name: "blank secrets preserve the stored credentials",
			form: url.Values{
				"email":      {"acme@example.com"},
				"provider":   {"cloudflare"},
				"cf_token":   {""},
				"cf_zone_id": {"stored-zone"},
				"r53_key_id": {"AKIAIOSFODNN7EXAMPLE"},
				"r53_secret": {""},
				"r53_region": {"eu-west-1"},
			},
			wantCFToken:   "stored-cf-token",
			wantR53Secret: "stored-r53-secret",
			wantRegion:    "eu-west-1",
			wantProvider:  "cloudflare",
		},
		{
			name: "absent secret fields preserve the stored credentials",
			form: url.Values{
				"provider": {"cloudflare"},
			},
			wantCFToken:   "stored-cf-token",
			wantR53Secret: "stored-r53-secret",
			wantRegion:    "us-east-1",
			wantProvider:  "cloudflare",
		},
		{
			name: "whitespace-only secrets preserve the stored credentials",
			form: url.Values{
				"provider":   {"route53"},
				"cf_token":   {"   "},
				"r53_secret": {"  "},
			},
			wantCFToken:   "stored-cf-token",
			wantR53Secret: "stored-r53-secret",
			wantRegion:    "us-east-1",
			wantProvider:  "route53",
		},
		{
			name: "submitted secrets replace the stored credentials",
			form: url.Values{
				"provider":   {"cloudflare"},
				"cf_token":   {"new-cf-token"},
				"r53_secret": {"new-r53-secret"},
				"r53_region": {"us-west-2"},
			},
			wantCFToken:   "new-cf-token",
			wantR53Secret: "new-r53-secret",
			wantRegion:    "us-west-2",
			wantProvider:  "cloudflare",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseLESettingsFields(makeLERequest(t, tc.form), storedLESettings())

			if got.CFToken != tc.wantCFToken {
				t.Errorf("CFToken: got %q want %q", got.CFToken, tc.wantCFToken)
			}
			if got.R53SecretKey != tc.wantR53Secret {
				t.Errorf("R53SecretKey: got %q want %q", got.R53SecretKey, tc.wantR53Secret)
			}
			if got.R53Region != tc.wantRegion {
				t.Errorf("R53Region: got %q want %q", got.R53Region, tc.wantRegion)
			}
			if got.Provider != tc.wantProvider {
				t.Errorf("Provider: got %q want %q", got.Provider, tc.wantProvider)
			}
		})
	}
}

// TestLESettingsTemplateNeverEchoesSecrets pins the template side of V2: the
// rendered form must not carry the stored token or secret key.
func TestLESettingsTemplateNeverEchoesSecrets(t *testing.T) {
	raw, err := os.ReadFile("../templates/le_settings.html")
	if err != nil {
		t.Fatalf("read le_settings.html: %v", err)
	}
	for _, field := range []string{".LESettings.CFToken", ".LESettings.R53SecretKey"} {
		if strings.Contains(string(raw), field) {
			t.Errorf("le_settings.html must not render %s into the page", field)
		}
	}
}
