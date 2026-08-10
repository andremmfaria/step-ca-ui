package middleware

import (
	"fmt"
	"net"
	"net/http"
	"strings"
)

// forwardedHeaders are the single-value forwarding headers honoured after
// X-Forwarded-For yields nothing.
var forwardedHeaders = []string{"X-Real-IP", "True-Client-IP"}

// ParseTrustedProxies turns a comma-separated list of CIDR blocks into the
// networks RealIP will trust. A malformed entry is an error rather than a
// skipped entry: a typo that silently shrank the allowlist would either strand
// the deployment on proxy addresses or, worse, leave a header-forging client
// looking like a trusted hop.
func ParseTrustedProxies(raw string) ([]*net.IPNet, error) {
	var nets []*net.IPNet
	for _, part := range strings.Split(raw, ",") {
		entry := strings.TrimSpace(part)
		if entry == "" {
			continue
		}
		_, block, err := net.ParseCIDR(entry)
		if err != nil {
			return nil, fmt.Errorf("trusted proxy CIDR %q: %w", entry, err)
		}
		nets = append(nets, block)
	}
	if len(nets) == 0 {
		return nil, fmt.Errorf("no CIDR blocks given")
	}
	return nets, nil
}

// RealIP rewrites r.RemoteAddr from a forwarding header, but only when the
// immediate peer is itself one of the trusted proxies. chi's RealIP has no such
// check, which handed the login rate limiter and the auth log to any client
// willing to rotate X-Forwarded-For (V4).
func RealIP(trusted []*net.IPNet) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if ip := clientFromHeaders(r, trusted); ip != "" {
				r.RemoteAddr = ip
			}
			next.ServeHTTP(w, r)
		})
	}
}

// clientFromHeaders returns the client address the forwarding headers claim, or
// "" when they must not be believed.
func clientFromHeaders(r *http.Request, trusted []*net.IPNet) string {
	peer := parseAddr(r.RemoteAddr)
	if peer == nil || !ipInAny(peer, trusted) {
		return ""
	}
	// Right to left: everything the trusted chain appended is at the end, and
	// the first address that is not itself a proxy is the client. Entries to
	// the left of it were written by an upstream we do not control.
	forwarded := strings.Split(r.Header.Get("X-Forwarded-For"), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		ip := parseAddr(strings.TrimSpace(forwarded[i]))
		if ip == nil || ipInAny(ip, trusted) {
			continue
		}
		return ip.String()
	}
	for _, header := range forwardedHeaders {
		if ip := parseAddr(strings.TrimSpace(r.Header.Get(header))); ip != nil && !ipInAny(ip, trusted) {
			return ip.String()
		}
	}
	return ""
}

// parseAddr accepts a bare IP or a host:port pair and returns nil for anything
// else, so an unparseable header entry is skipped rather than trusted.
func parseAddr(addr string) net.IP {
	if addr == "" {
		return nil
	}
	if ip := net.ParseIP(addr); ip != nil {
		return ip
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil
	}
	return net.ParseIP(host)
}

func ipInAny(ip net.IP, nets []*net.IPNet) bool {
	for _, block := range nets {
		if block.Contains(ip) {
			return true
		}
	}
	return false
}
