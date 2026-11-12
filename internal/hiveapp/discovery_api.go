package hiveapp

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/onvif"
)

// Discoverer finds cameras on the LAN and reads an ONVIF device's profiles.
// The hive's default talks ONVIF; tests inject a fake.
type Discoverer interface {
	Discover(ctx context.Context) ([]onvif.Discovered, error)
	Probe(ctx context.Context, xaddr string, creds onvif.Credentials) (ProbeResult, error)
}

// ProbeResult is what the owner sees before adding an ONVIF camera: the
// device and its media profiles with their RTSP addresses.
type ProbeResult struct {
	Device   onvif.DeviceInfo `json:"device"`
	Profiles []ProbedProfile  `json:"profiles"`
}

// ProbedProfile is a media profile with the stream URL it leads to.
type ProbedProfile struct {
	Token    string `json:"token"`
	Name     string `json:"name"`
	Encoding string `json:"encoding"`
	Width    int    `json:"width"`
	Height   int    `json:"height"`
	FPS      int    `json:"fps"`
	URL      string `json:"url"`
}

// ONVIFDiscoverer is the production Discoverer.
type ONVIFDiscoverer struct {
	Timeout time.Duration // WS-Discovery collection window; default 3 s
}

// Discover probes the LAN with WS-Discovery.
func (d ONVIFDiscoverer) Discover(ctx context.Context) ([]onvif.Discovered, error) {
	return onvif.Discover(ctx, onvif.DiscoverOptions{Timeout: d.Timeout})
}

// Probe reads device information and every profile's stream URI.
func (d ONVIFDiscoverer) Probe(ctx context.Context, xaddr string, creds onvif.Credentials) (ProbeResult, error) {
	c := onvif.NewClient(xaddr, creds)
	info, err := c.DeviceInformation(ctx)
	if err != nil {
		return ProbeResult{}, err
	}
	profiles, err := c.Profiles(ctx)
	if err != nil {
		return ProbeResult{}, err
	}
	out := ProbeResult{Device: info, Profiles: make([]ProbedProfile, 0, len(profiles))}
	for _, p := range profiles {
		uri, err := c.StreamURI(ctx, p.Token)
		if err != nil {
			return ProbeResult{}, err
		}
		out.Profiles = append(out.Profiles, ProbedProfile{Token: p.Token, Name: p.Name, Encoding: p.Encoding, Width: p.Width, Height: p.Height, FPS: p.FPS, URL: uri})
	}
	return out, nil
}

type discoveredView struct {
	XAddr  string   `json:"xaddr"`
	Name   string   `json:"name"`
	Scopes []string `json:"scopes"`
}

func (a *api) requireDiscovery(w http.ResponseWriter, r *http.Request) bool {
	if !claimsFrom(r).Allows("", auth.ActionManage) {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner can discover cameras")
		return false
	}
	if a.d.Discovery == nil {
		writeError(w, http.StatusServiceUnavailable, "not_ready", "discovery is not available on this hive")
		return false
	}
	return true
}

// discover answers GET /v0/discovery: every ONVIF device that replied on the LAN.
func (a *api) discover(w http.ResponseWriter, r *http.Request) {
	if !a.requireDiscovery(w, r) {
		return
	}
	found, err := a.d.Discovery.Discover(r.Context())
	if err != nil {
		writeError(w, http.StatusBadGateway, "camera", "discovery failed: "+err.Error())
		return
	}
	out := make([]discoveredView, 0, len(found))
	for _, f := range found {
		scopes := f.Scopes
		if scopes == nil {
			scopes = []string{}
		}
		out = append(out, discoveredView{XAddr: f.XAddr, Name: f.Name, Scopes: scopes})
	}
	writeJSON(w, http.StatusOK, out)
}

type probeInput struct {
	Address     string `json:"address"`
	Credentials *struct {
		Username string `json:"username"`
		Password string `json:"password"`
	} `json:"credentials"`
}

// probe answers POST /v0/discovery/probe: device info and profiles with
// stream URLs, so the owner picks main and sub before adding the camera.
func (a *api) probe(w http.ResponseWriter, r *http.Request) {
	if !a.requireDiscovery(w, r) {
		return
	}
	var in probeInput
	if !decodeJSON(w, r, &in) {
		return
	}
	if in.Address == "" {
		writeError(w, http.StatusUnprocessableEntity, "invalid", "address is required")
		return
	}
	var creds onvif.Credentials
	if in.Credentials != nil {
		creds = onvif.Credentials{Username: in.Credentials.Username, Password: in.Credentials.Password}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
	defer cancel()
	res, err := a.d.Discovery.Probe(ctx, in.Address, creds)
	switch {
	case errors.Is(err, onvif.ErrUnauthorized):
		writeError(w, http.StatusBadGateway, "camera_auth", "the camera rejected the credentials")
	case err != nil:
		writeError(w, http.StatusBadGateway, "camera", "the camera did not answer: "+err.Error())
	default:
		writeJSON(w, http.StatusOK, res)
	}
}
