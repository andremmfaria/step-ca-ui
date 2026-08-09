package handlers

import (
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"time"

	"step-ui/config"
)

// defaultStepTimeout is the bounded execution budget for step CLI calls.
// Callers may reduce it for tests; the timeout guards against hung CAs.
//
// NOTE: this file (and everything in it) is a migration remnant. Phase 3.4
// of plans/step-cli-to-ca-lib-swap.md replaces every runStep call site with
// the stepca package; once that lands, this whole file is deleted (along
// with its matching tests) — see the plan's R8/Phase 6.1.
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
func runStep(
	ctx context.Context,
	cfg *config.Config,
	runner stepRunner,
	subcommand []string,
	extraFlags []string,
	positionalArgs []string, //nolint:unparam // remaining migration-era callers all pass nil; see file header note
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

	slog.Debug("step-cli invocation", "args", strings.Join(redactArgs(args), " "))

	cctx, cancel := context.WithTimeout(ctx, defaultStepTimeout)
	defer cancel()

	out, err := runner(cctx, "step", args...)
	if cctx.Err() == context.DeadlineExceeded {
		return out, fmt.Errorf("step CLI timed out after %s", defaultStepTimeout)
	}
	return out, err
}
