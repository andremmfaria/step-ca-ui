package handlers

import (
	"fmt"
	"regexp"
	"strings"
)

// validIdentifier accepts hostnames, FQDNs, and wildcards (*.example.com).
// Anything that could be parsed as a CLI flag or contain shell metacharacters
// is rejected. The pattern is intentionally conservative.
var validIdentifier = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)*$`)

// validateIdentifier rejects blank values, values starting with '-', and
// anything that does not look like a hostname or wildcard hostname. Every
// CSR-bound domain must pass this before it reaches the CA library — it is
// the same guard that previously protected against flag injection into the
// step CLI's argv, and remains load-bearing now that domains flow into
// x509.CertificateRequest fields instead.
func validateIdentifier(id string) error {
	if id == "" {
		return fmt.Errorf("identifier must not be empty")
	}
	if strings.HasPrefix(id, "-") {
		return fmt.Errorf("identifier %q starts with '-': possible flag injection", id)
	}
	if !validIdentifier.MatchString(id) {
		return fmt.Errorf("identifier %q contains disallowed characters", id)
	}
	return nil
}
