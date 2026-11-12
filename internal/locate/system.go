package locate

import (
	"context"
	"net/netip"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/netscan"
	"github.com/camhive/hive/internal/onvif"
)

// probeTimeout bounds one conversation with one device.
const probeTimeout = 5 * time.Second

// System is the Hive's real network view.
type System struct {
	// DiscoverAddress overrides the WS-Discovery multicast address (tests).
	DiscoverAddress string
}

// Neighbours implements Network.
func (System) Neighbours(ctx context.Context) (map[netip.Addr]string, error) {
	return netscan.Neighbours(ctx)
}

// Subnets implements Network.
func (System) Subnets() ([]netip.Prefix, error) { return netscan.Subnets() }

// Sweep implements Network.
func (System) Sweep(ctx context.Context, p netip.Prefix) error { return netscan.Sweep(ctx, p) }

// Discover implements Network.
func (s System) Discover(ctx context.Context) ([]onvif.Discovered, error) {
	return onvif.Discover(ctx, onvif.DiscoverOptions{Address: s.DiscoverAddress})
}

// Credentials proves a camera's credentials on a device: ONVIF with the
// WS-Security digest, RTSP with HTTP-style digest only (ADR-0017).
type Credentials struct{}

// Device implements Prober.
func (Credentials) Device(ctx context.Context, c camera.Camera, host string) (camera.Identity, error) {
	at, err := c.MoveTo(host, time.Now())
	if err != nil {
		return camera.Identity{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	info, err := onvif.NewClient(at.Source.Address, onvif.Credentials{Username: c.Credentials.Username, Password: c.Credentials.Password}).DeviceInformation(ctx)
	if err != nil {
		return camera.Identity{}, err
	}
	return camera.Identity{Serial: info.SerialNumber, Manufacturer: info.Manufacturer, Model: info.Model}, nil
}

// RTSP implements Prober.
func (Credentials) RTSP(ctx context.Context, c camera.Camera, host string) error {
	at, err := c.MoveTo(host, time.Now())
	if err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	return netscan.CheckRTSP(ctx, at.Streams[camera.Main].URL, c.Credentials.Username, c.Credentials.Password)
}
