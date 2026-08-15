package api

import (
	"bufio"
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
)

// secretish matches property names that must never carry a value into a
// response. It is deliberately broad: a false positive costs one line in
// openapi/secret-allowlist.txt and a review, and a false negative costs a
// credential in a JSON body.
var secretish = regexp.MustCompile(`(?i)(password|secret|token|hash|privateKey|certPath|keyPath|recovery|otpauth|dsn|databaseUrl|connectionString|args|env)`)

func readAllowlist(t *testing.T) map[string]bool {
	t.Helper()
	f, err := os.Open("../openapi/secret-allowlist.txt") //nolint:gosec // G304: a committed path constant
	if err != nil {
		t.Fatalf("open allowlist: %v", err)
	}
	defer f.Close() //nolint:errcheck // read-only

	allowed := map[string]bool{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		allowed[line] = true
	}
	if err := sc.Err(); err != nil {
		t.Fatalf("read allowlist: %v", err)
	}
	return allowed
}

// walkProperties calls visit for every property name in every schema reachable
// from the document, along with the raw schema object.
func walkProperties(node any, visit func(name string, schema map[string]any)) {
	switch v := node.(type) {
	case map[string]any:
		if props, ok := v["properties"].(map[string]any); ok {
			for name, raw := range props {
				schema, _ := raw.(map[string]any)
				visit(name, schema)
			}
		}
		for _, child := range v {
			walkProperties(child, visit)
		}
	case []any:
		for _, child := range v {
			walkProperties(child, visit)
		}
	}
}

// TestNoSecretShapedPropertiesInSpec is the check that a credential cannot
// reach the contract by accident. A name matching secretish must either not
// exist or be listed in openapi/secret-allowlist.txt, and every allowlisted
// name must be writeOnly so it can appear in a request and never in a
// response.
func TestNoSecretShapedPropertiesInSpec(t *testing.T) {
	raw, err := os.ReadFile("../openapi/openapi.json")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	allowed := readAllowlist(t)
	seen := map[string]bool{}

	walkProperties(doc, func(name string, schema map[string]any) {
		if !secretish.MatchString(name) || seen[name] {
			return
		}
		seen[name] = true

		if !allowed[name] {
			t.Errorf("property %q is secret-shaped and not in openapi/secret-allowlist.txt", name)
			return
		}
		if writeOnly, _ := schema["writeOnly"].(bool); !writeOnly {
			t.Errorf("property %q is allowlisted but not writeOnly: true, so it can appear in a response", name)
		}
	})
}

// TestNoAdditionalPropertiesTrue asserts no schema accepts arbitrary members,
// which is what keeps a generated client's types meaningful.
func TestNoAdditionalPropertiesTrue(t *testing.T) {
	raw, err := os.ReadFile("../openapi/openapi.json")
	if err != nil {
		t.Fatalf("read spec: %v", err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec: %v", err)
	}

	var walk func(node any)
	walk = func(node any) {
		switch v := node.(type) {
		case map[string]any:
			if ap, ok := v["additionalProperties"].(bool); ok && ap {
				t.Errorf("a schema declares additionalProperties: true")
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(doc)
}

// TestEveryOperationIsWellFormed asserts the naming rules every generated
// client depends on: a camelCase operationId, a summary, and a tag from the
// fixed list, because tag assignment drives the SPA's cache-invalidation map
// and an unreviewed tag silently breaks invalidation.
func TestEveryOperationIsWellFormed(t *testing.T) {
	validTags := map[string]bool{
		"session": true, "certificates": true, "acme": true, "provisioners": true,
		"security": true, "admin": true, "users": true, "profile": true,
		"system": true, "dashboard": true,
	}
	camelCase := regexp.MustCompile(`^[a-z][A-Za-z0-9]*$`)

	spec := NewForSpec().OpenAPI()
	for path, item := range spec.Paths {
		for method, op := range operationsOf(item) {
			switch {
			case op.OperationID == "":
				t.Errorf("%s %s: no operationId", method, path)
			case !camelCase.MatchString(op.OperationID):
				t.Errorf("%s %s: operationId %q is not camelCase", method, path, op.OperationID)
			}
			if op.Summary == "" {
				t.Errorf("%s %s (%s): no summary", method, path, op.OperationID)
			}
			if len(op.Tags) == 0 {
				t.Errorf("%s %s (%s): no tag", method, path, op.OperationID)
				continue
			}
			for _, tag := range op.Tags {
				if !validTags[tag] {
					t.Errorf("%s %s (%s): tag %q is not in the fixed list", method, path, op.OperationID, tag)
				}
			}
		}
	}
}
