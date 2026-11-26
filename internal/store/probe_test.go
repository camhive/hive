package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/camhive/hive/internal/store"
)

// #172: the recording watchdog's write to the database succeeds on a
// healthy store, twice, and fails once the store cannot write.
func TestProbeWritesAndFailsOnAClosedStore(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if err := s.Probe(ctx); err != nil {
			t.Fatalf("Probe = %v on a healthy store", err)
		}
	}
	_ = s.Close()
	if err := s.Probe(ctx); err == nil {
		t.Fatal("Probe = nil on a closed store")
	}
}
