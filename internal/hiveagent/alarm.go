package hiveagent

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/camhive/hive/internal/protocol"
)

// MaxAlarms bounds the alarm lane; the oldest are dropped.
const MaxAlarms = 16

// AlarmTimeout is how long one attempt waits for the cloud's ack.
const AlarmTimeout = 10 * time.Second

// alarmLane holds the events the cloud must get although the hive's disk
// may be gone (#172, ADR-0024 amendment): in memory only, never in the
// store, each sent again until the cloud acknowledges it. It is apart
// from the event queue: its own lock, its own sender.
type alarmLane struct {
	mu     sync.Mutex
	list   []alarm
	seq    uint64
	notify chan struct{}
}

// alarm is one event on the lane and when it was made.
type alarm struct {
	queued
	made time.Time
}

func newAlarmLane() *alarmLane { return &alarmLane{notify: make(chan struct{}, 1)} }

// EmitAlarm queues an event on the alarm lane, stamped with at.
func (a *Agent) EmitAlarm(name string, at time.Time, data any) {
	raw, _ := json.Marshal(data)
	l := a.alarms
	l.mu.Lock()
	if len(l.list) >= MaxAlarms {
		l.list = l.list[1:]
	}
	l.seq++
	l.list = append(l.list, alarm{queued: queued{seq: l.seq, e: protocol.Event{Name: name, At: at.UTC(), Data: raw}}, made: a.d.Now()})
	l.mu.Unlock()
	select {
	case l.notify <- struct{}{}:
	default:
	}
}

// PendingAlarms is how many alarms wait for an ack.
func (a *Agent) PendingAlarms() int {
	a.alarms.mu.Lock()
	defer a.alarms.mu.Unlock()
	return len(a.alarms.list)
}

// AlarmWarnAfter is how long an alarm waits for its ack before the hive
// logs it, once.
const AlarmWarnAfter = time.Minute

// WakeAlarms asks the lane again at once and starts its backoff over:
// the link reconnected.
func (a *Agent) WakeAlarms() {
	select {
	case a.alarms.notify <- struct{}{}:
	default:
	}
}

// RunAlarms sends the alarm lane in order with emit, which returns nil
// only once the cloud acknowledged the event (tunnel.Agent.EmitAcked). A
// failed attempt is tried again after retry, doubling up to 30 times
// retry (10 s to 5 min); a new alarm or a reconnect (WakeAlarms) asks at
// once and starts over. An alarm unacknowledged for AlarmWarnAfter is
// logged once. Returns when ctx is done.
func (a *Agent) RunAlarms(ctx context.Context, emit Emitter, retry time.Duration) {
	l := a.alarms
	delay, warned := retry, uint64(0)
	for {
		head, err := l.sendAll(ctx, emit)
		if err == nil {
			delay = retry
			select {
			case <-ctx.Done():
				return
			case <-l.notify:
			}
			continue
		}
		if head.seq != warned && a.d.Now().Sub(head.made) >= AlarmWarnAfter {
			warned = head.seq
			a.log.Warn("alarm not acknowledged by the cloud; sending it again", "event", head.e.Name, "since", head.made, "err", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-l.notify:
			delay = retry
		case <-time.After(delay):
			delay = min(2*delay, 30*retry)
		}
	}
}

// sendAll sends until the lane is empty (nil) or an attempt fails (the
// alarm and its error).
func (l *alarmLane) sendAll(ctx context.Context, emit Emitter) (alarm, error) {
	for {
		l.mu.Lock()
		if len(l.list) == 0 {
			l.mu.Unlock()
			return alarm{}, nil
		}
		head := l.list[0]
		l.mu.Unlock()
		actx, cancel := context.WithTimeout(ctx, AlarmTimeout)
		err := emit(actx, head.e)
		cancel()
		if err != nil {
			return head, err
		}
		l.mu.Lock()
		if len(l.list) > 0 && l.list[0].seq == head.seq { // an overflow may have dropped it meanwhile
			l.list = l.list[1:]
		}
		l.mu.Unlock()
	}
}
