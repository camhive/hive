package hiveapp_test

import (
	"net/http"
	"slices"
	"testing"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/capability"
	"github.com/camhive/hive/internal/hiveapp"
)

// ADR-0022: any Member asks the hive what it can do; the answer is the
// version and the one capability list.
func TestHiveInfoTellsEveryMemberTheVersionAndCapabilities(t *testing.T) {
	f := newAPIWith(t, func(d *hiveapp.Deps) { d.Version = "0.1.6" })
	if res := f.do(t, http.MethodGet, "/v0/hive", "", nil); res.Status != http.StatusUnauthorized {
		t.Fatalf("without a token: %d", res.Status)
	}
	res := f.do(t, http.MethodGet, "/v0/hive", f.token(t, auth.RoleViewer, "cam_x"), nil)
	if res.Status != http.StatusOK || res.Body["version"] != "0.1.6" {
		t.Fatalf("GET /v0/hive = %d %v", res.Status, res.Body)
	}
	listed, _ := res.Body["capabilities"].([]any)
	got := make([]string, 0, len(listed))
	for _, c := range listed {
		got = append(got, c.(string))
	}
	if !slices.Equal(got, capability.Names()) || !slices.Contains(got, "archive.delete") {
		t.Fatalf("capabilities = %v, want %v", got, capability.Names())
	}
}
