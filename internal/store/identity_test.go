package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// ADR-0017: the hive's background identity and address updates touch only
// their own columns, so an owner's concurrent edit is not lost.
func TestIdentityAndAddressUpdatesLeaveOtherFieldsAlone(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	r := s.Cameras()
	c := sample(t, "Front")
	if err := r.Put(ctx, c); err != nil {
		t.Fatal(err)
	}
	stale := c // what a background task read
	renamed, _ := c.Update(camera.Patch{Name: ptr("Gate")}, time.Now())
	if err := r.Put(ctx, renamed); err != nil { // the owner's edit meanwhile
		t.Fatal(err)
	}

	id := camera.Identity{MAC: "aa:bb:cc:00:11:22"}
	if err := r.SetIdentity(ctx, stale.ID, id, camera.NetworkLocal); err != nil {
		t.Fatal(err)
	}
	moved, err := stale.MoveTo("10.0.0.23", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.SetAddress(ctx, moved); err != nil {
		t.Fatal(err)
	}
	got, _ := r.Get(ctx, c.ID)
	if got.Name != "Gate" || got.Identity != id || got.Network != camera.NetworkLocal ||
		got.Source.Address != "rtsp://10.0.0.23/live" || got.Streams[camera.Sub].URL != "rtsp://10.0.0.23/live2" || got.Credentials != c.Credentials {
		t.Fatalf("got %+v", got)
	}
}

func ptr[T any](v T) *T { return &v }
