package main

import (
	"crypto/tls"
	"log/slog"
	"os"
	"sync"
	"time"
)

// certReloader holds a cached TLS certificate and reloads it from disk
// whenever either file's modification time changes.  It is safe for concurrent
// use: reads take an RLock; the upgrade to a write lock only happens when a
// changed mtime is detected.
type certReloader struct {
	certPath string
	keyPath  string

	mu      sync.RWMutex
	cached  *tls.Certificate
	certMod time.Time
	keyMod  time.Time
}

// newCertReloader creates a reloader for the given cert/key paths.
// It does NOT load the certificate immediately; the first call to
// GetCertificate triggers the initial load so that a missing file at
// construction time is not fatal (the entrypoint may write the file
// moments later before the first TLS handshake arrives).
func newCertReloader(certPath, keyPath string) *certReloader {
	return &certReloader{certPath: certPath, keyPath: keyPath}
}

// GetCertificate satisfies tls.Config.GetCertificate.  On each call it stats
// both files; if either mtime has changed (or the cache is empty) it reloads.
// On a reload error it returns the last good cert if available, so in-flight
// serving is never interrupted by a transient file write.
func (r *certReloader) GetCertificate(_ *tls.ClientHelloInfo) (*tls.Certificate, error) {
	certMod, keyMod, statErr := r.statFiles()

	r.mu.RLock()
	cached := r.cached
	unchanged := statErr == nil && cached != nil &&
		certMod.Equal(r.certMod) && keyMod.Equal(r.keyMod)
	r.mu.RUnlock()

	if unchanged {
		return cached, nil
	}

	// Mtime changed or cache is empty — reload under write lock.
	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-check under write lock (another goroutine may have just reloaded).
	if statErr == nil && r.cached != nil &&
		certMod.Equal(r.certMod) && keyMod.Equal(r.keyMod) {
		return r.cached, nil
	}

	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		if r.cached != nil {
			slog.Warn("TLS cert reload failed — serving last good cert",
				"certPath", r.certPath, "err", err)
			return r.cached, nil
		}
		return nil, err
	}

	r.cached = &cert
	if statErr == nil {
		r.certMod = certMod
		r.keyMod = keyMod
	}
	slog.Info("TLS cert loaded", "certPath", r.certPath)
	return r.cached, nil
}

func (r *certReloader) statFiles() (certMod, keyMod time.Time, err error) {
	ci, e := os.Stat(r.certPath)
	if e != nil {
		return time.Time{}, time.Time{}, e
	}
	ki, e := os.Stat(r.keyPath)
	if e != nil {
		return time.Time{}, time.Time{}, e
	}
	return ci.ModTime(), ki.ModTime(), nil
}

// Compile-time assertion: certReloader satisfies the interface shape expected
// by tls.Config.GetCertificate.
var _ func(*tls.ClientHelloInfo) (*tls.Certificate, error) = (*certReloader)(nil).GetCertificate
