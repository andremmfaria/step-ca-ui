package handlers

import (
	"log/slog"
	"runtime/debug"
)

// safeGo launches fn in a new goroutine wrapped with panic recovery.
// A panic is caught, logged with a stack trace, and the goroutine exits
// cleanly — the process survives.  Use it for every fire-and-forget goroutine
// so a bug in background work cannot crash the server.
func safeGo(name string, fn func()) {
	go func() {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("panic in background goroutine", "name", name, "panic", r, "stack", string(debug.Stack()))
			}
		}()
		fn()
	}()
}

// SafeGoExported is the exported form of safeGo for use by main and other
// packages that need panic-safe goroutines but cannot import the internal helper.
func SafeGoExported(name string, fn func()) {
	safeGo(name, fn)
}
