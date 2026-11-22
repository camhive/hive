package health_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/health"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/video"
)

// stage plays a camsim Scenario to the hive's parts on a fake clock: the
// engine's status as the Monitor polls it, and the index's segments as the
// engine writes them (a new file every segment and after every drop); the
// index lists the file being written too, refreshed every minute.
type stage struct {
	w       *world
	eng     *statusEngine
	sc      camsim.Scenario
	start   time.Time
	cam     string
	segment time.Duration
	bytes   int64 // the engine's counter; a new session starts it again
	open    *store.SegmentSize
	wasUp   bool
}

func (s *stage) step(d time.Duration) {
	s.w.advance(d)
	now := s.w.Now()
	at := now.Sub(s.start)
	up := s.sc.UpAt(at)
	switch {
	case up && s.open == nil:
		s.open = &store.SegmentSize{Start: now}
		s.bytes = 0
	case up && now.Sub(s.open.Start) >= s.segment:
		s.close(now)
		s.open = &store.SegmentSize{Start: now}
	case !up && s.open != nil:
		s.close(now)
		s.open = nil
	}
	if up {
		n := int64(s.sc.KbpsAt(at)) * 1000 / 8 * int64(d/time.Second)
		s.open.Bytes += n
		s.bytes += n
	}
	if s.open != nil && now.Sub(s.start)%time.Minute == 0 {
		s.index(*s.open, now)
	}
	s.eng.set(s.cam, "main", video.Status{Ready: up, BytesReceived: s.bytes, Tracks: []string{"H264"},
		Video: video.Track{Codec: "H264", Width: 1920, Height: 1080}}, nil)
	s.wasUp = up
}

func (s *stage) close(now time.Time) { s.index(*s.open, now) }

// index upserts a segment row as the archive index does.
func (s *stage) index(row store.SegmentSize, now time.Time) {
	row.Duration = now.Sub(row.Start)
	s.w.mu.Lock()
	defer s.w.mu.Unlock()
	rows := s.w.segs[s.cam+"/main"]
	if n := len(rows); n > 0 && rows[n-1].Start.Equal(row.Start) {
		rows[n-1] = row
		return
	}
	s.w.segs[s.cam+"/main"] = append(rows, row)
}

// The issue's scenario (#163): a camera that halves its bitrate and
// reconnects every 5 minutes is degraded with bitrate_drop and reconnects
// within an hour; once it is well again the episode ends, and there was
// exactly one.
func TestCareScenarioHalvedBitrateAndReconnects(t *testing.T) {
	start := time.Date(2026, 11, 18, 7, 0, 0, 0, time.UTC)
	sc := camsim.Scenario{Kbps: 4000, Trouble: 5 * time.Hour, Recover: 7 * time.Hour, BitrateFactor: 0.5, ReconnectEvery: 5 * time.Minute}
	c0, err := camera.New(camera.Spec{Name: "front", Source: camera.Source{Kind: camera.SourceRTSP, Address: "rtsp://h/front"}}, start.Add(-48*time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	id := string(c0.ID)
	w := newWorld(start)
	cams := &memStore{cams: []camera.Camera{c0}}
	s := &stage{w: w, eng: &statusEngine{status: map[string]video.Status{}, err: map[string]error{}}, sc: sc, start: start, cam: id, segment: 10 * time.Minute}
	w.profile(id, "main", start.AddDate(0, 0, -3), start, flat(sc.Kbps)) // three healthy days before
	care := health.NewCare(health.CareSources{Cameras: cams, Segments: w, Events: events{w}},
		health.CareOptions{Now: w.Now, Started: start.Add(-time.Hour), Location: time.UTC, Segment: s.segment, OnDegraded: w.onDegraded, OnRecovered: w.onRecovered})
	mon := health.New(cams, s.eng, health.Options{Now: w.Now, OnSample: care.Observe,
		OnEvent: func(e health.Event) { w.event(e.Camera, e.Kind, e.At) }})
	ctx := context.Background()
	run := func(until time.Duration) {
		for w.Now().Sub(start) < until {
			s.step(5 * time.Second)
			if err := mon.Poll(ctx); err != nil {
				t.Fatal(err)
			}
			if w.Now().Sub(start)%(5*time.Minute) == 0 {
				if err := care.Check(ctx); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	run(sc.Trouble)
	if got := care.Camera(id); got.Degraded || len(w.since) != 0 {
		t.Fatalf("healthy camera degraded: %+v", got)
	}
	run(sc.Trouble + time.Hour)
	got := care.Camera(id)
	reasons := reasonsOf(got)
	if len(w.since) != 1 || w.since[0].Sub(start) > sc.Trouble+time.Hour ||
		!slices.Contains(reasons, health.BitrateDrop) || !slices.Contains(reasons, health.Reconnects) {
		t.Fatalf("an hour into the trouble: episodes at %v, care %+v", w.since, got)
	}
	if got.Signals.ReconnectsHour < 11 || got.Signals.Streams[0].UsualKbps != 4000 {
		t.Fatalf("signals %+v", got.Signals)
	}
	t.Logf("degraded %s after the trouble began: %v", w.since[0].Sub(start.Add(sc.Trouble)), reasons)
	run(sc.Recover + 90*time.Minute)
	if got := care.Camera(id); got.Degraded || w.recovered != 1 || len(w.since) != 1 {
		t.Fatalf("after recovery: %+v; episodes %d, recoveries %d", got, len(w.since), w.recovered)
	}
}
