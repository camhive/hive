package hiveconfig_test

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/camhive/hive/internal/hiveconfig"
)

// #61: the video engine's ports default to MediaMTX's own and can be moved
// in hive.yaml when another program on the host holds them.
func TestEnginePortsDefaultAndCanBeMoved(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	c, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	if c.EngineAPIPort != 9997 || c.EngineWebRTCPort != 8889 || c.EngineWebRTCUDPPort != 8890 {
		t.Fatalf("defaults = %d %d %d", c.EngineAPIPort, c.EngineWebRTCPort, c.EngineWebRTCUDPPort)
	}
	yaml := "engine_api_port: 19997\nengine_webrtc_port: 18889\nengine_webrtc_udp_port: 18890\n"
	if err := os.WriteFile(filepath.Join(paths.Data, "hive.yaml"), []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	if c, err = hiveconfig.Read(paths); err != nil || c.EngineAPIPort != 19997 || c.EngineWebRTCPort != 18889 || c.EngineWebRTCUDPPort != 18890 {
		t.Fatalf("moved = %d %d %d, %v", c.EngineAPIPort, c.EngineWebRTCPort, c.EngineWebRTCUDPPort, err)
	}
}

// A port outside 1-65535, or a TCP port another hive listener already
// uses, is refused at start with the setting's name.
func TestEnginePortsAreValidated(t *testing.T) {
	paths := hiveconfig.DefaultPathsFor(runtime.GOOS, os.Getenv).WithDataDir(t.TempDir())
	good, _, err := hiveconfig.Load(paths)
	if err != nil {
		t.Fatal(err)
	}
	for _, bad := range []struct {
		name   string
		mutate func(*hiveconfig.Config)
	}{
		{"engine_api_port", func(c *hiveconfig.Config) { c.EngineAPIPort = 0 }},
		{"engine_api_port", func(c *hiveconfig.Config) { c.EngineAPIPort = 65536 }},
		{"engine_webrtc_port", func(c *hiveconfig.Config) { c.EngineWebRTCPort = -1 }},
		{"engine_webrtc_udp_port", func(c *hiveconfig.Config) { c.EngineWebRTCUDPPort = 70000 }},
		{"engine_webrtc_port", func(c *hiveconfig.Config) { c.EngineWebRTCPort = c.EngineAPIPort }},
		{"engine_api_port", func(c *hiveconfig.Config) { c.EngineAPIPort = 16016 }},           // listen_addr
		{"engine_webrtc_port", func(c *hiveconfig.Config) { c.EngineWebRTCPort = 16443 }},     // tls_listen_addr
		{"tls_listen_addr", func(c *hiveconfig.Config) { c.TLSListenAddr = "0.0.0.0:16016" }}, // listen_addr's port
	} {
		c := good
		bad.mutate(&c)
		if err := c.Validate(); err == nil || !strings.Contains(err.Error(), bad.name) {
			t.Errorf("%s: %v", bad.name, err)
		}
	}
	for _, edge := range []func(*hiveconfig.Config){
		func(c *hiveconfig.Config) {
			c.EngineAPIPort, c.EngineWebRTCPort, c.EngineWebRTCUDPPort = 1, 65535, 65535
		},
		func(c *hiveconfig.Config) { c.EngineWebRTCUDPPort = c.EngineWebRTCPort },      // UDP and TCP do not clash
		func(c *hiveconfig.Config) { c.TLSListenAddr, c.EngineWebRTCPort = "", 16443 }, // no LAN listener
	} {
		c := good
		edge(&c)
		if err := c.Validate(); err != nil {
			t.Errorf("edge rejected: %v", err)
		}
	}
}
