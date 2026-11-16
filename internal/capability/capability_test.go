package capability_test

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/getkin/kin-openapi/openapi3"

	"github.com/camhive/hive/internal/capability"
)

// Every endpoint a Capability names is in api/hive-openapi.yaml (and the
// hiveapp contract test proves the mux serves every documented one), so
// the list cannot claim what the API does not serve (ADR-0022).
func TestEveryCapabilityMapsToDocumentedEndpoints(t *testing.T) {
	doc, err := openapi3.NewLoader().LoadFromFile(filepath.Join("..", "..", "api", "hive-openapi.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range capability.All() {
		if len(c.Endpoints) == 0 {
			t.Errorf("%s names no endpoint", c.Name)
		}
		for _, e := range c.Endpoints {
			method, path, ok := strings.Cut(e, " ")
			item := doc.Paths.Find(path)
			if !ok || item == nil || item.GetOperation(method) == nil {
				t.Errorf("%s: %q is not in api/hive-openapi.yaml", c.Name, e)
			}
		}
	}
}

// Names are never removed or renamed (ADR-0022). A ratchet: every name the
// list has had stays, including names whose feature ships in the next
// release (local_login and motion with v0.1.6). Add a name here when it
// joins the list.
func TestShippedNamesStay(t *testing.T) {
	shipped := []string{"archive.delete", "archive.encryption", "camera.identity", "local_login", "motion", "updates"}
	names := capability.Names()
	for _, n := range shipped {
		if !slices.Contains(names, n) {
			t.Errorf("capability %q was removed or renamed; an incompatible change needs a new name", n)
		}
	}
}

func TestNamesAreUniqueAndSorted(t *testing.T) {
	names := capability.Names()
	if !slices.IsSorted(names) || len(slices.Compact(slices.Clone(names))) != len(names) {
		t.Errorf("names must be unique and sorted: %v", names)
	}
}
