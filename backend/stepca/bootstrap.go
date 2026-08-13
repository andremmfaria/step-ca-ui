package stepca

import (
	"context"
	"encoding/pem"
	"fmt"

	"github.com/smallstep/certificates/ca"
)

// FetchRootByFingerprint fetches and verifies the CA's root certificate by
// its SHA256 fingerprint, replicating `step ca root`'s trust-on-first-use
// check with zero hand-rolling.
//
// Client.Root/RootWithContext (verified against v0.30.2 source, ca/client.go)
// always issues its GET /root/{sha256sum} over its own insecure pre-trust
// client — newInsecureClient(), TLS InsecureSkipVerify — regardless of how
// the *ca.Client wrapping it was constructed, and verifies the returned
// certificate's SHA256 against the fingerprint before returning an error if
// they don't match. The wrapping *ca.Client still requires some transport
// option to construct at all (NewClient rejects "no root cert, no root
// sha256, no transport" configurations), so ca.WithInsecure() is used here —
// it is never actually consulted for the /root request itself, only to
// satisfy that constructor check; there is no trusted root yet at bootstrap
// time, which is exactly the problem this function solves.
func FetchRootByFingerprint(ctx context.Context, caURL, fingerprint string) ([]byte, error) {
	c, err := ca.NewClient(caURL, ca.WithInsecure())
	if err != nil {
		return nil, fmt.Errorf("stepca: building bootstrap CA client: %w", err)
	}
	resp, err := c.RootWithContext(ctx, fingerprint)
	if err != nil {
		return nil, fmt.Errorf("stepca: fetching root by fingerprint: %w", err)
	}
	if resp.RootPEM.Certificate == nil {
		return nil, fmt.Errorf("stepca: root response contained no certificate")
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: resp.RootPEM.Raw}), nil
}
