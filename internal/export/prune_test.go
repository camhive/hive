package export

import (
	"slices"
	"testing"
	"time"
)

func job(id string, st Status, doneHoursAgo int, bytes int64, now time.Time) Job {
	j := Job{ID: id, Status: st, Bytes: bytes}
	if st != StatusRunning {
		j.DoneAt = now.Add(-time.Duration(doneHoursAgo) * time.Hour)
	}
	return j
}

func ids(js []Job) []string {
	out := make([]string, 0, len(js))
	for _, j := range js {
		out = append(out, j.ID)
	}
	slices.Sort(out)
	return out
}

// #59: finished exports older than the age limit go; then, while the
// finished files take more than the size limit, the oldest go; running jobs
// are never touched.
func TestExpiredPicksOldJobsThenOldestUntilUnderTheCap(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	week := 7 * 24 * time.Hour
	jobs := []Job{
		job("old-done", StatusDone, 8*24, 100, now),
		job("old-failed", StatusFailed, 8*24, 0, now),
		job("old-running", StatusRunning, 0, 0, now),
		job("a", StatusDone, 72, 400, now),
		job("b", StatusDone, 48, 300, now),
		job("c", StatusDone, 1, 200, now),
		job("fresh-failed", StatusFailed, 1, 0, now),
	}
	for _, c := range []struct {
		name string
		keep Keep
		want []string
	}{
		{"nothing set", Keep{}, nil},
		{"age only", Keep{MaxAge: week}, []string{"old-done", "old-failed"}},
		{"cap only: 1000 bytes of done files, keep 600", Keep{MaxBytes: 600}, []string{"a", "old-done"}},
		{"cap exactly met", Keep{MaxBytes: 1000}, nil},
		{"age then cap: 900 left after age, keep 500", Keep{MaxAge: week, MaxBytes: 500}, []string{"a", "old-done", "old-failed"}},
	} {
		if got := ids(expired(jobs, now, c.keep)); !slices.Equal(got, c.want) {
			t.Errorf("%s: %v, want %v", c.name, got, c.want)
		}
	}
}

func TestExpiredAgeIsMeasuredFromCompletion(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	edge := []Job{{ID: "edge", Status: StatusDone, DoneAt: now.Add(-24 * time.Hour)}, {ID: "over", Status: StatusDone, DoneAt: now.Add(-24*time.Hour - time.Second)}}
	if got := ids(expired(edge, now, Keep{MaxAge: 24 * time.Hour})); !slices.Equal(got, []string{"over"}) {
		t.Fatalf("expired = %v", got)
	}
}
