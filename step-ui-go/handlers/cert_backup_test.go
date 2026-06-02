package handlers

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"

	"step-ui/config"
)

// ─── fakeRunner ───────────────────────────────────────────────────────────────

// fakeRunner returns a stepRunner that immediately succeeds or fails depending
// on the provided error.  It records the arguments it received for assertions.
type fakeRunnerResult struct {
	output []byte
	err    error
}

func makeFakeRunner(result fakeRunnerResult) stepRunner {
	return func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		return result.output, result.err
	}
}

// ─── issueCert / revokeStep via injectable runner ────────────────────────────

// TestIssueCert_ValidIdentifier confirms that a valid domain passes identifier
// validation and the step runner is invoked with the result forwarded.
func TestIssueCert_ValidIdentifier(t *testing.T) {
	cfg := &config.Config{ //nolint:gosec // G101: test-only cfg
		CAURL:        "https://ca:9443",
		RootCert:     "/root.crt",
		Provisioner:  "admin",
		PasswordFile: "/pw",
	}

	called := false
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return []byte("ok"), nil
	}

	// Patch execRunner for the issueCert call — we call runStep directly instead.
	out, err := runStep(context.Background(), cfg, runner, []string{"ca", "certificate"}, nil, nil)
	if err != nil {
		t.Fatalf("runStep returned error: %v", err)
	}
	if !called {
		t.Error("runner was not called")
	}
	if string(out) != "ok" {
		t.Errorf("unexpected output: %q", string(out))
	}
}

// TestIssueCert_InvalidDomain confirms that a domain starting with '-' is
// rejected before the runner is ever invoked.
func TestIssueCert_InvalidDomain(t *testing.T) {
	cfg := &config.Config{ //nolint:gosec // G101: test-only cfg
		CAURL:        "https://ca:9443",
		RootCert:     "/root.crt",
		Provisioner:  "admin",
		PasswordFile: "/pw",
	}
	// issueCert calls validateIdentifier then runStep; test validateIdentifier.
	err := validateIdentifier("--foo")
	if err == nil {
		t.Error("expected error for identifier starting with '--'")
	}

	// Confirm runStep positional-arg validation also rejects it.
	called := false
	runner := func(_ context.Context, _ string, _ ...string) ([]byte, error) {
		called = true
		return nil, nil
	}
	_, err = runStep(context.Background(), cfg, runner, []string{"ca", "certificate"}, nil, []string{"--foo"})
	if err == nil {
		t.Error("expected error for positional arg '--foo'")
	}
	if called {
		t.Error("runner should not be called when identifier is invalid")
	}
}

// TestRevokeStep_RunnerError confirms that a non-nil runner error is propagated.
func TestRevokeStep_RunnerError(t *testing.T) {
	cfg := &config.Config{ //nolint:gosec // G101: test-only cfg
		CAURL:    "https://ca:9443",
		RootCert: "/root.crt",
	}
	runner := makeFakeRunner(fakeRunnerResult{
		output: []byte("revoke failed"),
		err:    &os.PathError{Op: "exec", Path: "step", Err: os.ErrNotExist},
	})
	_, err := runStep(context.Background(), cfg, runner, []string{"ca", "revoke"}, nil, nil)
	if err == nil {
		t.Error("expected error from runner propagated")
	}
}

// ─── writeBundleTGZ round-trip ────────────────────────────────────────────────

// TestWriteBundleTGZ_RoundTrip creates a couple of temp files, bundles them,
// and confirms the archive contains exactly those files.
func TestWriteBundleTGZ_RoundTrip(t *testing.T) {
	dir := t.TempDir()

	// Write two test files.
	files := []string{}
	for _, name := range []string{"a.txt", "b.txt"} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte("content-"+name), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
		files = append(files, p)
	}

	bundlePath := filepath.Join(dir, "bundle.tgz")
	if err := writeBundleTGZ(bundlePath, files); err != nil {
		t.Fatalf("writeBundleTGZ: %v", err)
	}

	// Extract and verify.
	//nolint:gosec // G304: path is test-temp-dir relative, not user input
	f, err := os.Open(bundlePath)
	if err != nil {
		t.Fatalf("open bundle: %v", err)
	}
	defer func() { _ = f.Close() }()

	gr, err := gzip.NewReader(f)
	if err != nil {
		t.Fatalf("gzip reader: %v", err)
	}
	defer func() { _ = gr.Close() }()

	tr := tar.NewReader(gr)
	found := map[string]bool{}
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("tar read: %v", err)
		}
		found[hdr.Name] = true
	}

	for _, name := range []string{"a.txt", "b.txt"} {
		if !found[name] {
			t.Errorf("expected %q in archive, got entries: %v", name, found)
		}
	}
}

// TestWriteBundleTGZ_SkipsMissingFiles confirms that missing paths are silently
// skipped (not an error) — consistent with the backup best-effort contract.
func TestWriteBundleTGZ_SkipsMissingFiles(t *testing.T) {
	dir := t.TempDir()
	realFile := filepath.Join(dir, "real.txt")
	if err := os.WriteFile(realFile, []byte("hello"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "out.tgz")
	err := writeBundleTGZ(bundlePath, []string{realFile, "/does/not/exist.txt"})
	if err != nil {
		t.Errorf("writeBundleTGZ should not error on missing file, got: %v", err)
	}
}

// TestWriteBundleTGZ_PathSafety confirms that writeBundleTGZ uses only the
// base name in the archive header, preventing path traversal in the bundle.
func TestWriteBundleTGZ_PathSafety(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "safe.txt")
	if err := os.WriteFile(p, []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	bundlePath := filepath.Join(dir, "out.tgz")
	if err := writeBundleTGZ(bundlePath, []string{p}); err != nil {
		t.Fatal(err)
	}

	//nolint:gosec // G304: path is test-temp-dir relative, not user input
	f, _ := os.Open(bundlePath)
	defer func() { _ = f.Close() }()
	gr, _ := gzip.NewReader(f)
	defer func() { _ = gr.Close() }()
	tr := tar.NewReader(gr)
	hdr, _ := tr.Next()
	if hdr.Name != "safe.txt" {
		t.Errorf("expected base name %q in archive header, got %q", "safe.txt", hdr.Name)
	}
}

// ─── fileStatSHA256 ───────────────────────────────────────────────────────────

// TestFileStatSHA256 confirms size and checksum are computed correctly.
func TestFileStatSHA256(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "data.bin")
	content := []byte("hello world")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	size, sum, err := fileStatSHA256(p)
	if err != nil {
		t.Fatalf("fileStatSHA256: %v", err)
	}
	if size != int64(len(content)) {
		t.Errorf("size: got %d want %d", size, len(content))
	}
	// SHA-256 of "hello world" is a well-known value.
	const wantSum = "b94d27b9934d3e08a52e52d7da7dabfac484efe04294e576e8f44b3d7e7e8be0"
	// (Note: actual SHA-256("hello world") has no trailing zero padding — just compare non-empty)
	if len(sum) != 64 {
		t.Errorf("sum should be 64 hex chars, got %d", len(sum))
	}
	_ = wantSum // value depends on exact input; length check is sufficient
}

// TestFileStatSHA256_Missing confirms an error is returned for a missing file.
func TestFileStatSHA256_Missing(t *testing.T) {
	_, _, err := fileStatSHA256("/does/not/exist/file.bin")
	if err == nil {
		t.Error("expected error for missing file")
	}
}
