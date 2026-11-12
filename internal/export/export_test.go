package export_test

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/store"
)

var ctx = context.Background()

func jobs(t *testing.T) *store.Exports {
	t.Helper()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st.Exports()
}

func fetchOK(_ context.Context, _ export.Request) (io.ReadCloser, error) {
	return io.NopCloser(strings.NewReader("\x00\x00\x00\x18ftypiso5" + strings.Repeat("x", 1000))), nil
}

func TestJobRunsToDoneAndFileIsDownloadable(t *testing.T) {
	dir := t.TempDir()
	m := export.NewManager(dir, fetchOK, jobs(t))
	req := export.Request{Camera: "cam_a", Stream: "main", Start: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Duration: 2 * time.Minute, Owner: "acc_1"}
	job, err := m.Start(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(job.ID, "exp_") || job.Status != export.StatusRunning {
		t.Fatalf("job = %+v", job)
	}
	final := waitStatus(t, m, job.ID, export.StatusDone)
	if final.Bytes != 1012 || final.FileName != "cam_a_main_2026-11-09T10-00-00Z_120s.mp4" {
		t.Fatalf("final = %+v", final)
	}
	path, err := m.FilePath(ctx, job.ID)
	if err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(path)
	if len(data) != 1012 || string(data[4:8]) != "ftyp" {
		t.Fatalf("file content wrong: %d bytes", len(data))
	}
	if filepath.Dir(path) != dir {
		t.Fatalf("file must live in the export dir, got %s", path)
	}
}

func TestJobFailsWhenFetchFailsAndLeavesNoPartialFile(t *testing.T) {
	dir := t.TempDir()
	m := export.NewManager(dir, func(context.Context, export.Request) (io.ReadCloser, error) {
		return nil, errors.New("engine down")
	}, jobs(t))
	job, _ := m.Start(context.Background(), export.Request{Camera: "c", Stream: "main", Start: time.Now(), Duration: time.Second, Owner: "a"})
	final := waitStatus(t, m, job.ID, export.StatusFailed)
	if final.Error == "" {
		t.Fatal("failed job must carry an error")
	}
	entries, _ := os.ReadDir(dir)
	if len(entries) != 0 {
		t.Fatalf("partial files left behind: %v", entries)
	}
	if _, err := m.FilePath(ctx, job.ID); !errors.Is(err, export.ErrNotReady) {
		t.Fatalf("FilePath on failed job: %v", err)
	}
}

func TestRequestValidation(t *testing.T) {
	m := export.NewManager(t.TempDir(), fetchOK, jobs(t))
	bad := []export.Request{
		{Stream: "main", Start: time.Now(), Duration: time.Second, Owner: "a"},
		{Camera: "c", Stream: "main", Start: time.Now(), Duration: 0, Owner: "a"},
		{Camera: "c", Stream: "main", Start: time.Now(), Duration: 25 * time.Hour, Owner: "a"},
		{Camera: "c", Stream: "third", Start: time.Now(), Duration: time.Second, Owner: "a"},
		{Camera: "c", Stream: "main", Duration: time.Second, Owner: "a"},
	}
	for i, r := range bad {
		if _, err := m.Start(context.Background(), r); !errors.Is(err, export.ErrInvalid) {
			t.Errorf("case %d: %v", i, err)
		}
	}
}

func TestGetUnknownAndListByOwner(t *testing.T) {
	m := export.NewManager(t.TempDir(), fetchOK, jobs(t))
	if _, err := m.Get(ctx, "exp_nope"); !errors.Is(err, export.ErrNotFound) {
		t.Fatalf("unknown: %v", err)
	}
	a, _ := m.Start(context.Background(), export.Request{Camera: "c", Stream: "main", Start: time.Now(), Duration: time.Second, Owner: "acc_a"})
	_, _ = m.Start(context.Background(), export.Request{Camera: "c", Stream: "main", Start: time.Now(), Duration: time.Second, Owner: "acc_b"})
	waitStatus(t, m, a.ID, export.StatusDone)
	if list, err := m.List(ctx, "acc_a"); err != nil || len(list) != 1 || list[0].ID != a.ID {
		t.Fatalf("list = %+v", list)
	}
}

func waitStatus(t *testing.T, m *export.Manager, id string, want export.Status) export.Job {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		j, err := m.Get(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if j.Status == want {
			return j
		}
		if j.Status == export.StatusFailed && want != export.StatusFailed {
			t.Fatalf("job failed: %s", j.Error)
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("job %s did not reach %s", id, want)
	return export.Job{}
}

// #59: the list survives a restart, and the file can still be downloaded.
func TestJobsSurviveARestart(t *testing.T) {
	dir, repo := t.TempDir(), jobs(t)
	m := export.NewManager(dir, fetchOK, repo)
	job, err := m.Start(ctx, export.Request{Camera: "cam_a", Stream: "sub", Start: time.Date(2026, 11, 9, 10, 0, 0, 0, time.UTC), Duration: time.Minute, Owner: "acc_1"})
	if err != nil {
		t.Fatal(err)
	}
	done := waitStatus(t, m, job.ID, export.StatusDone)

	again := export.NewManager(dir, fetchOK, repo) // the hive restarted
	if err := again.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	list, err := again.List(ctx, "acc_1")
	if err != nil || len(list) != 1 {
		t.Fatalf("list after restart = %+v, %v", list, err)
	}
	got := list[0]
	if got.ID != done.ID || got.Status != export.StatusDone || got.Bytes != done.Bytes || got.Request != done.Request || got.FileName != done.FileName || !got.DoneAt.Equal(done.DoneAt) {
		t.Fatalf("after restart %+v, before %+v", got, done)
	}
	if _, err := again.FilePath(ctx, job.ID); err != nil {
		t.Fatalf("file after restart: %v", err)
	}
}

// A job that was running when the hive stopped cannot finish: at the next
// start it is marked failed and its partial file goes. Running Recover twice
// ends in the same state.
func TestRecoverFailsInterruptedJobsAndRemovesPartialFiles(t *testing.T) {
	dir, repo := t.TempDir(), jobs(t)
	now := time.Now().UTC()
	for _, id := range []string{"exp_cut", "exp_cut2"} {
		if err := repo.Put(ctx, store.ExportJob{ID: id, Owner: "acc_1", Camera: "c", Stream: "main", Start: now.Add(-time.Hour), Duration: time.Minute, Status: "running", FileName: "c.mp4", CreatedAt: now}); err != nil {
			t.Fatal(err)
		}
	}
	part := filepath.Join(dir, ".exp_cut-123.part")
	_ = os.WriteFile(part, []byte("half"), 0o600)
	part2 := filepath.Join(dir, ".exp_cut2-456.part")
	_ = os.WriteFile(part2, []byte("half"), 0o600)
	keep := filepath.Join(dir, "exp_other.mp4")
	_ = os.WriteFile(keep, []byte("done"), 0o600)
	m := export.NewManager(dir, fetchOK, repo)
	check := func(when string) {
		t.Helper()
		for _, id := range []string{"exp_cut", "exp_cut2"} {
			j, err := m.Get(ctx, id)
			if err != nil || j.Status != export.StatusFailed || !strings.Contains(j.Error, "restarted") || j.DoneAt.IsZero() {
				t.Fatalf("%s: interrupted job = %+v, %v", when, j, err)
			}
		}
		for _, p := range []string{part, part2} {
			if _, err := os.Stat(p); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("%s: partial file %s still there: %v", when, p, err)
			}
		}
	}
	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	check("after one run")
	if err := m.Recover(ctx); err != nil {
		t.Fatal(err)
	}
	check("after a second run")
	if _, err := os.Stat(keep); err != nil {
		t.Fatalf("finished files stay: %v", err)
	}
	if err := export.NewManager(filepath.Join(dir, "missing"), fetchOK, repo).Recover(ctx); err != nil {
		t.Fatalf("no export dir yet is fine: %v", err)
	}
}

// #59: Prune removes old exports, file and list entry, and keeps fresh ones.
func TestPruneRemovesOldExportsFileAndRow(t *testing.T) {
	dir, repo := t.TempDir(), jobs(t)
	m := export.NewManager(dir, fetchOK, repo, export.WithKeep(export.Keep{MaxAge: 24 * time.Hour}))
	old, _ := m.Start(ctx, export.Request{Camera: "c", Stream: "main", Start: time.Now(), Duration: time.Second, Owner: "acc_1"})
	waitStatus(t, m, old.ID, export.StatusDone)
	oldPath, _ := m.FilePath(ctx, old.ID)
	fresh, _ := m.Start(ctx, export.Request{Camera: "c", Stream: "main", Start: time.Now(), Duration: time.Second, Owner: "acc_1"})
	waitStatus(t, m, fresh.ID, export.StatusDone)

	// The first finished two days ago.
	row, _ := repo.Get(ctx, old.ID)
	row.DoneAt = row.DoneAt.Add(-48 * time.Hour)
	_ = repo.Put(ctx, row)

	p, err := m.Prune(ctx, time.Now())
	if err != nil || p.Jobs != 1 || p.Bytes != 1012 {
		t.Fatalf("pruned %+v, %v", p, err)
	}
	if _, err := m.Get(ctx, old.ID); !errors.Is(err, export.ErrNotFound) {
		t.Fatalf("old job still listed: %v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("old file still there: %v", err)
	}
	if _, err := m.FilePath(ctx, fresh.ID); err != nil {
		t.Fatalf("fresh export gone: %v", err)
	}
	if p, err := m.Prune(ctx, time.Now()); err != nil || p.Jobs != 0 {
		t.Fatalf("second prune %+v, %v", p, err)
	}
}
