package camera

import (
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Identity is what a camera is, independent of where it is (ADR-0017).
// Every field is optional; the Hive learns them as it can.
type Identity struct {
	MAC           string `json:"mac,omitempty"`            // lower case, colon separated; from the neighbour table
	ONVIFEndpoint string `json:"onvif_endpoint,omitempty"` // WS-Discovery endpoint reference, e.g. urn:uuid:…
	Serial        string `json:"serial,omitempty"`         // ONVIF GetDeviceInformation
	Manufacturer  string `json:"manufacturer,omitempty"`
	Model         string `json:"model,omitempty"`
}

// Network says how the Hive reaches a camera.
type Network string

// Network kinds; empty means not known yet.
const (
	NetworkLocal  Network = "local"  // in one of the Hive's subnets, MAC known
	NetworkRouted Network = "routed" // behind a router: cannot be searched for
)

func (id Identity) normalized() Identity {
	id.MAC = strings.ToLower(strings.TrimSpace(id.MAC))
	id.ONVIFEndpoint = strings.TrimSpace(id.ONVIFEndpoint)
	id.Serial = strings.TrimSpace(id.Serial)
	id.Manufacturer = strings.TrimSpace(id.Manufacturer)
	id.Model = strings.TrimSpace(id.Model)
	return id
}

// fields lists the identifying fields (not manufacturer and model, which
// many cameras share).
func (id Identity) fields() [][2]string {
	return [][2]string{{"mac", id.MAC}, {"onvif_endpoint", id.ONVIFEndpoint}, {"serial", id.Serial}}
}

// Matches reports whether a found device is this camera: every identifying
// field both know is equal, and they share at least one.
func (id Identity) Matches(found Identity) bool {
	a, b := id.normalized().fields(), found.normalized().fields()
	shared := 0
	for k := range a {
		if a[k][1] == "" || b[k][1] == "" {
			continue
		}
		if a[k][1] != b[k][1] {
			return false
		}
		shared++
	}
	return shared > 0
}

// Learn fills the identity fields the camera does not know yet. Fields it
// knows and that differ are returned by name and left unchanged.
func (c Camera) Learn(seen Identity, now time.Time) (Camera, []string) {
	seen = seen.normalized()
	u := c
	var conflicts []string
	set := func(name string, have *string, got string) {
		switch {
		case got == "" || *have == got:
		case *have == "":
			*have = got
			u.UpdatedAt = now.UTC()
		default:
			conflicts = append(conflicts, name)
		}
	}
	set("mac", &u.Identity.MAC, seen.MAC)
	set("onvif_endpoint", &u.Identity.ONVIFEndpoint, seen.ONVIFEndpoint)
	set("serial", &u.Identity.Serial, seen.Serial)
	set("manufacturer", &u.Identity.Manufacturer, seen.Manufacturer)
	set("model", &u.Identity.Model, seen.Model)
	return u, conflicts
}

// Host is the camera's current host (IP or name) without port.
func (c Camera) Host() string {
	u, err := url.Parse(c.Source.Address)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

// MoveTo returns the camera at a new host: the source and every stream on
// the old host get it, with scheme, port, path and credentials unchanged.
func (c Camera) MoveTo(host string, now time.Time) (Camera, error) {
	host = strings.TrimSpace(host)
	if host == "" || (strings.ContainsAny(host, ":/ @") && net.ParseIP(host) == nil) {
		return Camera{}, fmt.Errorf("%w: %q is not a host name or IP address", ErrInvalid, host)
	}
	old := c.Host()
	u := c
	u.Streams = cloneStreams(c.Streams)
	var err error
	if u.Source.Address, err = withHost(c.Source.Address, old, host); err != nil {
		return Camera{}, err
	}
	for name, s := range u.Streams {
		if s.URL, err = withHost(s.URL, old, host); err != nil {
			return Camera{}, err
		}
		u.Streams[name] = s
	}
	u.UpdatedAt = now.UTC()
	if err := u.validate(); err != nil {
		return Camera{}, err
	}
	return u, nil
}

// withHost replaces the host of raw when it is old, keeping the port.
func withHost(raw, old, host string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("%w: %w", ErrInvalid, err)
	}
	if u.Hostname() != old {
		return raw, nil
	}
	h := host
	if strings.Contains(host, ":") { // IPv6
		h = "[" + host + "]"
	}
	if p := u.Port(); p != "" {
		h = net.JoinHostPort(host, p)
	}
	u.Host = h
	return u.String(), nil
}
