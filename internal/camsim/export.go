package camsim

import (
	"context"
	"net"
	"net/http"
)

// ONVIFHandler returns the ONVIF mock as an http.Handler without starting
// MediaMTX or ffmpeg, together with a function that returns the PTZ commands
// received so far. Intended for tests of ONVIF clients.
func ONVIFHandler(cfg Config) (http.Handler, func() []PTZCommand) {
	cfg.applyDefaults()
	log := newPTZLog()
	return newONVIFHandlerWithPTZ(&cfg, log), log.Commands
}

// ServeDiscovery runs only the WS-Discovery responder on addr (a unicast
// address for tests, or the multicast group) until ctx is cancelled and
// returns the address it listens on.
func ServeDiscovery(ctx context.Context, cfg Config, addr string) (net.Addr, error) {
	cfg.applyDefaults()
	d, err := startDiscovery(ctx, &cfg, addr)
	if err != nil {
		return nil, err
	}
	return d.Addr(), nil
}
