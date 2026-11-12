// Package locate keeps track of what each Camera is and finds it again when
// it moves to another address on the Hive's network (ADR-0017). It learns a
// camera's identity while it is online and, once it has been offline for a
// while, looks for a device with that identity: WS-Discovery for ONVIF
// endpoints, a sweep of the Hive's subnets for MAC addresses. Credentials go
// only to a device whose MAC or endpoint already matches, and only in digest
// form; the address moves only when every known field matches.
package locate

import (
	"context"
	"log/slog"
	"net/netip"
	"net/url"
	"sync"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/onvif"
)

// Cameras is the part of the camera store the locator uses.
type Cameras interface {
	List(ctx context.Context) ([]camera.Camera, error)
	SetIdentity(ctx context.Context, id camera.ID, identity camera.Identity, network camera.Network) error
	SetAddress(ctx context.Context, c camera.Camera) error
}

// Network is what the locator sees of the LAN.
type Network interface {
	Neighbours(ctx context.Context) (map[netip.Addr]string, error)
	Subnets() ([]netip.Prefix, error)
	Sweep(ctx context.Context, p netip.Prefix) error
	Discover(ctx context.Context) ([]onvif.Discovered, error)
}

// Prober talks to a device with a camera's credentials.
type Prober interface {
	// Device asks an ONVIF device at host for its serial, make and model.
	Device(ctx context.Context, c camera.Camera, host string) (camera.Identity, error)
	// RTSP proves the credentials on the main stream at host (digest only).
	RTSP(ctx context.Context, c camera.Camera, host string) error
}

// Health reports whether a camera is online, or since when it is offline.
type Health func(id camera.ID) (online bool, since time.Time)

// Options tune the locator.
type Options struct {
	Now          func() time.Time
	Logger       *slog.Logger
	OfflineAfter time.Duration // before the first search; default 2 min
	// OnMoved is called after a camera was found at another host and stored.
	OnMoved func(old, moved camera.Camera)
	// OnConflict is called when the device at a camera's address reports
	// identity fields that differ from the known ones.
	OnConflict func(c camera.Camera, fields []string)
}

// backoff between searches while a camera stays missing.
var backoff = []time.Duration{2 * time.Minute, 5 * time.Minute, 15 * time.Minute, time.Hour}

// relearn is how often an online camera is asked again what it is: to fill
// what it could not tell before, and to notice another device at its
// address (ADR-0017 §1).
const relearn = time.Hour

// Locator learns identities and finds moved cameras.
type Locator struct {
	cams   Cameras
	net    Network
	probe  Prober
	health Health
	opts   Options

	mu      sync.Mutex
	learned map[camera.ID]time.Time // last identity attempt
	search  map[camera.ID]*searchState
}

type searchState struct {
	attempts int
	next     time.Time
}

// New creates a Locator.
func New(cams Cameras, net Network, probe Prober, health Health, opts Options) *Locator {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.DiscardHandler)
	}
	if opts.OfflineAfter == 0 {
		opts.OfflineAfter = 2 * time.Minute
	}
	if opts.OnMoved == nil {
		opts.OnMoved = func(camera.Camera, camera.Camera) {}
	}
	if opts.OnConflict == nil {
		opts.OnConflict = func(camera.Camera, []string) {}
	}
	return &Locator{cams: cams, net: net, probe: probe, health: health, opts: opts, learned: map[camera.ID]time.Time{}, search: map[camera.ID]*searchState{}}
}

// Run ticks every interval until ctx ends.
func (l *Locator) Run(ctx context.Context, interval time.Duration) {
	t := time.NewTicker(interval)
	defer t.Stop()
	for {
		l.Tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Tick learns identities of online cameras and searches for offline ones
// whose turn has come. One tick at a time.
func (l *Locator) Tick(ctx context.Context) {
	l.mu.Lock()
	defer l.mu.Unlock()
	cams, err := l.cams.List(ctx)
	if err != nil {
		l.opts.Logger.Warn("locate: cameras", "err", err)
		return
	}
	scan := &scan{l: l}
	now := l.opts.Now()
	for _, c := range cams {
		if !c.Enabled {
			continue
		}
		online, since := l.health(c.ID)
		if online {
			delete(l.search, c.ID)
			if now.Sub(l.learned[c.ID]) >= relearn {
				l.learned[c.ID] = now
				l.identify(ctx, c, scan)
			}
			continue
		}
		if l.searchDue(c, since, now) {
			l.find(ctx, c, scan)
		}
	}
}

// identify asks the camera at its current address what it is, stores what
// is new and reports fields that differ from the known ones.
func (l *Locator) identify(ctx context.Context, c camera.Camera, s *scan) {
	seen, network := l.probeAt(ctx, c, c.Host(), s)
	learned, conflicts := c.Learn(seen, l.opts.Now())
	if network == "" {
		network = c.Network
	}
	if len(conflicts) > 0 {
		l.opts.Logger.Warn("locate: another device answers at the camera's address", "camera", c.ID, "fields", conflicts)
		l.opts.OnConflict(c, conflicts)
	}
	if learned.Identity != c.Identity || network != c.Network {
		if err := l.cams.SetIdentity(ctx, c.ID, learned.Identity, network); err != nil {
			l.opts.Logger.Warn("locate: store identity", "camera", c.ID, "err", err)
		}
	}
}

// Probe reports what the device at host is and how it is reached, using
// the camera's credentials (the owner chose this host, ADR-0017 §6).
func (l *Locator) Probe(ctx context.Context, c camera.Camera, host string) (camera.Identity, camera.Network) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.probeAt(ctx, c, host, &scan{l: l})
}

// probeAt gathers identity at host: MAC and network from the neighbour
// table, endpoint from discovery, the rest from the device itself.
func (l *Locator) probeAt(ctx context.Context, c camera.Camera, host string, s *scan) (camera.Identity, camera.Network) {
	var id camera.Identity
	network := camera.Network("")
	if ip, err := netip.ParseAddr(host); err == nil {
		if s.local(ip) {
			if mac := s.neighbours(ctx)[ip]; mac != "" {
				id.MAC, network = mac, camera.NetworkLocal
			}
		} else if len(s.subnets()) > 0 {
			network = camera.NetworkRouted
		}
	}
	if c.Source.Kind == camera.SourceONVIF {
		id.ONVIFEndpoint = s.endpointAt(ctx, host)
		if dev, err := l.probe.Device(ctx, c, host); err == nil {
			id.Serial, id.Manufacturer, id.Model = dev.Serial, dev.Manufacturer, dev.Model
		}
	}
	return id, network
}

// searchDue: a local camera that can be recognised, offline long enough,
// whose backoff has passed.
func (l *Locator) searchDue(c camera.Camera, since, now time.Time) bool {
	if c.Network != camera.NetworkLocal || (c.Identity.MAC == "" && c.Identity.ONVIFEndpoint == "") {
		return false
	}
	st := l.search[c.ID]
	if st == nil {
		st = &searchState{next: since.Add(l.opts.OfflineAfter)}
		l.search[c.ID] = st
	}
	if now.Before(st.next) {
		return false
	}
	st.next = now.Add(backoff[min(st.attempts, len(backoff)-1)])
	st.attempts++
	return true
}

// find looks for the camera elsewhere and moves it to the first device that
// proves to be it.
func (l *Locator) find(ctx context.Context, c camera.Camera, s *scan) {
	for _, cand := range s.candidates(ctx, c) {
		if !l.verify(ctx, c, cand) {
			continue
		}
		moved, err := c.MoveTo(cand.host, l.opts.Now())
		if err != nil {
			l.opts.Logger.Warn("locate: move", "camera", c.ID, "err", err)
			return
		}
		if err := l.cams.SetAddress(ctx, moved); err != nil {
			l.opts.Logger.Warn("locate: store address", "camera", c.ID, "err", err)
			return
		}
		l.opts.Logger.Info("camera found at a new address", "camera", c.ID, "old", c.Host(), "new", cand.host)
		delete(l.search, c.ID)
		l.opts.OnMoved(c, moved)
		return
	}
	l.opts.Logger.Info("locate: camera not found", "camera", c.ID)
}

// verify: the pre-match without credentials, then the credentials (digest)
// and, for ONVIF, the serial.
func (l *Locator) verify(ctx context.Context, c camera.Camera, cand candidate) bool {
	if !c.Identity.Matches(cand.seen) {
		return false
	}
	if c.Source.Kind == camera.SourceRTSP {
		return l.probe.RTSP(ctx, c, cand.host) == nil
	}
	dev, err := l.probe.Device(ctx, c, cand.host)
	if err != nil {
		return false
	}
	full := cand.seen
	full.Serial, full.Manufacturer, full.Model = dev.Serial, dev.Manufacturer, dev.Model
	return c.Identity.Matches(full)
}

// candidate is a device elsewhere that matches by MAC or endpoint.
type candidate struct {
	host string
	seen camera.Identity // what is known without credentials
}

// scan caches one tick's view of the network.
type scan struct {
	l          *Locator
	nbrs       map[netip.Addr]string
	nets       []netip.Prefix
	netsRead   bool
	found      []onvif.Discovered
	discovered bool
	swept      bool
}

func (s *scan) neighbours(ctx context.Context) map[netip.Addr]string {
	if s.nbrs == nil {
		n, err := s.l.net.Neighbours(ctx)
		if err != nil {
			s.l.opts.Logger.Warn("locate: neighbour table", "err", err)
			n = map[netip.Addr]string{}
		}
		s.nbrs = n
	}
	return s.nbrs
}

func (s *scan) subnets() []netip.Prefix {
	if !s.netsRead {
		s.netsRead = true
		n, err := s.l.net.Subnets()
		if err != nil {
			s.l.opts.Logger.Warn("locate: subnets", "err", err)
		}
		s.nets = n
	}
	return s.nets
}

func (s *scan) local(ip netip.Addr) bool {
	for _, p := range s.subnets() {
		if p.Contains(ip) {
			return true
		}
	}
	return false
}

func (s *scan) discover(ctx context.Context) []onvif.Discovered {
	if !s.discovered {
		s.discovered = true
		f, err := s.l.net.Discover(ctx)
		if err != nil {
			s.l.opts.Logger.Warn("locate: discovery", "err", err)
		}
		s.found = f
	}
	return s.found
}

func (s *scan) endpointAt(ctx context.Context, host string) string {
	for _, d := range s.discover(ctx) {
		if u, err := url.Parse(d.XAddr); err == nil && u.Hostname() == host {
			return d.Endpoint
		}
	}
	return ""
}

// sweep refreshes the neighbour table once per tick.
func (s *scan) sweep(ctx context.Context) {
	if s.swept {
		return
	}
	s.swept = true
	for _, p := range s.subnets() {
		if err := s.l.net.Sweep(ctx, p); err != nil {
			s.l.opts.Logger.Warn("locate: sweep", "prefix", p, "err", err)
		}
	}
	s.nbrs = nil // read again
}

// candidates lists other hosts whose MAC or ONVIF endpoint is the camera's,
// with what is known of them without credentials.
func (s *scan) candidates(ctx context.Context, c camera.Camera) []candidate {
	set := &candidates{self: c.Host(), by: map[string]*candidate{}}
	if c.Source.Kind == camera.SourceONVIF && c.Identity.ONVIFEndpoint != "" {
		s.byEndpoint(ctx, c.Identity.ONVIFEndpoint, set)
	}
	if c.Identity.MAC != "" {
		s.sweep(ctx)
		s.byMAC(ctx, c.Identity.MAC, set)
	}
	set.fillMACs(s.neighbours(ctx))
	return set.list()
}

func (s *scan) byEndpoint(ctx context.Context, endpoint string, set *candidates) {
	for _, d := range s.discover(ctx) {
		if u, err := url.Parse(d.XAddr); err == nil && d.Endpoint == endpoint {
			if cand := set.add(u.Hostname()); cand != nil {
				cand.seen.ONVIFEndpoint = d.Endpoint
			}
		}
	}
}

func (s *scan) byMAC(ctx context.Context, mac string, set *candidates) {
	for ip, m := range s.neighbours(ctx) {
		if m == mac && s.local(ip) {
			set.add(ip.String())
		}
	}
}

// candidates collects hosts in the order found, never the camera's own.
type candidates struct {
	self  string
	by    map[string]*candidate
	order []string
}

func (cs *candidates) add(host string) *candidate {
	if host == cs.self {
		return nil
	}
	if cs.by[host] == nil {
		cs.by[host] = &candidate{host: host}
		cs.order = append(cs.order, host)
	}
	return cs.by[host]
}

// fillMACs records each candidate's MAC from the neighbour table.
func (cs *candidates) fillMACs(nbrs map[netip.Addr]string) {
	for _, host := range cs.order {
		if ip, err := netip.ParseAddr(host); err == nil {
			cs.by[host].seen.MAC = nbrs[ip]
		}
	}
}

func (cs *candidates) list() []candidate {
	out := make([]candidate, 0, len(cs.order))
	for _, h := range cs.order {
		out = append(out, *cs.by[h])
	}
	return out
}
