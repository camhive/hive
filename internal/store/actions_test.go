package store_test

import (
	"context"
	"testing"

	"github.com/camhive/hive/internal/store"
)

// RecentOf finds the newest entries of some kinds however much else was
// logged after them.
func TestRecentOfFindsKindsBehindABusyLog(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	for _, kind := range []string{"update.applied", "update.available", "update.applied"} {
		if err := s.Actions().Append(ctx, store.Action{Actor: "updater", Action: kind, Target: "hive"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 300; i++ {
		if err := s.Actions().Append(ctx, store.Action{Actor: "acc", Action: "camera.update", Target: "cam_1"}); err != nil {
			t.Fatal(err)
		}
	}
	got, err := s.Actions().RecentOf(ctx, []string{"update.applied", "update.available"}, 2)
	if err != nil || len(got) != 2 || got[0].Action != "update.applied" || got[1].Action != "update.available" || got[0].ID < got[1].ID {
		t.Fatalf("RecentOf = %+v %v", got, err)
	}
}
