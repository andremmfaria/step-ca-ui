package api_test

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"

	humaapi "step-ui/api"
)

func marshalSpec(t *testing.T) []byte {
	t.Helper()
	spec := humaapi.NewForSpec()
	compact, err := spec.OpenAPI().MarshalJSON()
	if err != nil {
		t.Fatalf("MarshalJSON: %v", err)
	}
	var pretty bytes.Buffer
	if err := json.Indent(&pretty, compact, "", "  "); err != nil {
		t.Fatalf("Indent: %v", err)
	}
	pretty.WriteByte('\n')
	return pretty.Bytes()
}

// TestSpecGenerationIsDeterministic is Section 2's "regenerates it
// byte-identically, twice in a row" gate (7.1), run in-process.
func TestSpecGenerationIsDeterministic(t *testing.T) {
	first := marshalSpec(t)
	second := marshalSpec(t)
	if !bytes.Equal(first, second) {
		t.Fatal("openapi spec differs across two in-process generations")
	}
}

// TestSpecMatchesCommittedFile is the drift gate (7.2), run as a unit test
// rather than only as a CI shell step.
func TestSpecMatchesCommittedFile(t *testing.T) {
	committed, err := os.ReadFile("../openapi/openapi.json")
	if err != nil {
		t.Fatalf("reading committed spec: %v", err)
	}
	generated := marshalSpec(t)
	if !bytes.Equal(generated, committed) {
		t.Fatal("backend/openapi/openapi.json is stale; run `make openapi`")
	}
}

// TestSpecIsEnvironmentIndependent is D3 rule 1's enforcement: two
// materially different environments (UI_CERT_DURATION, PROVISIONER,
// ALLOWED_DOMAIN_SUFFIXES, LE_ACME_DIRECTORY_URL, plus DATABASE_URL/CA_URL
// pointed at addresses that fail loudly if ever dialed) must produce the
// same spec, because handlers.NewForSpec() builds against a zero-value
// config.Config rather than config.Load().
func TestSpecIsEnvironmentIndependent(t *testing.T) {
	envs := []map[string]string{
		{
			"UI_CERT_DURATION":        "10m",
			"PROVISIONER":             "admin",
			"ALLOWED_DOMAIN_SUFFIXES": "example.com",
			"LE_ACME_DIRECTORY_URL":   "https://acme-staging-v02.api.letsencrypt.org/directory",
			"DATABASE_URL":            "postgres://127.0.0.1:1/x",
			"CA_URL":                  "https://127.0.0.1:1",
		},
		{
			"UI_CERT_DURATION":        "72h",
			"PROVISIONER":             "other-provisioner",
			"ALLOWED_DOMAIN_SUFFIXES": "example.org,example.net",
			"LE_ACME_DIRECTORY_URL":   "https://acme-v02.api.letsencrypt.org/directory",
			"DATABASE_URL":            "postgres://127.0.0.1:2/y",
			"CA_URL":                  "https://127.0.0.1:2",
		},
	}

	var results [][]byte
	for _, env := range envs {
		for k, v := range env {
			t.Setenv(k, v)
		}
		results = append(results, marshalSpec(t))
	}
	if !bytes.Equal(results[0], results[1]) {
		t.Fatal("openapi spec differs between two materially different environments")
	}
}
