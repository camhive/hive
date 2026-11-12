package camsim

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"crypto/rand"
)

// wsDiscoveryMulticast is the WS-Discovery group and port ONVIF clients probe.
const wsDiscoveryMulticast = "239.255.255.250:3702"

// discovery answers WS-Discovery Probe messages with a ProbeMatch for cfg.
type discovery struct {
	conn *net.UDPConn
	cfg  *Config
	uuid string
}

// startDiscovery listens on addr (multicast group or a unicast address for
// tests) until ctx is cancelled.
func startDiscovery(ctx context.Context, cfg *Config, addr string) (*discovery, error) {
	udpAddr, err := net.ResolveUDPAddr("udp4", addr)
	if err != nil {
		return nil, err
	}
	var conn *net.UDPConn
	if udpAddr.IP.IsMulticast() {
		conn, err = net.ListenMulticastUDP("udp4", nil, udpAddr)
	} else {
		conn, err = net.ListenUDP("udp4", udpAddr)
	}
	if err != nil {
		return nil, fmt.Errorf("camsim: discovery listen %s: %w", addr, err)
	}
	d := &discovery{conn: conn, cfg: cfg, uuid: newUUID()}
	go d.serve(ctx)
	return d, nil
}

// Addr is the address the responder listens on.
func (d *discovery) Addr() net.Addr { return d.conn.LocalAddr() }

func (d *discovery) serve(ctx context.Context) {
	go func() {
		<-ctx.Done()
		_ = d.conn.Close()
	}()
	buf := make([]byte, 64<<10)
	for {
		n, from, err := d.conn.ReadFromUDP(buf)
		if err != nil {
			return // closed
		}
		msg := string(buf[:n])
		if !strings.Contains(msg, "Probe") || strings.Contains(msg, "ProbeMatch") {
			continue
		}
		relates := textOf(msg, "MessageID")
		_ = d.conn.SetWriteDeadline(time.Now().Add(time.Second))
		_, _ = d.conn.WriteToUDP([]byte(d.probeMatch(relates)), from)
	}
}

func (d *discovery) probeMatch(relatesTo string) string {
	return fmt.Sprintf(`<?xml version="1.0" encoding="UTF-8"?>`+
		`<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing"`+
		` xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">`+
		`<e:Header><w:MessageID>uuid:%s</w:MessageID><w:RelatesTo>%s</w:RelatesTo>`+
		`<w:To>http://schemas.xmlsoap.org/ws/2004/08/addressing/role/anonymous</w:To>`+
		`<w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/ProbeMatches</w:Action></e:Header>`+
		`<e:Body><d:ProbeMatches><d:ProbeMatch><w:EndpointReference><w:Address>urn:uuid:%s</w:Address></w:EndpointReference>`+
		`<d:Types>dn:NetworkVideoTransmitter</d:Types>`+
		`<d:Scopes>onvif://www.onvif.org/type/video_encoder onvif://www.onvif.org/name/camsim onvif://www.onvif.org/hardware/camsim</d:Scopes>`+
		`<d:XAddrs>%s</d:XAddrs><d:MetadataVersion>1</d:MetadataVersion></d:ProbeMatch></d:ProbeMatches></e:Body></e:Envelope>`,
		newUUID(), relatesTo, d.uuid, d.cfg.deviceServiceURL())
}

func newUUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
