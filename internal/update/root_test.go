package update

import (
	"testing"

	"github.com/theupdateframework/go-tuf/v2/metadata"
)

// The root built into the updater is the one from the key ceremony
// (docs/runbooks/update-keys.md): it verifies itself with 2 of 3 root keys,
// and every role has the key the runbook names.
func TestShippedRootIsTheCeremonyRoot(t *testing.T) {
	raw := ShippedRoot()
	if raw == nil {
		t.Fatal("no root built in: updates would stay off")
	}
	root, err := metadata.Root().FromBytes(raw)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if err := root.VerifyDelegate(metadata.ROOT, root); err != nil {
		t.Fatalf("root does not verify itself: %v", err)
	}
	if root.Signed.Version != 1 {
		t.Errorf("version = %d, want 1", root.Signed.Version)
	}
	want := map[string]struct{ keys, threshold int }{
		metadata.ROOT:      {3, 2},
		metadata.TARGETS:   {1, 1},
		metadata.SNAPSHOT:  {1, 1},
		metadata.TIMESTAMP: {1, 1},
	}
	for name, w := range want {
		role := root.Signed.Roles[name]
		if role == nil || len(role.KeyIDs) != w.keys || role.Threshold != w.threshold {
			t.Errorf("role %s = %+v, want %d keys, threshold %d", name, role, w.keys, w.threshold)
		}
	}
	if n := len(root.Signed.Keys); n != 6 {
		t.Errorf("%d keys, want 6 distinct keys", n)
	}
}
