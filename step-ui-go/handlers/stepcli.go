package handlers

import (
	"context"
	"fmt"
	"log"
	"os/exec"
	"regexp"
	"step-ui/config"
	"strings"
	"time"
)

// defaultStepTimeout is the bounded execution budget for step CLI calls.
// Callers may reduce it for tests; the timeout guards against hung CAs.
const defaultStepTimeout = 30 * time.Second

// stepRunner is the injectable execution function type.  The default
// implementation wraps exec.CommandContext; Wave-3 tests substitute a fake.
type stepRunner func(ctx context.Context, name string, args ...string) ([]byte, error)

// execRunner is the production stepRunner backed by exec.CommandContext.
//
//nolint:gosec // G204: subprocess launched with variable — intentional in the CLI wrapper.
func execRunner(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	return cmd.CombinedOutput()
}

// validIdentifier accepts hostnames, FQDNs, and wildcards (*.example.com).
// Anything that could be parsed as a CLI flag or contain shell metacharacters
// is rejected.  The pattern is intentionally conservative.
var validIdentifier = regexp.MustCompile(`^(\*\.)?[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?(\.[A-Za-z0-9]([A-Za-z0-9\-]*[A-Za-z0-9])?)*$`)

// validateIdentifier rejects blank values, values starting with '-', and
// anything that does not look like a hostname or wildcard hostname.
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

// redactArgs returns a copy of args with the values following sensitive flags
// replaced by "<redacted>", suitable for debug logging.
func redactArgs(args []string) []string {
	sensitive := map[string]bool{
		"--provisioner-password-file": true,
		"--root":                      true,
		"--ca-url":                    true,
		"--key":                       true,
	}
	out := make([]string, len(args))
	copy(out, args)
	for i := range len(out) - 1 {
		if sensitive[out[i]] {
			out[i+1] = "<redacted>"
		}
	}
	return out
}

// runStep is the single point of entry for all step/step-ca CLI invocations.
// It:
//  1. injects common flags (--ca-url, --root) from cfg;
//  2. wraps execution with a bounded context timeout;
//  3. validates any caller-supplied domain/name identifiers (positionalArgs);
//  4. places positional args after "--" to prevent flag injection;
//  5. logs the invocation at DEBUG with sensitive values redacted.
//
// positionalArgs are validated via validateIdentifier and appended after "--".
// extraFlags are inserted verbatim before "--" (the caller is responsible for
// their contents — use only trusted, hard-coded flag/value pairs).
//
// NOTE: positionalArgs is intentionally kept even though current callers pass
// nil; PR-19 test helpers will inject a fake runner and exercise domain
// validation without a live CA.
func runStep(
	ctx context.Context,
	cfg *config.Config,
	runner stepRunner,
	subcommand []string,
	extraFlags []string,
	positionalArgs []string, //nolint:unparam // PR-19 tests will pass non-nil values
) ([]byte, error) {
	// Validate every positional arg before constructing the command.
	for _, id := range positionalArgs {
		if err := validateIdentifier(id); err != nil {
			return nil, err
		}
	}

	// Build the full argument list:
	//   step <subcommand…> --ca-url <url> --root <cert> [extraFlags…] -- [positionalArgs…]
	args := make([]string, 0, len(subcommand)+4+len(extraFlags)+1+len(positionalArgs))
	args = append(args, subcommand...)
	args = append(args, "--ca-url", cfg.CAURL, "--root", cfg.RootCert)
	args = append(args, extraFlags...)
	if len(positionalArgs) > 0 {
		args = append(args, "--")
		args = append(args, positionalArgs...)
	}

	log.Printf("[step-cli DEBUG] step %s", strings.Join(redactArgs(args), " "))

	cctx, cancel := context.WithTimeout(ctx, defaultStepTimeout)
	defer cancel()

	out, err := runner(cctx, "step", args...)
	if cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("step CLI timed out after %s", defaultStepTimeout)
	}
	return out, err
}
