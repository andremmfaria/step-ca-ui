package middleware

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
)

// mustTrust parses CIDRs a test controls and fails loudly on a typo.
func mustTrust(t *testing.T, raw string) []*net.IPNet {
	t.Helper()
	nets, err := ParseTrustedProxies(raw)
	if err != nil {
		t.Fatalf("ParseTrustedProxies(%q): %v", raw, err)
	}
	return nets
}

// remoteAddrAfterRealIP runs one request through RealIP and reports the
// RemoteAddr the wrapped handler saw.
func remoteAddrAfterRealIP(t *testing.T, trusted []*net.IPNet, peer string, headers map[string]string) string {
	t.Helper()
	var seen string
	handler := RealIP(trusted)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	}))
	req := newReq("GET", "/login")
	req.RemoteAddr = peer
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	handler.ServeHTTP(httptest.NewRecorder(), req)
	return seen
}

// ─── ParseTrustedProxies ───────────────────────────────────────────────────────

func TestParseTrustedProxies_Valid(t *testing.T) {
	nets, err := ParseTrustedProxies(" 10.0.0.7/32 ,192.168.0.0/16, ")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(nets) != 2 {
		t.Fatalf("blocks: got %d want 2", len(nets))
	}
}

// TestParseTrustedProxies_Rejects covers the fail-closed contract: a
// misconfigured allowlist must stop the process, not shrink silently.
func TestParseTrustedProxies_Rejects(t *testing.T) {
	cases := map[string]string{
		"empty":          "",
		"only_separator": " , ",
		"bare_ip":        "10.0.0.7",
		"malformed":      "10.0.0.7/32,not-a-cidr",
		"bad_mask":       "10.0.0.0/33",
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := ParseTrustedProxies(raw); err == nil {
				t.Errorf("ParseTrustedProxies(%q): expected an error", raw)
			}
		})
	}
}

// ─── RealIP (V4) ───────────────────────────────────────────────────────────────

// TestRealIP_UntrustedPeer_HeaderIgnored is the finding itself: a client that
// connects directly and crafts a forwarding header must not choose the address
// the rate limiter and the auth log key off.
func TestRealIP_UntrustedPeer_HeaderIgnored(t *testing.T) {
	trusted := mustTrust(t, "10.0.0.7/32")
	headers := map[string]string{
		"X-Forwarded-For": "1.2.3.4",
		"X-Real-IP":       "5.6.7.8",
		"True-Client-IP":  "9.10.11.12",
	}
	got := remoteAddrAfterRealIP(t, trusted, "203.0.113.9:44321", headers)
	if got != "203.0.113.9:44321" {
		t.Errorf("untrusted peer: RemoteAddr got %q, want the socket peer unchanged", got)
	}
}

// TestRealIP_TrustedPeer_HeaderHonoured is the control for the test above.
func TestRealIP_TrustedPeer_HeaderHonoured(t *testing.T) {
	trusted := mustTrust(t, "10.0.0.7/32")
	got := remoteAddrAfterRealIP(t, trusted, "10.0.0.7:9999", map[string]string{
		"X-Forwarded-For": "1.2.3.4",
	})
	if got != "1.2.3.4" {
		t.Errorf("trusted peer: RemoteAddr got %q want %q", got, "1.2.3.4")
	}
}

// TestRealIP_ChainReadRightToLeft confirms the client is the rightmost address
// that is not itself a proxy, so a client prepending its own entries cannot
// choose the result.
func TestRealIP_ChainReadRightToLeft(t *testing.T) {
	trusted := mustTrust(t, "10.0.0.0/24")
	got := remoteAddrAfterRealIP(t, trusted, "10.0.0.7:9999", map[string]string{
		"X-Forwarded-For": "203.0.113.9, 1.2.3.4, 10.0.0.9, 10.0.0.7",
	})
	if got != "1.2.3.4" {
		t.Errorf("RemoteAddr got %q want %q", got, "1.2.3.4")
	}
}

func TestRealIP_SingleValueHeaders(t *testing.T) {
	trusted := mustTrust(t, "10.0.0.7/32")
	cases := map[string]string{"X-Real-IP": "5.6.7.8", "True-Client-IP": "9.10.11.12"}
	for header, want := range cases {
		t.Run(header, func(t *testing.T) {
			got := remoteAddrAfterRealIP(t, trusted, "10.0.0.7:9999", map[string]string{header: want})
			if got != want {
				t.Errorf("RemoteAddr got %q want %q", got, want)
			}
		})
	}
}

// TestRealIP_NoUsableHeader leaves RemoteAddr alone rather than blanking it:
// the peer is the best address available when the chain says nothing.
func TestRealIP_NoUsableHeader(t *testing.T) {
	trusted := mustTrust(t, "10.0.0.0/24")
	cases := map[string]map[string]string{
		"absent":       {},
		"garbage":      {"X-Forwarded-For": "not-an-ip"},
		"all_trusted":  {"X-Forwarded-For": "10.0.0.9, 10.0.0.7"},
		"empty_string": {"X-Forwarded-For": ""},
	}
	for name, headers := range cases {
		t.Run(name, func(t *testing.T) {
			got := remoteAddrAfterRealIP(t, trusted, "10.0.0.7:9999", headers)
			if got != "10.0.0.7:9999" {
				t.Errorf("RemoteAddr got %q, want the socket peer unchanged", got)
			}
		})
	}
}

// TestRealIP_PortedEntriesAndIPv6 covers the shapes a proxy may actually write.
func TestRealIP_PortedEntriesAndIPv6(t *testing.T) {
	trusted := mustTrust(t, "10.0.0.7/32")
	cases := map[string]struct{ header, want string }{
		"host_port": {"1.2.3.4:5678", "1.2.3.4"},
		"ipv6":      {"2001:db8::1", "2001:db8::1"},
		"ipv6_port": {"[2001:db8::2]:443", "2001:db8::2"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got := remoteAddrAfterRealIP(t, trusted, "10.0.0.7:9999", map[string]string{"X-Forwarded-For": tc.header})
			if got != tc.want {
				t.Errorf("RemoteAddr got %q want %q", got, tc.want)
			}
		})
	}
}
