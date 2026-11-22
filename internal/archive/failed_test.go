package archive_test

import (
	"context"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
)

// #155: a reconcile that cannot write the index is told to the disk's
// health watch, not only logged.
func TestAFailedReconcileIsReported(t *testing.T) {
	st := openStore(t)
	failed := make(chan error, 1)
	idx := archive.NewIndex(st, archive.WithFailed(func(err error) { failed <- err }))
	_ = st.Close() // every write now fails
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go idx.Run(ctx, t.TempDir(), time.Hour)
	select {
	case err := <-failed:
		if err == nil {
			t.Fatal("reported a nil error")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the failed reconcile was not reported")
	}
}
