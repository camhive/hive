package store_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/store"
)

func open(t *testing.T) *store.Store {
	t.Helper()
	path := filepath.Join(t.TempDir(), "hive.db")
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	s, err := store.Open(context.Background(), path, key)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func sample(t *testing.T, name string) camera.Camera {
	t.Helper()
	c, err := camera.New(camera.Spec{
		Name:   name,
		Group:  "Outside",
		Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://admin:topsecret@10.0.0.9/live"},
		Streams: map[camera.Stream]camera.StreamSpec{
			camera.Sub: {URL: "rtsp://10.0.0.9/live2", Codec: "H264", Width: 640, Height: 360},
		},
	}, time.Date(2026, 11, 9, 12, 0, 0, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestOpenAppliesMigrationsIdempotently(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hive.db")
	key := make([]byte, 32)
	ctx := context.Background()
	s1, err := store.Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	v1, _ := s1.SchemaVersion(ctx)
	_ = s1.Close()
	s2, err := store.Open(ctx, path, key)
	if err != nil {
		t.Fatalf("second open must not fail: %v", err)
	}
	defer s2.Close()
	v2, _ := s2.SchemaVersion(ctx)
	if v1 == 0 || v1 != v2 {
		t.Fatalf("schema version %d then %d", v1, v2)
	}
}

func TestCameraRoundTripKeepsEverythingIncludingCredentials(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	in := sample(t, "Front")
	if err := s.Cameras().Put(ctx, in); err != nil {
		t.Fatal(err)
	}
	out, err := s.Cameras().Get(ctx, in.ID)
	if err != nil {
		t.Fatal(err)
	}
	if out.ID != in.ID || out.Name != in.Name || out.Group != in.Group || out.Source != in.Source || out.Enabled != in.Enabled {
		t.Fatalf("got %+v\nwant %+v", out, in)
	}
	if out.Credentials != in.Credentials {
		t.Fatalf("credentials = %+v", out.Credentials)
	}
	if !out.CreatedAt.Equal(in.CreatedAt) || !out.UpdatedAt.Equal(in.UpdatedAt) {
		t.Fatalf("timestamps = %v %v", out.CreatedAt, out.UpdatedAt)
	}
	if len(out.Streams) != 2 || out.Streams[camera.Sub].Width != 640 || out.Streams[camera.Main].URL != "rtsp://10.0.0.9/live" {
		t.Fatalf("streams = %+v", out.Streams)
	}
}

func TestCredentialsAreEncryptedAtRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "hive.db")
	key := make([]byte, 32)
	ctx := context.Background()
	s, err := store.Open(ctx, path, key)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Cameras().Put(ctx, sample(t, "Front")); err != nil {
		t.Fatal(err)
	}
	_ = s.Close()
	raw, _ := os.ReadFile(path)
	if strings.Contains(string(raw), "topsecret") {
		t.Fatal("password stored in clear text")
	}
	// A different key cannot read the credentials.
	other := make([]byte, 32)
	other[0] = 1
	s2, err := store.Open(ctx, path, other)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	// Rows cannot be read without the right key: a copied database is useless.
	if _, err := s2.Cameras().List(ctx); !errors.Is(err, store.ErrCredentials) {
		t.Fatalf("wrong key: %v, want ErrCredentials", err)
	}
}

func TestPutIsAnUpsertAndListIsOrderedByName(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	a, b := sample(t, "Yard"), sample(t, "Front")
	for _, c := range []camera.Camera{a, b} {
		if err := s.Cameras().Put(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	name := "Back yard"
	a2, _ := a.Update(camera.Patch{Name: &name}, time.Now())
	if err := s.Cameras().Put(ctx, a2); err != nil {
		t.Fatal(err)
	}
	list, err := s.Cameras().List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 2 || list[0].Name != "Back yard" || list[1].Name != "Front" {
		t.Fatalf("list = %v", names(list))
	}
}

func TestDeleteIsIdempotentAndGetMissingIsErrNotFound(t *testing.T) {
	s := open(t)
	ctx := context.Background()
	c := sample(t, "Gone")
	_ = s.Cameras().Put(ctx, c)
	if err := s.Cameras().Delete(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Cameras().Delete(ctx, c.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := s.Cameras().Get(ctx, c.ID); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("get after delete: %v", err)
	}
}

func TestOpenRejectsBadKey(t *testing.T) {
	if _, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "x.db"), []byte("short")); err == nil {
		t.Fatal("key must be 32 bytes")
	}
}

func names(cs []camera.Camera) []string {
	out := make([]string, len(cs))
	for i, c := range cs {
		out[i] = c.Name
	}
	return out
}
