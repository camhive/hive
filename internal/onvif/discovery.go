package onvif

import (
	"context"
	"crypto/rand"
	"encoding/xml"
	"fmt"
	"net"
	"strings"
	"time"
)

// DiscoverOptions controls a WS-Discovery probe.
type DiscoverOptions struct {
	Timeout time.Duration // how long to collect answers; default 3s
	Address string        // where to send the probe; default the multicast group 239.255.255.250:3702
}

// Discovered is one camera that answered a probe.
type Discovered struct {
	XAddr  string // device service address (first XAddr)
	Name   string // from scope onvif://www.onvif.org/name/<name>, if present
	Scopes []string
}

// Discover sends a WS-Discovery Probe for NetworkVideoTransmitter devices and
// returns every distinct device that answers within the timeout.
func Discover(ctx context.Context, opts DiscoverOptions) ([]Discovered, error) {
	if opts.Timeout == 0 {
		opts.Timeout = 3 * time.Second
	}
	if opts.Address == "" {
		opts.Address = "239.255.255.250:3702"
	}
	dst, err := net.ResolveUDPAddr("udp4", opts.Address)
	if err != nil {
		return nil, err
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("onvif: discovery socket: %w", err)
	}
	defer func() { _ = conn.Close() }()

	msgID := "uuid:" + newUUID()
	probe := `<?xml version="1.0" encoding="UTF-8"?>` +
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"` +
		` xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">` +
		`<e:Header><w:MessageID>` + msgID + `</w:MessageID><w:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To>` +
		`<w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>` +
		`<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`
	if _, err := conn.WriteToUDP([]byte(probe), dst); err != nil {
		return nil, fmt.Errorf("onvif: send probe: %w", err)
	}

	deadline := time.Now().Add(opts.Timeout)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	return collectMatches(conn, deadline, msgID), nil
}

// collectMatches reads ProbeMatches until the deadline, de-duplicating by XAddr.
func collectMatches(conn *net.UDPConn, deadline time.Time, msgID string) []Discovered {
	_ = conn.SetReadDeadline(deadline)
	seen := map[string]bool{}
	var found []Discovered
	buf := make([]byte, 64<<10)
	for {
		n, _, err := conn.ReadFromUDP(buf)
		if err != nil {
			return found // deadline or closed
		}
		for _, d := range parseProbeMatches(buf[:n], msgID) {
			if d.XAddr == "" || seen[d.XAddr] {
				continue
			}
			seen[d.XAddr] = true
			found = append(found, d)
		}
	}
}

// parseProbeMatches extracts devices from a ProbeMatches envelope that relates
// to our message id (other probes on the network are ignored).
func parseProbeMatches(raw []byte, msgID string) []Discovered {
	var env struct {
		RelatesTo string `xml:"Header>RelatesTo"`
		Matches   []struct {
			Scopes string `xml:"Scopes"`
			XAddrs string `xml:"XAddrs"`
		} `xml:"Body>ProbeMatches>ProbeMatch"`
	}
	if err := xml.Unmarshal(raw, &env); err != nil || strings.TrimSpace(env.RelatesTo) != msgID {
		return nil
	}
	var out []Discovered
	for _, m := range env.Matches {
		xaddrs := strings.Fields(m.XAddrs)
		d := Discovered{Scopes: strings.Fields(m.Scopes)}
		if len(xaddrs) > 0 {
			d.XAddr = xaddrs[0]
		}
		for _, s := range d.Scopes {
			if name, ok := strings.CutPrefix(s, "onvif://www.onvif.org/name/"); ok {
				d.Name = name
			}
		}
		out = append(out, d)
	}
	return out
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
