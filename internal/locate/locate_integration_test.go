package locate_test

import (
	"context"
	"errors"
	"net/netip"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/locate"
	"github.com/camhive/hive/internal/testports"
)

// lan is a real network view except for the neighbour table, which a test
// cannot make the kernel fill for loopback addresses.
type lan struct {
	locate.System
	mu   sync.Mutex
	nbrs map[netip.Addr]string
}

func (l *lan) Neighbours(context.Context) (map[netip.Addr]string, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := map[netip.Addr]string{}
	for k, v := range l.nbrs {
		out[k] = v
	}
	return out, nil
}
func (l *lan) Subnets() ([]netip.Prefix, error) {
	return []netip.Prefix{netip.MustParsePrefix("127.0.0.0/24")}, nil
}
func (l *lan) Sweep(context.Context, netip.Prefix) error { return nil }

func startSim(t *testing.T, ctx context.Context, cfg camsim.Config) (*camsim.Simulator, string, context.CancelFunc) {
	t.Helper()
	sim, err := camsim.New(cfg)
	if errors.Is(err, camsim.ErrMissingTool) {
		if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
			t.Fatalf("CI requires tools: %v", err)
		}
		t.Skipf("skipping: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := sim.Start(ctx); err != nil {
		t.Fatal(err)
	}
	dctx, cancel := context.WithCancel(ctx)
	addr, err := camsim.ServeDiscovery(dctx, cfg, cfg.Host+":0")
	if err != nil {
		t.Fatal(err)
	}
	return sim, addr.String(), func() { cancel(); sim.Stop() }
}

// ADR-0017: an ONVIF camera that comes back at another address is found by
// its endpoint and MAC, proven with its credentials over ONVIF digest, and
// moved under the same id.
func TestCameraSimulatorMovedToAnotherAddressIsFoundAgain(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	ports := testports.Get(t, 3)
	cfg := camsim.Config{Username: "admin", Password: "pw", Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "front", Codec: camsim.H264, FPS: 5}}}
	sim, disc, stop := startSim(t, ctx, cfg)
	net := &lan{nbrs: map[netip.Addr]string{netip.MustParseAddr("127.0.0.1"): "aa:bb:cc:00:11:22"}}
	net.DiscoverAddress = disc

	simCfg := sim.Config()
	c, err := camera.New(camera.Spec{Name: "Front", Source: camera.Source{Kind: camera.SourceONVIF, Address: simCfg.DeviceServiceURL()},
		Credentials: camera.Credentials{Username: "admin", Password: "pw"},
		Streams:     map[camera.Stream]camera.StreamSpec{camera.Main: {URL: simCfg.RTSPURL(simCfg.Cameras[0], camsim.Main)}}}, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	store := &world{cams: map[camera.ID]camera.Camera{c.ID: c}, health: map[camera.ID]time.Time{}}
	var moved []camera.Camera
	l := locate.New(store, net, locate.Credentials{}, func(id camera.ID) (bool, time.Time) {
		store.mu.Lock()
		defer store.mu.Unlock()
		since, off := store.health[id]
		return !off, since
	}, locate.Options{OnMoved: func(_, m camera.Camera) { moved = append(moved, m) }})

	l.Tick(ctx)
	learned := store.cam(c.ID)
	if learned.Network != camera.NetworkLocal || learned.Identity.MAC == "" || learned.Identity.Serial == "" || learned.Identity.ONVIFEndpoint == "" {
		t.Fatalf("identity = %+v network %q", learned.Identity, learned.Network)
	}

	// The camera reboots and gets 127.0.0.2.
	stop()
	cfg.Host = "127.0.0.2"
	_, disc, stop = startSim(t, ctx, cfg)
	defer stop()
	net.mu.Lock()
	net.nbrs = map[netip.Addr]string{netip.MustParseAddr("127.0.0.2"): "aa:bb:cc:00:11:22"}
	net.DiscoverAddress = disc
	net.mu.Unlock()
	store.offline(c.ID, time.Now().Add(-time.Hour))
	l.Tick(ctx)

	got := store.cam(c.ID)
	if len(moved) != 1 || got.Host() != "127.0.0.2" || got.ID != c.ID {
		t.Fatalf("moved %d, camera now at %s", len(moved), got.Host())
	}
	if _, err := (locate.Credentials{}).Device(ctx, got, got.Host()); err != nil {
		t.Fatalf("the moved camera does not answer: %v", err)
	}
	if err := (locate.Credentials{}).RTSP(ctx, got, got.Host()); err != nil {
		t.Fatalf("its stream does not answer: %v", err)
	}
}
