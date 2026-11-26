package hiveagent_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveagent"
	"github.com/camhive/hive/internal/protocol"
)

// alarmSink acknowledges events after the first fail attempts.
type alarmSink struct {
	mu   sync.Mutex
	fail int
	got  []protocol.Event // every attempt
}

func (s *alarmSink) emit(_ context.Context, e protocol.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.got = append(s.got, e)
	if s.fail > 0 {
		s.fail--
		return errors.New("not acknowledged")
	}
	return nil
}

func (s *alarmSink) names() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]string, len(s.got))
	for i, e := range s.got {
		out[i] = e.Name
	}
	return out
}

func waitAttempts(t *testing.T, s *alarmSink, n int) []string {
	t.Helper()
	for d := time.Now().Add(3 * time.Second); time.Now().Before(d); time.Sleep(5 * time.Millisecond) {
		if got := s.names(); len(got) >= n {
			return got
		}
	}
	t.Fatalf("attempts = %v, want %d", s.names(), n)
	return nil
}

// #172: the alarm lane keeps an event in memory and sends it again until
// the cloud acknowledges it, in order, without the store.
func TestAlarmsAreSentAgainUntilAcknowledged(t *testing.T) {
	a := hiveagent.New(hiveagent.Deps{}) // no store at all
	sink := &alarmSink{fail: 2}
	at := time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC)
	a.EmitAlarm("recording.stopped", at, map[string]any{"reason": "archive_missing"})
	a.EmitAlarm("recording.resumed", at.Add(time.Hour), map[string]any{})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunAlarms(ctx, sink.emit, 5*time.Millisecond)

	got := waitAttempts(t, sink, 4)
	want := []string{"recording.stopped", "recording.stopped", "recording.stopped", "recording.resumed"}
	if fmt.Sprint(got[:4]) != fmt.Sprint(want) {
		t.Fatalf("attempts = %v, want %v", got, want)
	}
	time.Sleep(30 * time.Millisecond)
	if n := len(sink.names()); n != 4 {
		t.Fatalf("%d attempts, want nothing after both were acknowledged", n)
	}
	sink.mu.Lock()
	first := sink.got[0]
	sink.mu.Unlock()
	var data struct{ Reason string }
	if !first.At.Equal(at) || json.Unmarshal(first.Data, &data) != nil || data.Reason != "archive_missing" {
		t.Fatalf("first = %+v, want at %v with the reason", first, at)
	}
	if a.PendingAlarms() != 0 {
		t.Fatalf("pending = %d, want 0", a.PendingAlarms())
	}
}

// An alarm made while the lane is running is sent at once, not at the
// next retry.
func TestANewAlarmWakesTheLane(t *testing.T) {
	a := hiveagent.New(hiveagent.Deps{})
	sink := &alarmSink{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunAlarms(ctx, sink.emit, time.Hour)
	time.Sleep(10 * time.Millisecond)
	a.EmitAlarm("recording.stopped", time.Now(), nil)
	waitAttempts(t, sink, 1)
}

// The lane keeps the newest MaxAlarms changes: a flapping disk cannot
// grow it without bound.
func TestTheAlarmLaneKeepsTheNewest(t *testing.T) {
	a := hiveagent.New(hiveagent.Deps{})
	for i := range hiveagent.MaxAlarms + 4 {
		a.EmitAlarm(fmt.Sprintf("alarm.%d", i), time.Now(), nil)
	}
	if a.PendingAlarms() != hiveagent.MaxAlarms {
		t.Fatalf("pending = %d, want %d", a.PendingAlarms(), hiveagent.MaxAlarms)
	}
	sink := &alarmSink{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunAlarms(ctx, sink.emit, time.Millisecond)
	got := waitAttempts(t, sink, hiveagent.MaxAlarms)
	if got[0] != "alarm.4" {
		t.Fatalf("first sent = %s, want alarm.4 (the oldest four dropped)", got[0])
	}
}

// #172 review: a cloud that never acknowledges is asked less and less
// often (from retry up to 30 times it), not every retry forever.
func TestAnUnacknowledgedAlarmBacksOff(t *testing.T) {
	a := hiveagent.New(hiveagent.Deps{})
	sink := &alarmSink{fail: 1 << 30}
	a.EmitAlarm("recording.stopped", time.Now(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	go a.RunAlarms(ctx, sink.emit, 5*time.Millisecond)
	time.Sleep(400 * time.Millisecond)
	cancel()
	if n := len(sink.names()); n < 3 || n > 12 {
		t.Fatalf("%d attempts in 400 ms with a 5 ms retry, want a few (backing off to 150 ms), not ~80", n)
	}
}

// A reconnect (or a new alarm) asks again at once and starts the backoff
// over.
func TestAReconnectWakesTheLane(t *testing.T) {
	a := hiveagent.New(hiveagent.Deps{})
	sink := &alarmSink{fail: 1}
	a.EmitAlarm("recording.stopped", time.Now(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunAlarms(ctx, sink.emit, time.Hour)
	waitAttempts(t, sink, 1)
	a.WakeAlarms()
	waitAttempts(t, sink, 2)
	if a.PendingAlarms() != 0 {
		// the second attempt was acknowledged
		time.Sleep(20 * time.Millisecond)
	}
	if a.PendingAlarms() != 0 {
		t.Fatalf("pending = %d after the reconnect's attempt was acknowledged", a.PendingAlarms())
	}
}

// One warning once an alarm waited a minute for its ack, not one per try.
func TestAnAlarmUnacknowledgedForAMinuteIsLoggedOnce(t *testing.T) {
	var buf syncBuffer
	now := time.Date(2026, 11, 24, 10, 30, 0, 0, time.UTC)
	var mu sync.Mutex
	clock := func() time.Time { mu.Lock(); defer mu.Unlock(); return now }
	a := hiveagent.New(hiveagent.Deps{Logger: slog.New(slog.NewTextHandler(&buf, nil)), Now: clock})
	sink := &alarmSink{fail: 1 << 30}
	a.EmitAlarm("recording.stopped", now, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go a.RunAlarms(ctx, sink.emit, time.Millisecond)
	waitAttempts(t, sink, 2)
	if strings.Contains(buf.String(), "alarm not acknowledged") {
		t.Fatalf("warned before a minute: %s", buf.String())
	}
	mu.Lock()
	now = now.Add(2 * time.Minute)
	mu.Unlock()
	a.WakeAlarms()
	n := len(sink.names())
	waitAttempts(t, sink, n+3)
	if c := strings.Count(buf.String(), "alarm not acknowledged"); c != 1 {
		t.Fatalf("%d warnings, want 1:\n%s", c, buf.String())
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  strings.Builder
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}
