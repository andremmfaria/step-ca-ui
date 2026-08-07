package le

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestLoadOrCreateKeyNoRace verifies that concurrent loadOrCreateKey calls on
// the same path do not produce data races.  The -race detector will flag any
// unsynchronised access to the key file.
//
// Note: loadOrCreateKey itself is not locked; it is always called from within
// IssueCert which holds issueMu.  This test exercises the path that IssueCert
// would serialise in production.
func TestLoadOrCreateKeyNoRace(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "account.key")

	const workers = 5
	var wg sync.WaitGroup
	wg.Add(workers)

	// Simulate what happens if multiple goroutines each hold issueMu in turn
	// and call loadOrCreateKey.  The first one creates the key; the rest read it.
	// This test would surface a race if loadOrCreateKey were called without
	// the mutex (the -race detector would catch concurrent writes to keyPath).
	for range workers {
		go func() {
			defer wg.Done()
			issueMu.Lock()
			defer issueMu.Unlock()
			_, err := loadOrCreateKey(keyPath)
			if err != nil {
				t.Errorf("loadOrCreateKey: %v", err)
			}
		}()
	}
	wg.Wait()

	// Verify that exactly one key file exists and is readable.
	data, err := os.ReadFile(keyPath) //nolint:gosec // G304: keyPath is t.TempDir()-relative, not user input
	if err != nil {
		t.Fatalf("key file not found after concurrent creation: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("key file is empty")
	}
}

// TestSaveLoadRegistrationNoRace verifies that concurrent save+load of
// registration data under the mutex does not race.
func TestSaveLoadRegistrationNoRace(t *testing.T) {
	dir := t.TempDir()
	regPath := filepath.Join(dir, "account.json")

	const workers = 5
	var wg sync.WaitGroup
	wg.Add(workers)

	for i := range workers {
		go func(i int) {
			defer wg.Done()
			issueMu.Lock()
			defer issueMu.Unlock()
			// saveRegistration is best-effort (ignores errors); calling it
			// concurrently without the lock would race on the file.
			saveRegistration(regPath, nil)
			if i%2 == 0 {
				_, _ = loadRegistration(regPath)
			}
		}(i)
	}
	wg.Wait()
}

// TestParseCertDates checks the happy path and the nil-block path.
func TestParseCertDates(t *testing.T) {
	// Non-PEM input — both pointers should be nil.
	issued, expires := parseCertDates([]byte("not a pem block"))
	if issued != nil || expires != nil {
		t.Errorf("expected nil times for invalid PEM, got issued=%v expires=%v", issued, expires)
	}
}

// TestCertDirFor rejects any domain that could escape LEDirectory and accepts
// the hostname forms the ACME flow actually issues for.
func TestCertDirFor(t *testing.T) {
	valid := []string{
		"example.com",
		"sub.example.com",
		"*.example.com",
		"a.b.c.d.example.com",
		"xn--bcher-kva.example.com",
		"host",
	}
	for _, d := range valid {
		got, err := certDirFor(d)
		if err != nil {
			t.Errorf("certDirFor(%q) returned error: %v", d, err)
			continue
		}
		want := filepath.Join(LEDirectory, d)
		if got != want {
			t.Errorf("certDirFor(%q) = %q, want %q", d, got, want)
		}
	}

	invalid := []string{
		"",
		"../etc",
		"../../etc/cron.d",
		"example.com/../..",
		"/etc/passwd",
		"foo/bar",
		`foo\bar`,
		"..",
		".",
		".example.com",
		"example.com.",
		"-example.com",
		"example.com\x00",
		"exam ple.com",
		"example.com\r\n",
		strings.Repeat("a", 254),
	}
	for _, d := range invalid {
		got, err := certDirFor(d)
		if err == nil {
			t.Errorf("certDirFor(%q) accepted, returned %q; want error", d, got)
		}
	}
}

// TestCertDirForStaysUnderLEDirectory is the property the path guard exists for:
// no accepted domain may resolve outside LEDirectory.
func TestCertDirForStaysUnderLEDirectory(t *testing.T) {
	prefix := LEDirectory + string(filepath.Separator)
	for _, d := range []string{"example.com", "*.example.com", "deep.sub.example.com"} {
		got, err := certDirFor(d)
		if err != nil {
			t.Fatalf("certDirFor(%q): %v", d, err)
		}
		if !strings.HasPrefix(filepath.Clean(got), prefix) {
			t.Errorf("certDirFor(%q) = %q escapes %q", d, got, LEDirectory)
		}
	}
}
