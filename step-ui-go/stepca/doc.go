// Package stepca wraps github.com/smallstep/certificates/ca (pinned v0.30.2)
// behind the small CA interface consumed by step-ui's handlers, replacing the
// former step/step-ca CLI subprocess calls (handlers/stepcli.go, deleted).
//
// # Phase 0 spike findings (re-confirmed against the v0.30.2 module source,
// go.sum-pinned; see plans/step-cli-to-ca-lib-swap.md Phase 0)
//
// All facts below were re-confirmed by running `go doc` against the actually
// fetched v0.30.2 tag (not just re-read from the plan) and, where noted, by
// reading the module source directly under
// $GOMODCACHE/github.com/smallstep/certificates@v0.30.2.
//
//   - ca.NewClient(endpoint string, opts ...ClientOption) (*Client, error) and
//     ca.WithRootFile(filename string) ClientOption exist with these exact
//     signatures. Client.Health() / HealthWithContext(ctx), Provisioners(...) /
//     ProvisionersWithContext(ctx, ...), Sign(*api.SignRequest) (*api.SignResponse, error)
//     / SignWithContext(ctx, ...) all confirmed present on *ca.Client.
//
//   - ca.WithRootFile reads and parses the PEM file EAGERLY inside NewClient:
//     NewClient -> o.getTransport(endpoint) -> (for a file-backed root)
//     os.ReadFile + pool.AppendCertsFromPEM, before NewClient returns. A
//     missing/bad root file therefore fails client CONSTRUCTION, not just the
//     first call. Confirmed by reading ca/client.go's NewClient body directly
//     (source, not godoc). This is why handlers.Handler.caClient() (Phase 1.2)
//     builds the client lazily on first use and never at Handler-construction
//     time, and never caches a construction failure (R2).
//
//   - ca.NewProvisioner(name, kid, caURL string, password []byte, opts ...ClientOption) (*Provisioner, error)
//     confirmed; password is []byte. Provisioner.Token(subject string, sans ...string) (string, error)
//     confirmed; per ca/provisioner.go source, when len(sans) == 0 it defaults
//     sans to []string{subject} — Token(domain, domain) and Token(domain) are
//     therefore equivalent, but this package passes both explicitly (matches
//     the plan's stated call shape).
//
//   - Deviation from the plan's literal phrasing, not a contradiction of its
//     design: NewProvisioner does NOT require the caller to pre-resolve "kid"
//     via a Provisioners() list lookup. Reading ca/provisioner.go directly
//     shows that when kid == "", NewProvisioner internally calls
//     loadProvisionerJWKByName(client, name, password), which looks up the
//     named provisioner's encrypted key from the CA itself and decrypts it
//     with the supplied password — i.e. NewProvisioner(name, "", caURL, password, ...)
//     is a complete, self-sufficient "dedicated lookup" (the plan's task 3.1
//     explicitly allowed "Provisioners() (or a dedicated lookup)"). This
//     package therefore calls NewProvisioner with an empty kid and skips a
//     separate Provisioners()-based kid resolution step — simpler, same
//     network-call count, same password-handling contract (R9: password read
//     per-issuance, never stored on a struct, zeroed after use).
//
//   - api.SignRequest{CsrPEM api.CertificateRequest, OTT string, NotAfter TimeDuration, ...}.
//     Correction to the plan's shorthand "api.SignRequest{CsrPEM: <PEM bytes>, OTT: ...}":
//     CsrPEM's type is api.CertificateRequest (a struct wrapping *x509.CertificateRequest
//     with custom JSON marshaling), not a raw PEM byte slice or string — build
//     the DER CSR via x509.CreateCertificateRequest, parse it back with
//     x509.ParseCertificateRequest, and wrap it with api.NewCertificateRequest(csr)
//     (confirmed present: func NewCertificateRequest(cr *x509.CertificateRequest) CertificateRequest).
//     Only the field's Go type differs from the plan's shorthand; the
//     underlying design (R4, no BYO-key primitive) is unaffected.
//
//   - ca.CreateSignRequest(ott) and the unexported createCertificateRequest
//     helper it calls both hardcode EC P-256 (via keyutil.GenerateDefaultKey /
//     an explicit ecdsa.GenerateKey path) and no exported bring-your-own-key
//     variant exists at this level (R4 confirmed by reading ca/client.go
//     source). Reading createCertificateRequest's template also confirms the
//     exact SAN/CN shape this package replicates: Subject.CommonName = the
//     domain, DNSNames = the SAN list (defaulting to []string{commonName} when
//     no SANs are given) — see Open Questions in the plan, "resolved".
//
//   - api.RevokeRequest{Serial, OTT, ReasonCode, Reason, Passive} confirmed
//     with those exact field names/types (Serial, Reason string; ReasonCode int;
//     Passive bool). Client.Revoke(req *api.RevokeRequest, tr http.RoundTripper) (*api.RevokeResponse, error)
//     confirmed — a plain http.RoundTripper (e.g. an *http.Transport built from
//     tls.LoadX509KeyPair) satisfies it; RevokeWithContext(ctx, ...) also
//     exists and is used here. Server-side Serial/Passive enforcement (R7) was
//     not independently re-verified against a live CA in this spike (that is
//     Phase 3.3's integration test's job) but the request/response Go shapes
//     are confirmed.
//
//   - Client.Root(sha256Sum string) (*api.RootResponse, error) and
//     RootWithContext(ctx, sha256Sum string) both confirmed present — an
//     unauthenticated, fingerprint-verified root-fetch primitive exists in the
//     library exactly as the plan states. Not used by this package (Phase 5 /
//     Change Set B's bootstrap.go consumes it); documented here only because
//     Phase 0's job was to re-confirm every fact in one place.
//
//   - provisioner.List is []provisioner.Interface; concrete provisioner types
//     (e.g. *provisioner.JWK) implement GetName() string and GetType() Type,
//     where Type has a String() method. api.ProvisionersResponse{Provisioners provisioner.List, NextCursor string}
//     confirmed.
package stepca
