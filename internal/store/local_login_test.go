package store_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/store"
)

// #57: the local login is one row that exists only while it is enabled.
func TestLocalLoginExistsOnlyWhileEnabled(t *testing.T) {
	ctx := context.Background()
	r := open(t).LocalLogin()
	if _, err := r.Get(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("fresh hive: err = %v, want ErrNotFound", err)
	}
	at := time.Date(2026, 11, 16, 9, 0, 0, 0, time.UTC)
	for _, c := range []store.LocalCredential{{Username: "anna", Hash: "$argon2id$one", UpdatedAt: at}, {Username: "ben", Hash: "$argon2id$two", UpdatedAt: at.Add(time.Hour)}} {
		if err := r.Set(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	got, err := r.Get(ctx)
	if err != nil || got.Username != "ben" || got.Hash != "$argon2id$two" || !got.UpdatedAt.Equal(at.Add(time.Hour)) {
		t.Fatalf("got %+v, %v; want the second credential replacing the first", got, err)
	}
	for range 2 { // removing twice ends in the same state
		if err := r.Delete(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := r.Get(ctx); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("after delete: err = %v, want ErrNotFound", err)
	}
}
