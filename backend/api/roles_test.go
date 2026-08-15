package api

import (
	"bufio"
	"fmt"
	"os"
	"sort"
	"strings"
	"testing"

	appmw "step-ui/middleware"

	"github.com/danielgtaylor/huma/v2"
)

// goldenRow is one line of testdata/roles.golden.
type goldenRow struct {
	method, path, auth, role, tag, csrf, ratelimit, retired string
}

func (g goldenRow) String() string {
	return fmt.Sprintf("%s %s auth=%s role=%s tag=%s csrf=%s ratelimit=%s templateRouteRetired=%s",
		g.method, g.path, g.auth, g.role, g.tag, g.csrf, g.ratelimit, g.retired)
}

func readGolden(t *testing.T, path string) []goldenRow {
	t.Helper()
	f, err := os.Open(path) //nolint:gosec // G304: path is a test constant naming a committed golden file
	if err != nil {
		t.Fatalf("open %s: %v", path, err)
	}
	defer f.Close() //nolint:errcheck // read-only

	var rows []goldenRow
	sc := bufio.NewScanner(f)
	for line := 1; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" || strings.HasPrefix(text, "#") {
			continue
		}
		fields := strings.Fields(text)
		if len(fields) != 8 {
			t.Fatalf("%s:%d: want 8 fields, got %d: %q", path, line, len(fields), text)
		}
		kv := map[string]string{}
		for _, f := range fields[2:] {
			k, v, ok := strings.Cut(f, "=")
			if !ok {
				t.Fatalf("%s:%d: field %q is not key=value", path, line, f)
			}
			kv[k] = v
		}
		rows = append(rows, goldenRow{
			method: fields[0], path: fields[1],
			auth: kv["auth"], role: kv["role"], tag: kv["tag"],
			csrf: kv["csrf"], ratelimit: kv["ratelimit"], retired: kv["templateRouteRetired"],
		})
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return rows
}

// registeredRows walks the in-process API's registered operations and derives
// the same row shape from the metadata the chain actually enforces.
func registeredRows(t *testing.T) []goldenRow {
	t.Helper()
	spec := NewForSpec().OpenAPI()

	var rows []goldenRow
	for path, item := range spec.Paths {
		for method, op := range operationsOf(item) {
			row := goldenRow{method: method, path: path, retired: "true"}

			switch opAuth(op) {
			case authPublic:
				row.auth, row.role = "public", "-"
			case authOptional:
				row.auth, row.role = "optional", "-"
			default:
				role, declared := opRole(op)
				if !declared {
					t.Errorf("%s %s: registered with neither a role nor an auth mode; roleMiddleware would answer 403", method, path)
					continue
				}
				row.auth, row.role = "required", role
			}

			row.tag = "-"
			if len(op.Tags) > 0 {
				row.tag = op.Tags[0]
			}
			row.csrf = "no"
			if !safeMethod(method) {
				row.csrf = "yes"
			}
			row.ratelimit = "no"
			if scope, _ := op.Metadata[metaRateLimit].(string); scope == rateLimitAuth {
				row.ratelimit = "yes"
			}
			rows = append(rows, row)
		}
	}
	return rows
}

// operationsOf returns the non-nil operations on a path item, keyed by method.
// Several tests walk the registered set, and each would otherwise repeat the
// same method literal, which is how one of them ends up silently skipping a
// method nobody notices is missing.
func operationsOf(item *huma.PathItem) map[string]*huma.Operation {
	all := map[string]*huma.Operation{
		"GET": item.Get, "POST": item.Post, "PUT": item.Put,
		"PATCH": item.Patch, "DELETE": item.Delete,
		"HEAD": item.Head, "OPTIONS": item.Options, "TRACE": item.Trace,
	}
	for method, op := range all {
		if op == nil {
			delete(all, method)
		}
	}
	return all
}

func sortRows(rows []goldenRow) {
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].method != rows[j].method {
			return rows[i].method < rows[j].method
		}
		return rows[i].path < rows[j].path
	})
}

// TestRoleMatrix is the gate that makes the role declaration load-bearing:
// every registered operation must appear in the golden table with the same
// auth mode, role, tag, CSRF applicability and rate-limit scope. Adding an
// operation without a role, or changing one without updating the table, fails
// the build rather than shipping a spec that documents a posture the runtime
// does not enforce.
//
// templateRouteRetired is carried by the golden file alone: it records
// migration progress, not runtime behaviour, so it is compared only for
// well-formedness.
func TestRoleMatrix(t *testing.T) {
	want := readGolden(t, "../testdata/roles.golden")
	got := registeredRows(t)
	sortRows(want)
	sortRows(got)

	byKey := map[string]goldenRow{}
	for _, w := range want {
		byKey[w.method+" "+w.path] = w
	}

	for _, g := range got {
		key := g.method + " " + g.path
		w, ok := byKey[key]
		if !ok {
			t.Errorf("operation %s is registered but absent from testdata/roles.golden:\n  %s", key, g)
			continue
		}
		g.retired = w.retired // migration bookkeeping, not runtime behaviour
		if g != w {
			t.Errorf("operation %s disagrees with testdata/roles.golden:\n  registered: %s\n  golden:     %s", key, g, w)
		}
		delete(byKey, key)
	}
	for key, w := range byKey {
		t.Errorf("testdata/roles.golden carries %s, which is not registered:\n  %s", key, w)
	}
}

// TestOptionalAuthIsOnlyGetSession enforces 5.5's exhaustiveness rule: exactly
// one operation may validate a session without ever answering 401, because
// every additional one is a route that silently degrades to anonymous.
func TestOptionalAuthIsOnlyGetSession(t *testing.T) {
	spec := NewForSpec().OpenAPI()
	for path, item := range spec.Paths {
		for method, op := range operationsOf(item) {
			if opAuth(op) != authOptional {
				continue
			}
			if op.OperationID != "getSession" {
				t.Errorf("%s %s (%s) declares auth: optional; only getSession may", method, path, op.OperationID)
			}
		}
	}
}

// TestRoleRepresentationsAgree asserts roleOp's three outputs never drift:
// the runtime metadata the chain reads, the x-required-role extension a
// reader sees, and the Security entry the document carries.
func TestRoleRepresentationsAgree(t *testing.T) {
	spec := NewForSpec().OpenAPI()
	for path, item := range spec.Paths {
		for method, op := range operationsOf(item) {
			role, declared := opRole(op)
			ext, hasExt := op.Extensions["x-required-role"].(string)

			if !declared {
				if hasExt {
					t.Errorf("%s %s: x-required-role=%q with no runtime metadata; the document would claim an unenforced posture", method, path, ext)
				}
				if len(op.Security) != 0 {
					t.Errorf("%s %s: carries a security requirement with no role metadata", method, path)
				}
				continue
			}
			if !hasExt || ext != role {
				t.Errorf("%s %s: metadata role=%q but x-required-role=%q", method, path, role, ext)
			}
			if len(op.Security) != 1 || len(op.Security[0][securitySchemeName]) != 1 || op.Security[0][securitySchemeName][0] != role {
				t.Errorf("%s %s: metadata role=%q but security entry is %v", method, path, role, op.Security)
			}
			if _, known := appmw.RoleLevels[role]; !known {
				t.Errorf("%s %s: role %q is not in middleware.RoleLevels, so RoleAllows denies it unconditionally", method, path, role)
			}
		}
	}
}

// TestRateLimitedSetsTheScopeTheMiddlewareReads pins the contract between the
// declaration helper and rateLimitMiddleware. They are the only two places
// that name the scope, and a change to one that misses the other silently
// unscopes the login rate limiter.
func TestRateLimitedSetsTheScopeTheMiddlewareReads(t *testing.T) {
	op := rateLimited(huma.Operation{OperationID: "createSession"})
	got, _ := op.Metadata[metaRateLimit].(string)
	if got != rateLimitAuth {
		t.Fatalf("rateLimited set %s=%q, want %q", metaRateLimit, got, rateLimitAuth)
	}
}

// TestNoOperationIsRateLimitedYet records why rateLimitMiddleware has no
// end-to-end test in this phase: the four operations 5.5 scopes it to
// (createSession, submitMfa, requestPasswordReset, confirmPasswordReset) do
// not exist until the auth domain is ported. This test fails when the first
// one lands, which is the moment to write that coverage.
func TestNoOperationIsRateLimitedYet(t *testing.T) {
	for _, row := range registeredRows(t) {
		if row.ratelimit == "yes" {
			t.Fatalf("%s %s is rate-limited; add end-to-end coverage of rateLimitMiddleware and delete this test", row.method, row.path)
		}
	}
}
