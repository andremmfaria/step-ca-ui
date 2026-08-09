package stepca

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/smallstep/certificates/ca"

	"step-ui/config"
)

// timeout bounds every CA library call, replacing stepcli.go's
// defaultStepTimeout (deleted in Phase 1.3 along with the CLI wrapper it
// guarded).
const timeout = 30 * time.Second

// CA is the interface handlers depend on, so tests can substitute a fake
// (handlers/stepca_fake_test.go) instead of talking to a live step-ca.
type CA interface {
	Health(ctx context.Context) error
	Provisioners(ctx context.Context) ([]ProvisionerInfo, error)
	IssueCertificate(ctx context.Context, req IssueRequest) (certPEM, keyPEM []byte, err error)
	Revoke(ctx context.Context, certPath, keyPath string) error
}

// ProvisionerInfo is the minimal shape handlers/provisioners.go needs to
// render the provisioners page — mirrors the "name"/"type" JSON the deleted
// CLI call produced, so provisioners.html's index . "name"/"type" lookups
// keep working unchanged.
type ProvisionerInfo struct {
	Name string
	Type string
}

// IssueRequest is the input to Client.IssueCertificate, defined here in
// Phase 1 (moved to issue.go, with a real implementation, in Phase 3.1) so
// the CA interface above has a concrete parameter type from the start.
type IssueRequest struct {
	Domain       string
	Duration     time.Duration
	KeyType      string // "EC:P-256", "EC:P-384", "RSA:2048", "RSA:4096" — same vocabulary as cert_ops.go
	Provisioner  string
	PasswordFile string
}

// Client implements CA against a live step-ca server via ca.Client.
type Client struct {
	cfg *config.Config
	ca  *ca.Client
}

// New constructs a Client. It performs no network I/O itself — ca.NewClient
// only parses cfg.CAURL and (eagerly, per doc.go's R2 finding) reads
// cfg.RootCert from disk — so New is fast/safe to call speculatively from
// Handler.caClient() on every request until it first succeeds.
func New(cfg *config.Config) (*Client, error) {
	c, err := ca.NewClient(cfg.CAURL, ca.WithRootFile(cfg.RootCert))
	if err != nil {
		return nil, fmt.Errorf("stepca: building CA client: %w", err)
	}
	return &Client{cfg: cfg, ca: c}, nil
}

// withTimeout bounds ctx the same way defaultStepTimeout bounded every step
// CLI invocation.
func withTimeout(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, timeout)
}

// timeoutErr reports deadline-exceeded with "timed out after 30s" text, for
// parity with the display strings health.go/certs.go callers already show.
func timeoutErr(ctx context.Context, err error) error {
	if ctx.Err() == context.DeadlineExceeded {
		return fmt.Errorf("stepca: timed out after %s: %w", timeout, err)
	}
	return err
}

// Health reports whether the CA's /health endpoint is reachable and healthy.
func (c *Client) Health(ctx context.Context) error {
	cctx, cancel := withTimeout(ctx)
	defer cancel()
	if _, err := c.ca.HealthWithContext(cctx); err != nil {
		return timeoutErr(cctx, err)
	}
	return nil
}

// Provisioners lists the CA's configured provisioners.
func (c *Client) Provisioners(ctx context.Context) ([]ProvisionerInfo, error) {
	cctx, cancel := withTimeout(ctx)
	defer cancel()
	resp, err := c.ca.ProvisionersWithContext(cctx)
	if err != nil {
		return nil, timeoutErr(cctx, err)
	}
	out := make([]ProvisionerInfo, 0, len(resp.Provisioners))
	for _, p := range resp.Provisioners {
		out = append(out, ProvisionerInfo{Name: p.GetName(), Type: p.GetType().String()})
	}
	return out, nil
}

// IssueCertificate and Revoke are stubbed here in Phase 1 only so *Client
// satisfies the full CA interface as soon as Handler.caClient() (Phase 1.2)
// needs to assign a *Client to a stepca.CA variable. Phase 3 replaces both
// stubs with real implementations in the new stepca/issue.go and
// stepca/revoke.go files and removes these bodies.

func (c *Client) IssueCertificate(context.Context, IssueRequest) (certPEM, keyPEM []byte, err error) {
	return nil, nil, errors.New("stepca: IssueCertificate not yet implemented (lands in Phase 3.1)")
}

func (c *Client) Revoke(context.Context, string, string) error {
	return errors.New("stepca: Revoke not yet implemented (lands in Phase 3.3)")
}
