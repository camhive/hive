package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

// #147: the owner's automatic-lock settings, off on a fresh hive, kept
// across a restart.
func TestAutolockSettingsAreOffUntilSetAndPersist(t *testing.T) {
	ctx := context.Background()
	st := open(t)
	if got, err := st.Autolock().Get(ctx); err != nil || got != (store.AutolockSettings{}) {
		t.Fatalf("fresh hive: %+v, %v; want both off", got, err)
	}
	for _, s := range []store.AutolockSettings{{After: time.Hour, Away: 10 * time.Minute}, {After: 7 * 24 * time.Hour}} {
		if err := st.Autolock().Set(ctx, s); err != nil {
			t.Fatal(err)
		}
		if got, err := st.Autolock().Get(ctx); err != nil || got != s {
			t.Fatalf("got %+v, %v; want %+v", got, err, s)
		}
	}
}
