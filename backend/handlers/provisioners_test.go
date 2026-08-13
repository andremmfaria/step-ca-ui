package handlers

import (
	"testing"

	"step-ui/stepca"
)

// TestProvisionerMaps_Shape confirms a FakeCA-returned provisioner list maps
// into the []map[string]interface{}{"name":..., "type":...} shape
// provisioners.html:82-83 expects (index . "name" / index . "type").
func TestProvisionerMaps_Shape(t *testing.T) {
	got := provisionerMaps([]stepca.ProvisionerInfo{
		{Name: "admin", Type: "JWK"},
		{Name: "acme", Type: "ACME"},
	})

	if len(got) != 2 {
		t.Fatalf("expected 2 entries, got %d: %v", len(got), got)
	}
	if got[0]["name"] != "admin" || got[0]["type"] != "JWK" {
		t.Errorf("entry 0: got %v", got[0])
	}
	if got[1]["name"] != "acme" || got[1]["type"] != "ACME" {
		t.Errorf("entry 1: got %v", got[1])
	}
}
