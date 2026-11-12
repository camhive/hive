package hiveapp_test

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/camhive/hive/internal/hiveapp"
)

func TestRunServesHealthAndStopsOnCancel(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	app := hiveapp.New(hiveapp.Config{ListenAddr: addr, Version: "9.9.9"})
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- app.Run(ctx) }()

	var resp *http.Response
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		req, _ := http.NewRequestWithContext(ctx, http.MethodGet, "http://"+addr+"/healthz", nil)
		resp, err = http.DefaultClient.Do(req)
		if err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("health endpoint never came up: %v", err)
	}
	defer resp.Body.Close()
	var body struct {
		Status  string `json:"status"`
		Version string `json:"version"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK || body.Status != "ok" || body.Version != "9.9.9" {
		t.Fatalf("health = %d %+v", resp.StatusCode, body)
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run returned %v after cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Run did not return after cancel")
	}
}

func TestRunFailsFastWhenPortIsTaken(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	app := hiveapp.New(hiveapp.Config{ListenAddr: ln.Addr().String()})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := app.Run(ctx); err == nil {
		t.Fatal("expected an error when the port is already in use")
	}
}

func TestDefaultsBindLoopbackOnly(t *testing.T) {
	cfg := hiveapp.Config{}
	cfg.ApplyDefaults()
	host, _, err := net.SplitHostPort(cfg.ListenAddr)
	if err != nil || host != "127.0.0.1" {
		t.Fatalf("default ListenAddr = %q; the local API must not be exposed on all interfaces by default", cfg.ListenAddr)
	}
}
