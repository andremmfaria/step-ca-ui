package middleware

import (
	"net"
	"net/http"
)

// ClientIP returns the host part of r.RemoteAddr, stripping the ephemeral
// source port so that every connection from one client counts under a single
// rate-limit key regardless of TCP connection cycling.
//
// When TrustProxy is on, RealIP above has already normalised RemoteAddr to a
// bare address, so SplitHostPort errors and the raw value is returned. Both
// cases produce the correct host-only string.
//
// This is the canonical spelling. handlers and api both call it, because the
// alternative is what the codebase had: some call sites recording host:port
// and others recording host, with the rate limiter keyed on one and
// users.last_ip holding the other.
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
