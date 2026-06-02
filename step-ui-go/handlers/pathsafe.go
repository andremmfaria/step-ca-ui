package handlers

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// containedPath resolves candidate relative to root and verifies that the
// result is strictly inside root (not equal to it, not a sibling directory).
// It returns the cleaned absolute path or an error if the candidate escapes.
//
// The separator-boundary check (absRoot + os.PathSeparator) prevents the
// classic "sibling directory" attack where "/app/static-x" would pass a naive
// strings.HasPrefix check against root "/app/static".
//
// Resolution strategy: clean the candidate, then join it under root and
// resolve the combined path.  Any ".." components that would escape root are
// eliminated by filepath.Clean; the resulting absolute path is then compared
// against rootPrefix to confirm containment.
func containedPath(root, candidate string) (string, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolving root %q: %w", root, err)
	}

	// Ensure absRoot ends with a separator so the prefix check is
	// boundary-correct even when root itself has no trailing slash.
	rootPrefix := absRoot + string(os.PathSeparator)

	// Strip any leading separator from candidate so it is treated as
	// relative; then join under root and resolve.
	rel := filepath.Clean(candidate)
	// filepath.Clean returns "." for empty/current and ".." for parent;
	// both must be rejected before joining.
	if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("path %q escapes root %q", candidate, root)
	}
	// filepath.Join + filepath.Abs: if rel is absolute, Join will ignore root.
	// To prevent absolute-path bypass, check for leading separator before joining.
	if filepath.IsAbs(rel) {
		return "", fmt.Errorf("absolute path %q not permitted; must be relative to root", candidate)
	}

	joined := filepath.Join(absRoot, rel)
	absCandidate, err := filepath.Abs(joined)
	if err != nil {
		return "", fmt.Errorf("resolving candidate %q: %w", candidate, err)
	}

	if !strings.HasPrefix(absCandidate, rootPrefix) {
		return "", fmt.Errorf("path %q escapes root %q", candidate, root)
	}
	return absCandidate, nil
}

// safeNameRe allows only characters that are safe in a filename on all
// target platforms.  Forward slashes, back-slashes, null bytes, and path
// separators are all excluded.
var safeNameRe = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// safeName validates that name contains only allowlisted characters
// ([A-Za-z0-9._-]) and that filepath.Base does not change it (which would
// indicate a directory component or a leading/trailing dot).
//
// Returns an error for empty names, names with path separators or "..",
// and names that would be altered by filepath.Base.
func safeName(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("name must not be empty")
	}
	// Explicitly reject dot-only names before the regex check, because ".."
	// matches the allowlist pattern ([A-Za-z0-9._-]+) but is a path traversal.
	if name == "." || name == ".." {
		return "", fmt.Errorf("name %q is a reserved path component", name)
	}
	if !safeNameRe.MatchString(name) {
		return "", fmt.Errorf("name %q contains disallowed characters (only A-Za-z0-9._- are permitted)", name)
	}
	clean := filepath.Base(name)
	if clean != name {
		return "", fmt.Errorf("name %q is not a clean filename (filepath.Base changed it to %q)", name, clean)
	}
	return clean, nil
}
