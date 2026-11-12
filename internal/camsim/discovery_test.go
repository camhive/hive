package camsim

import (
	"context"
	"net"
	"strings"
	"testing"
	"time"
)

// WS-Discovery: a Probe sent to the responder must yield a ProbeMatch that
// advertises the device service XAddr and relates to the probe's MessageID.
func TestDiscoveryAnswersProbeWithXAddrs(t *testing.T) {
	cfg := testConfig()
	cfg.applyDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Bind to an ephemeral unicast port so the test does not need multicast.
	resp, err := startDiscovery(ctx, &cfg, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	conn, err := net.Dial("udp", resp.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	probe := `<e:Envelope xmlns:e="http://www.w3.org/2003/05/soap-envelope" xmlns:w="http://schemas.xmlsoap.org/ws/2004/08/addressing" xmlns:d="http://schemas.xmlsoap.org/ws/2005/04/discovery" xmlns:dn="http://www.onvif.org/ver10/network/wsdl">
<e:Header><w:MessageID>uuid:1111-2222</w:MessageID><w:To>urn:schemas-xmlsoap-org:ws:2005:04:discovery</w:To><w:Action>http://schemas.xmlsoap.org/ws/2005/04/discovery/Probe</w:Action></e:Header>
<e:Body><d:Probe><d:Types>dn:NetworkVideoTransmitter</d:Types></d:Probe></e:Body></e:Envelope>`
	if _, err := conn.Write([]byte(probe)); err != nil {
		t.Fatal(err)
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 8192)
	n, err := conn.Read(buf)
	if err != nil {
		t.Fatal(err)
	}
	out := string(buf[:n])
	for _, want := range []string{"ProbeMatches", "<w:RelatesTo>uuid:1111-2222</w:RelatesTo>", cfg.deviceServiceURL(), "NetworkVideoTransmitter"} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in\n%s", want, out)
		}
	}
}

func TestDiscoveryIgnoresNonProbeDatagrams(t *testing.T) {
	cfg := testConfig()
	cfg.applyDefaults()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	resp, err := startDiscovery(ctx, &cfg, "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	conn, _ := net.Dial("udp", resp.Addr().String())
	defer conn.Close()
	_, _ = conn.Write([]byte("hello"))
	_ = conn.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if n, err := conn.Read(make([]byte, 64)); err == nil {
		t.Fatalf("expected no reply, got %d bytes", n)
	}
}
