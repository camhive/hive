package protocol_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/camhive/hive/internal/protocol"
)

func TestEnvelopeRoundTripCarriesTypedPayload(t *testing.T) {
	cmd := protocol.Command{Name: "status", Args: json.RawMessage(`{"camera":"front"}`)}
	env, err := protocol.NewEnvelope(protocol.KindCommand, "c-1", cmd)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := protocol.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	back, err := protocol.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	if back.V != protocol.Version || back.Kind != protocol.KindCommand || back.ID != "c-1" {
		t.Fatalf("envelope header = %+v", back)
	}
	var got protocol.Command
	if err := back.Unmarshal(&got); err != nil {
		t.Fatal(err)
	}
	if got.Name != "status" || string(got.Args) != `{"camera":"front"}` {
		t.Fatalf("command = %+v", got)
	}
}

func TestDecodeRejectsUnknownKindAndBadJSON(t *testing.T) {
	if _, err := protocol.Decode([]byte(`{"v":1,"kind":"teleport","id":"x","payload":{}}`)); !errors.Is(err, protocol.ErrUnknownKind) {
		t.Fatalf("unknown kind: %v", err)
	}
	if _, err := protocol.Decode([]byte(`not json`)); err == nil {
		t.Fatal("bad json must fail")
	}
	if _, err := protocol.Decode([]byte(`{"v":1,"kind":"ping"}`)); err != nil {
		t.Fatalf("payload-less ping is valid: %v", err)
	}
}

func TestDecodeRejectsUnsupportedVersion(t *testing.T) {
	_, err := protocol.Decode([]byte(`{"v":99,"kind":"ping","id":"x"}`))
	if !errors.Is(err, protocol.ErrUnsupportedVersion) {
		t.Fatalf("got %v, want ErrUnsupportedVersion", err)
	}
}

func TestEnvelopeRequiresIDForRequestKinds(t *testing.T) {
	_, err := protocol.NewEnvelope(protocol.KindCommand, "", protocol.Command{Name: "x"})
	if !errors.Is(err, protocol.ErrMissingID) {
		t.Fatalf("command without id: %v", err)
	}
	if _, err := protocol.NewEnvelope(protocol.KindPing, "", nil); err != nil {
		t.Fatalf("ping needs no id: %v", err)
	}
}

func TestHelloWelcomeAndResultShapes(t *testing.T) {
	h := protocol.Hello{HiveID: "hv_1", Version: "0.1.0", ProtocolVersion: protocol.Version}
	env, _ := protocol.NewEnvelope(protocol.KindHello, "", h)
	raw, _ := protocol.Encode(env)
	back, _ := protocol.Decode(raw)
	var got protocol.Hello
	if err := back.Unmarshal(&got); err != nil || got != h {
		t.Fatalf("hello = %+v err=%v", got, err)
	}

	w := protocol.Welcome{ServerTime: time.Date(2026, 11, 9, 0, 0, 0, 0, time.UTC), PingInterval: 5 * time.Second, MinProtocolVersion: 1}
	env, _ = protocol.NewEnvelope(protocol.KindWelcome, "", w)
	raw, _ = protocol.Encode(env)
	back, _ = protocol.Decode(raw)
	var gw protocol.Welcome
	if err := back.Unmarshal(&gw); err != nil || !gw.ServerTime.Equal(w.ServerTime) || gw.PingInterval != w.PingInterval {
		t.Fatalf("welcome = %+v err=%v", gw, err)
	}

	r := protocol.Result{OK: false, Error: "camera offline"}
	env, _ = protocol.NewEnvelope(protocol.KindResult, "c-1", r)
	raw, _ = protocol.Encode(env)
	back, _ = protocol.Decode(raw)
	var gr protocol.Result
	if err := back.Unmarshal(&gr); err != nil || gr.OK || gr.Error != "camera offline" {
		t.Fatalf("result = %+v err=%v", gr, err)
	}
}

// Golden: the wire format is a contract shared by hive and cloud.
func TestWireFormatGolden(t *testing.T) {
	env, _ := protocol.NewEnvelope(protocol.KindResult, "c-7", protocol.Result{OK: true, Data: json.RawMessage(`{"ready":true}`)})
	raw, _ := protocol.Encode(env)
	want := `{"v":1,"kind":"result","id":"c-7","payload":{"ok":true,"data":{"ready":true}}}`
	if string(raw) != want {
		t.Fatalf("wire format changed:\n got %s\nwant %s", raw, want)
	}
}

func TestEventRoundTripAndEmptyPayloadErrors(t *testing.T) {
	ev := protocol.Event{Name: "camera.offline", At: time.Date(2026, 11, 9, 1, 2, 3, 0, time.UTC), Data: json.RawMessage(`{"camera":"front"}`)}
	env, err := protocol.NewEnvelope(protocol.KindEvent, "", ev)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := protocol.Encode(env)
	back, _ := protocol.Decode(raw)
	var got protocol.Event
	if err := back.Unmarshal(&got); err != nil || got.Name != ev.Name || !got.At.Equal(ev.At) || string(got.Data) != string(ev.Data) {
		t.Fatalf("event = %+v err=%v", got, err)
	}

	ping, _ := protocol.NewEnvelope(protocol.KindPing, "", nil)
	var x struct{}
	if err := ping.Unmarshal(&x); err == nil {
		t.Fatal("Unmarshal on an empty payload must fail, not silently succeed")
	}
	if _, err := protocol.NewEnvelope("teleport", "x", nil); !errors.Is(err, protocol.ErrUnknownKind) {
		t.Fatalf("NewEnvelope unknown kind: %v", err)
	}
	if _, err := protocol.NewEnvelope(protocol.KindEvent, "", make(chan int)); err == nil {
		t.Fatal("unmarshalable payload must fail")
	}
	if _, err := protocol.Decode([]byte(`{"v":1,"kind":"result"}`)); !errors.Is(err, protocol.ErrMissingID) {
		t.Fatalf("result without id: %v", err)
	}
}
