package tunnel_test

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/camhive/hive/internal/pki"
	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/tunnel"
)

type fixture struct {
	ca      *pki.CA
	gateway *tunnel.Gateway
	addr    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ca, err := pki.NewCA("test CA", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	srvTLS, err := ca.ServerTLSConfig("gateway.test")
	if err != nil {
		t.Fatal(err)
	}
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	if err != nil {
		t.Fatal(err)
	}
	gw := tunnel.NewGateway(tunnel.GatewayConfig{PingInterval: 200 * time.Millisecond})
	go func() { _ = gw.Serve(ln) }()
	t.Cleanup(func() { _ = gw.Close() })
	return &fixture{ca: ca, gateway: gw, addr: ln.Addr().String()}
}

// connectHive issues a certificate from ca and runs a Hive agent with the
// given command handler until the test ends.
func (f *fixture) connectHive(t *testing.T, ca *pki.CA, id pki.HiveID, handler tunnel.Handler) {
	t.Helper()
	pub, priv, _ := ed25519.GenerateKey(nil)
	cert, err := ca.IssueDevice(id, pub, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	cliTLS, err := pki.ClientTLSConfig(f.ca.Certificate(), cert, priv, "gateway.test")
	if err != nil {
		t.Fatal(err)
	}
	ag := tunnel.NewAgent(tunnel.AgentConfig{
		URL: "wss://" + f.addr + "/v1/hive", TLS: cliTLS, HiveID: id, Version: "test",
		ReconnectMin: 50 * time.Millisecond, ReconnectMax: 200 * time.Millisecond,
	}, handler)
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	go ag.Run(ctx)
}

func echoHandler(_ context.Context, cmd protocol.Command) protocol.Result {
	switch cmd.Name {
	case "echo":
		return protocol.Result{OK: true, Data: cmd.Args}
	default:
		return protocol.Result{OK: false, Error: "unknown command " + cmd.Name}
	}
}

func waitOnline(t *testing.T, gw *tunnel.Gateway, id pki.HiveID) {
	const d = 5 * time.Second
	t.Helper()
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if gw.Online(id) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("hive %s did not come online within %v", id, d)
}

func TestHiveConnectsOutboundAndAnswersCommands(t *testing.T) {
	f := newFixture(t)
	f.connectHive(t, f.ca, "hv_1", echoHandler)
	waitOnline(t, f.gateway, "hv_1")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	res, err := f.gateway.Send(ctx, "hv_1", protocol.Command{Name: "echo", Args: json.RawMessage(`{"n":42}`)})
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK || string(res.Data) != `{"n":42}` {
		t.Fatalf("result = %+v", res)
	}

	res, err = f.gateway.Send(ctx, "hv_1", protocol.Command{Name: "explode"})
	if err != nil || res.OK || res.Error == "" {
		t.Fatalf("unknown command should yield OK=false with error, got %+v err=%v", res, err)
	}
}

func TestSendToOfflineHiveFails(t *testing.T) {
	f := newFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := f.gateway.Send(ctx, "hv_missing", protocol.Command{Name: "echo"})
	if !errors.Is(err, tunnel.ErrHiveOffline) {
		t.Fatalf("got %v, want ErrHiveOffline", err)
	}
}

func TestCertificateFromAnotherCAIsRejected(t *testing.T) {
	f := newFixture(t)
	other, _ := pki.NewCA("other", time.Hour)
	f.connectHive(t, other, "hv_evil", echoHandler)
	time.Sleep(600 * time.Millisecond)
	if f.gateway.Online("hv_evil") {
		t.Fatal("hive with a foreign certificate must not be accepted")
	}
}

func TestHiveIDMustMatchCertificate(t *testing.T) {
	f := newFixture(t)
	pub, priv, _ := ed25519.GenerateKey(nil)
	cert, _ := f.ca.IssueDevice("hv_real", pub, time.Hour)
	cliTLS, _ := pki.ClientTLSConfig(f.ca.Certificate(), cert, priv, "gateway.test")
	ag := tunnel.NewAgent(tunnel.AgentConfig{
		URL: "wss://" + f.addr + "/v1/hive", TLS: cliTLS, HiveID: "hv_impostor", Version: "test",
		ReconnectMin: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond,
	}, echoHandler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	time.Sleep(600 * time.Millisecond)
	if f.gateway.Online("hv_impostor") || f.gateway.Online("hv_real") {
		t.Fatal("hello with an id that differs from the certificate must be rejected")
	}
}

func TestHiveReconnectsAfterGatewayRestart(t *testing.T) {
	ca, _ := pki.NewCA("test CA", time.Hour)
	srvTLS, _ := ca.ServerTLSConfig("gateway.test")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	gw1 := tunnel.NewGateway(tunnel.GatewayConfig{PingInterval: 100 * time.Millisecond})
	go func() { _ = gw1.Serve(ln) }()

	pub, priv, _ := ed25519.GenerateKey(nil)
	cert, _ := ca.IssueDevice("hv_r", pub, time.Hour)
	cliTLS, _ := pki.ClientTLSConfig(ca.Certificate(), cert, priv, "gateway.test")
	var connects atomic.Int32
	ag := tunnel.NewAgent(tunnel.AgentConfig{
		URL: "wss://" + addr + "/v1/hive", TLS: cliTLS, HiveID: "hv_r", Version: "test",
		ReconnectMin: 50 * time.Millisecond, ReconnectMax: 200 * time.Millisecond,
		OnConnect: func() { connects.Add(1) },
	}, echoHandler)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go ag.Run(ctx)
	waitOnline(t, gw1, "hv_r")

	// Gateway goes away and comes back on the same address.
	_ = gw1.Close()
	ln2, err := reListen(addr, srvTLS, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	gw2 := tunnel.NewGateway(tunnel.GatewayConfig{PingInterval: 100 * time.Millisecond})
	go func() { _ = gw2.Serve(ln2) }()
	t.Cleanup(func() { _ = gw2.Close() })
	waitOnline(t, gw2, "hv_r")

	sctx, scancel := context.WithTimeout(ctx, 3*time.Second)
	defer scancel()
	if res, err := gw2.Send(sctx, "hv_r", protocol.Command{Name: "echo", Args: json.RawMessage(`1`)}); err != nil || !res.OK {
		t.Fatalf("after reconnect: %+v %v", res, err)
	}
	if connects.Load() < 2 {
		t.Fatalf("OnConnect must run on every connect, got %d", connects.Load())
	}
}

func reListen(addr string, cfg *tls.Config, within time.Duration) (net.Listener, error) {
	deadline := time.Now().Add(within)
	for {
		ln, err := tls.Listen("tcp", addr, cfg)
		if err == nil || time.Now().After(deadline) {
			return ln, err
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// Hooks let the cloud decide who may connect and learn about presence and
// events without the tunnel knowing about databases or NATS.
func TestGatewayHooksGateHelloAndReportPresenceAndEvents(t *testing.T) {
	ca, _ := pki.NewCA("test CA", time.Hour)
	srvTLS, _ := ca.ServerTLSConfig("gateway.test")
	ln, err := tls.Listen("tcp", "127.0.0.1:0", srvTLS)
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var log []string
	rec := func(s string) {
		mu.Lock()
		defer mu.Unlock()
		log = append(log, s)
	}
	gw := tunnel.NewGateway(tunnel.GatewayConfig{PingInterval: 200 * time.Millisecond, Hooks: tunnel.Hooks{
		OnHello: func(id pki.HiveID, h protocol.Hello) error {
			if id == "hv_banned" {
				return errors.New("revoked")
			}
			rec("hello:" + string(id) + ":" + h.Version)
			return nil
		},
		OnOnline:  func(id pki.HiveID) { rec("online:" + string(id)) },
		OnOffline: func(id pki.HiveID) { rec("offline:" + string(id)) },
		OnEvent:   func(id pki.HiveID, e protocol.Event) { rec("event:" + string(id) + ":" + e.Name) },
	}})
	go func() { _ = gw.Serve(ln) }()
	t.Cleanup(func() { _ = gw.Close() })
	f := &fixture{ca: ca, gateway: gw, addr: ln.Addr().String()}

	// Banned hive: valid certificate, rejected by the hook.
	f.connectHive(t, ca, "hv_banned", echoHandler)
	time.Sleep(500 * time.Millisecond)
	if gw.Online("hv_banned") {
		t.Fatal("hook must be able to reject a hello")
	}

	// Good hive: hello, online, an event, then offline when it goes away.
	ctx, cancel := context.WithCancel(context.Background())
	pub, priv, _ := ed25519.GenerateKey(nil)
	cert, _ := ca.IssueDevice("hv_ok", pub, time.Hour)
	cliTLS, _ := pki.ClientTLSConfig(ca.Certificate(), cert, priv, "gateway.test")
	ag := tunnel.NewAgent(tunnel.AgentConfig{URL: "wss://" + f.addr + "/v1/hive", TLS: cliTLS, HiveID: "hv_ok", Version: "1.2.3",
		ReconnectMin: 50 * time.Millisecond, ReconnectMax: 100 * time.Millisecond}, echoHandler)
	go ag.Run(ctx)
	waitOnline(t, gw, "hv_ok")
	// The gateway registers the hive on hello; the agent holds the session
	// only after welcome, so wait for its side too before emitting.
	for d := time.Now().Add(3 * time.Second); !ag.Connected() && time.Now().Before(d); {
		time.Sleep(10 * time.Millisecond)
	}
	if err := ag.Emit(context.Background(), protocol.Event{Name: "camera.online", At: time.Now()}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		n := len(log)
		mu.Unlock()
		if n >= 3 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) && gw.Online("hv_ok") {
		time.Sleep(20 * time.Millisecond)
	}
	mu.Lock()
	got := append([]string(nil), log...)
	mu.Unlock()
	want := []string{"hello:hv_ok:1.2.3", "online:hv_ok", "event:hv_ok:camera.online", "offline:hv_ok"}
	if len(got) != len(want) {
		t.Fatalf("hook log = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("hook log = %v, want %v", got, want)
		}
	}
}
