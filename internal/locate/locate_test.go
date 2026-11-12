package locate_test

import (
	"context"
	"errors"
	"net/netip"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/locate"
	"github.com/camhive/hive/internal/onvif"
)

// world is a fake LAN: devices by IP with MAC, ONVIF endpoint, serial and
// the password they accept, plus what the hive sent to whom.
type world struct {
	mu        sync.Mutex
	devices   map[string]device // by IP
	sweeps    int
	asked     []string // hosts that were sent credentials
	cams      map[camera.ID]camera.Camera
	health    map[camera.ID]time.Time // offline since; absent: online
	moved     []string
	conflicts []string

	conflictCalls, identityWrites int
	noNets                        bool // Subnets knows none
}

type device struct {
	mac, endpoint, serial, password string
	onvif                           bool
}

func (w *world) Neighbours(context.Context) (map[netip.Addr]string, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := map[netip.Addr]string{}
	for ip, d := range w.devices {
		if d.mac != "" {
			out[netip.MustParseAddr(ip)] = d.mac
		}
	}
	return out, nil
}

func (w *world) Subnets() ([]netip.Prefix, error) {
	if w.noNets {
		return nil, nil
	}
	return []netip.Prefix{netip.MustParsePrefix("192.168.1.0/24")}, nil
}

func (w *world) Sweep(context.Context, netip.Prefix) error {
	w.mu.Lock()
	w.sweeps++
	w.mu.Unlock()
	return nil
}

func (w *world) Discover(context.Context) ([]onvif.Discovered, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	var out []onvif.Discovered
	for ip, d := range w.devices {
		if d.onvif {
			out = append(out, onvif.Discovered{XAddr: "http://" + ip + ":8000/onvif/device_service", Endpoint: d.endpoint})
		}
	}
	return out, nil
}

func (w *world) Device(_ context.Context, c camera.Camera, host string) (camera.Identity, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked = append(w.asked, host)
	d, ok := w.devices[host]
	if !ok || !d.onvif {
		return camera.Identity{}, errors.New("no answer")
	}
	if d.password != c.Credentials.Password {
		return camera.Identity{}, onvif.ErrUnauthorized
	}
	return camera.Identity{Serial: d.serial, Manufacturer: "Acme", Model: "X1"}, nil
}

func (w *world) RTSP(_ context.Context, c camera.Camera, host string) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.asked = append(w.asked, host)
	if d, ok := w.devices[host]; !ok || d.password != c.Credentials.Password {
		return errors.New("rejected")
	}
	return nil
}

func (w *world) List(context.Context) ([]camera.Camera, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]camera.Camera, 0, len(w.cams))
	for _, c := range w.cams {
		out = append(out, c)
	}
	return out, nil
}

func (w *world) SetIdentity(_ context.Context, id camera.ID, identity camera.Identity, network camera.Network) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.identityWrites++
	c := w.cams[id]
	c.Identity, c.Network = identity, network
	w.cams[id] = c
	return nil
}

func (w *world) SetAddress(_ context.Context, c camera.Camera) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	cur := w.cams[c.ID]
	cur.Source, cur.Streams = c.Source, c.Streams
	w.cams[c.ID] = cur
	return nil
}

func (w *world) cam(id camera.ID) camera.Camera {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.cams[id]
}

type clock struct{ t time.Time }

func setup(t *testing.T, kind camera.SourceKind) (*world, *locate.Locator, *clock, camera.Camera) {
	t.Helper()
	now := &clock{t: time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)}
	addr := "http://192.168.1.10:8000/onvif/device_service"
	streams := map[camera.Stream]camera.StreamSpec{camera.Main: {URL: "rtsp://192.168.1.10:554/main"}}
	if kind == camera.SourceRTSP {
		addr, streams = "rtsp://192.168.1.10:554/main", nil
	}
	c, err := camera.New(camera.Spec{Name: "Door", Source: camera.Source{Kind: kind, Address: addr},
		Credentials: camera.Credentials{Username: "admin", Password: "pw"}, Streams: streams}, now.t)
	if err != nil {
		t.Fatal(err)
	}
	w := &world{
		devices: map[string]device{"192.168.1.10": {mac: "aa:bb:cc:00:11:22", endpoint: "urn:uuid:1", serial: "SN1", password: "pw", onvif: kind == camera.SourceONVIF}},
		cams:    map[camera.ID]camera.Camera{c.ID: c},
		health:  map[camera.ID]time.Time{},
	}
	l := locate.New(w, w, w, func(id camera.ID) (bool, time.Time) {
		w.mu.Lock()
		defer w.mu.Unlock()
		since, off := w.health[id]
		return !off, since
	}, locate.Options{
		Now:     func() time.Time { return now.t },
		OnMoved: func(old, moved camera.Camera) { w.moved = append(w.moved, old.Host()+"->"+moved.Host()) },
		OnConflict: func(c camera.Camera, fields []string) {
			w.conflictCalls++
			w.conflicts = append(w.conflicts, fields...)
		},
	})
	return w, l, now, c
}

// The camera disappears from its address and shows up at another one.
func (w *world) move(from, to string) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.devices[to] = w.devices[from]
	delete(w.devices, from)
}

func (w *world) offline(id camera.ID, since time.Time) {
	w.mu.Lock()
	w.health[id] = since
	w.mu.Unlock()
}

func TestOnlineCameraLearnsItsIdentityAndNetwork(t *testing.T) {
	w, l, now, c := setup(t, camera.SourceONVIF)
	l.Tick(context.Background())
	got := w.cam(c.ID)
	want := camera.Identity{MAC: "aa:bb:cc:00:11:22", ONVIFEndpoint: "urn:uuid:1", Serial: "SN1", Manufacturer: "Acme", Model: "X1"}
	if got.Identity != want || got.Network != camera.NetworkLocal {
		t.Fatalf("identity = %+v network %q", got.Identity, got.Network)
	}
	if w.conflictCalls != 0 || w.identityWrites != 1 {
		t.Fatalf("clean learning: %d conflict reports, %d writes", w.conflictCalls, w.identityWrites)
	}
	// Another device on the old address later: found by the hourly check,
	// reported, not learned.
	w.devices["192.168.1.10"] = device{mac: "aa:bb:cc:00:11:99", endpoint: "urn:uuid:9", serial: "SN9", password: "pw", onvif: true}
	now.t = now.t.Add(time.Hour)
	l.Tick(context.Background())
	if w.cam(c.ID).Identity != want || len(w.conflicts) != 3 || w.identityWrites != 1 {
		t.Fatalf("identity %+v conflicts %v writes %d", w.cam(c.ID).Identity, w.conflicts, w.identityWrites)
	}
}

func TestOfflineCameraIsFoundAtItsNewAddressAndMoved(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceONVIF)
	l.Tick(ctx) // learn identity while online
	w.move("192.168.1.10", "192.168.1.23")
	w.offline(c.ID, now.t)

	l.Tick(ctx) // offline for less than two minutes: nothing yet
	if len(w.moved) != 0 || w.sweeps != 0 {
		t.Fatalf("searched too early: moved %v sweeps %d", w.moved, w.sweeps)
	}
	now.t = now.t.Add(2 * time.Minute)
	l.Tick(ctx)
	got := w.cam(c.ID)
	if len(w.moved) != 1 || w.moved[0] != "192.168.1.10->192.168.1.23" || got.Host() != "192.168.1.23" || got.Streams[camera.Main].URL != "rtsp://192.168.1.23:554/main" {
		t.Fatalf("moved %v, camera at %s", w.moved, got.Host())
	}
}

// A device with the right MAC but another serial is not adopted, and a
// device whose MAC and endpoint do not match never receives credentials.
func TestImpostorsAreNotAdoptedAndStrangersGetNoCredentials(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceONVIF)
	l.Tick(ctx)
	w.mu.Lock()
	delete(w.devices, "192.168.1.10")
	w.devices["192.168.1.30"] = device{mac: "aa:bb:cc:00:11:22", endpoint: "urn:uuid:1", serial: "OTHER", password: "pw", onvif: true} // spoofed MAC and endpoint
	w.devices["192.168.1.31"] = device{mac: "11:22:33:44:55:66", endpoint: "urn:uuid:7", serial: "SN7", password: "pw", onvif: true}   // another camera
	w.asked = nil
	w.mu.Unlock()
	w.offline(c.ID, now.t.Add(-time.Hour))
	l.Tick(ctx)
	if len(w.moved) != 0 || w.cam(c.ID).Host() != "192.168.1.10" {
		t.Fatalf("adopted an impostor: %v", w.moved)
	}
	for _, h := range w.asked {
		if h == "192.168.1.31" {
			t.Fatal("credentials sent to a device that does not match")
		}
	}
}

// The search backs off while the camera stays missing: 2, 5, 15 minutes, then hourly.
func TestSearchBacksOffWhileTheCameraStaysMissing(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceONVIF)
	l.Tick(ctx)
	w.mu.Lock()
	delete(w.devices, "192.168.1.10")
	w.mu.Unlock()
	w.offline(c.ID, now.t)
	start := now.t
	var at []time.Duration
	for m := 0; m <= 90; m++ {
		now.t = start.Add(time.Duration(m) * time.Minute)
		before := w.sweeps
		l.Tick(ctx)
		if w.sweeps > before {
			at = append(at, now.t.Sub(start))
		}
	}
	want := []time.Duration{2 * time.Minute, 4 * time.Minute, 9 * time.Minute, 24 * time.Minute, 84 * time.Minute}
	if len(at) != len(want) {
		t.Fatalf("searched at %v, want %v", at, want)
	}
	for i := range want {
		if at[i] != want[i] {
			t.Fatalf("searched at %v, want %v", at, want)
		}
	}
}

// An RTSP camera is found by its MAC and proven with its credentials; one
// whose MAC is unknown is not searched for.
func TestRTSPCameraIsFoundByMACAndOneWithoutIsNot(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceRTSP)
	l.Tick(ctx)
	if w.cam(c.ID).Identity.MAC != "aa:bb:cc:00:11:22" {
		t.Fatalf("identity = %+v", w.cam(c.ID).Identity)
	}
	w.move("192.168.1.10", "192.168.1.40")
	w.offline(c.ID, now.t.Add(-time.Hour))
	l.Tick(ctx)
	if w.cam(c.ID).Host() != "192.168.1.40" {
		t.Fatalf("not moved: %v", w.moved)
	}

	w2, l2, now2, c2 := setup(t, camera.SourceRTSP)
	w2.mu.Lock()
	w2.devices["192.168.1.10"] = device{password: "pw"} // MAC never learned
	w2.mu.Unlock()
	l2.Tick(ctx)
	w2.offline(c2.ID, now2.t.Add(-time.Hour))
	l2.Tick(ctx)
	if w2.sweeps != 0 {
		t.Fatal("searched for a camera it cannot recognise")
	}
}

// A camera outside the hive's subnets is routed and never searched for.
func TestRoutedCameraIsFlaggedAndNotSearched(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceONVIF)
	routed, _ := c.MoveTo("10.20.0.5", now.t)
	w.cams[c.ID] = routed
	w.devices["10.20.0.5"] = device{endpoint: "urn:uuid:1", serial: "SN1", password: "pw", onvif: true}
	l.Tick(ctx)
	if w.cam(c.ID).Network != camera.NetworkRouted {
		t.Fatalf("network = %q", w.cam(c.ID).Network)
	}
	w.offline(c.ID, now.t.Add(-time.Hour))
	l.Tick(ctx)
	if w.sweeps != 0 || len(w.moved) != 0 {
		t.Fatal("searched for a routed camera")
	}
}

func (w *world) askedCount() int {
	w.mu.Lock()
	defer w.mu.Unlock()
	return len(w.asked)
}

// An online camera is asked what it is at once, then hourly (to notice
// another device at its address), never in between; switched-off cameras
// never.
func TestIdentityIsAskedAtOnceThenHourly(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceONVIF)
	l.Tick(ctx)
	first := w.askedCount()
	if first == 0 {
		t.Fatal("not asked at once")
	}
	now.t = now.t.Add(59 * time.Minute)
	l.Tick(ctx)
	if w.askedCount() != first {
		t.Fatal("asked again within the hour")
	}
	now.t = now.t.Add(time.Minute) // exactly an hour
	l.Tick(ctx)
	if w.askedCount() != first+1 {
		t.Fatalf("not asked after an hour: %d", w.askedCount())
	}
	off := w.cam(c.ID)
	off.Enabled = false
	w.cams[c.ID] = off
	now.t = now.t.Add(2 * time.Hour)
	l.Tick(ctx)
	if w.askedCount() != first+1 {
		t.Fatal("asked a switched-off camera")
	}
}

// Without known subnets the hive cannot tell local from routed: it says neither.
func TestNoSubnetsMeansTheNetworkStaysUnknown(t *testing.T) {
	w, l, _, c := setup(t, camera.SourceONVIF)
	w.noNets = true
	l.Tick(context.Background())
	if w.cam(c.ID).Network != "" {
		t.Fatalf("network = %q", w.cam(c.ID).Network)
	}
}

// A local camera the hive knows nothing about is not searched for.
func TestLocalCameraWithoutIdentityIsNotSearched(t *testing.T) {
	w, l, now, c := setup(t, camera.SourceONVIF)
	bare := w.cam(c.ID)
	bare.Network = camera.NetworkLocal
	w.cams[c.ID] = bare
	w.offline(c.ID, now.t.Add(-time.Hour))
	l.Tick(context.Background())
	if w.sweeps != 0 || w.askedCount() != 0 {
		t.Fatal("searched for a camera it cannot recognise")
	}
}

// When the sweep did not learn the new address's MAC, the ONVIF endpoint
// alone leads to the camera, and the serial still has to match.
func TestCameraIsFoundByItsEndpointWhenTheSweepMissedItsMAC(t *testing.T) {
	ctx := context.Background()
	w, l, now, c := setup(t, camera.SourceONVIF)
	l.Tick(ctx)
	w.mu.Lock()
	d := w.devices["192.168.1.10"]
	delete(w.devices, "192.168.1.10")
	d.mac = ""
	w.devices["192.168.1.77"] = d
	w.mu.Unlock()
	w.offline(c.ID, now.t.Add(-time.Hour))
	l.Tick(ctx)
	if w.cam(c.ID).Host() != "192.168.1.77" {
		t.Fatalf("not found by endpoint: %v", w.moved)
	}
}

func TestRunTicksUntilCancelled(t *testing.T) {
	w, l, _, c := setup(t, camera.SourceRTSP)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { l.Run(ctx, time.Hour); close(done) }()
	deadline := time.Now().Add(5 * time.Second)
	for w.cam(c.ID).Identity.MAC == "" && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if w.cam(c.ID).Network != camera.NetworkLocal {
		t.Fatalf("no tick ran: %+v", w.cam(c.ID))
	}
}
