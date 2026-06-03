package handlers

import "testing"

// ─── securityEventLabel ───────────────────────────────────────────────────────

func TestSecurityEventLabel(t *testing.T) {
	cases := []struct {
		name    string
		success bool
		reason  string
		want    string
	}{
		{name: "failed attempt returns Denied", success: false, reason: "bad password", want: "Denied"},
		{name: "failed with empty reason returns Denied", success: false, reason: "", want: "Denied"},
		{name: "audit prefix returns Audit", success: true, reason: auditPrefix + "user.create target=alice role=admin", want: "Audit"},
		{name: "2FA prefix returns 2FA", success: true, reason: "2FA OTP verified", want: "2FA"},
		{name: "recovery code in reason returns 2FA", success: true, reason: "Authenticated via recovery code", want: "2FA"},
		{name: "recovery code case-insensitive", success: true, reason: "Used Recovery Code #3", want: "2FA"},
		{name: "Password reset prefix returns Reset", success: true, reason: "Password reset completed", want: "Reset"},
		{name: "Logout reason returns Logout", success: true, reason: "Logout", want: "Logout"},
		{name: "ordinary login returns Login", success: true, reason: "Login OK", want: "Login"},
		{name: "empty reason success returns Login", success: true, reason: "", want: "Login"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := securityEventLabel(tc.success, tc.reason)
			if got != tc.want {
				t.Errorf("securityEventLabel(%v, %q) = %q; want %q", tc.success, tc.reason, got, tc.want)
			}
		})
	}
}

// ─── securityEventBadge ───────────────────────────────────────────────────────

func TestSecurityEventBadge(t *testing.T) {
	cases := []struct {
		name    string
		success bool
		reason  string
		want    string
	}{
		{name: "failed returns danger", success: false, reason: "invalid password", want: "danger"},
		{name: "audit action returns warn", success: true, reason: auditPrefix + "backup.download filename=x.tgz", want: "warn"},
		{name: "password reset returns warn", success: true, reason: "Password reset completed", want: "warn"},
		{name: "successful login returns ok", success: true, reason: "Login OK", want: "ok"},
		{name: "logout returns ok", success: true, reason: "Logout", want: "ok"},
		{name: "2FA returns ok", success: true, reason: "2FA OTP verified", want: "ok"},
		{name: "empty reason success returns ok", success: true, reason: "", want: "ok"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := securityEventBadge(tc.success, tc.reason)
			if got != tc.want {
				t.Errorf("securityEventBadge(%v, %q) = %q; want %q", tc.success, tc.reason, got, tc.want)
			}
		})
	}
}
