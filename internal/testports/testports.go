// Package testports hands out free TCP ports to integration tests so that
// packages running in parallel never collide (they did: a camsim in one
// package and the engine in another shared a port, and the engine happily
// talked to the wrong MediaMTX). Ports are reserved by binding :0 and
// released just before use; the window is tiny and tests are the only user.
package testports

import (
	"context"
	"net"
	"testing"
)

// Get returns n distinct free TCP ports on 127.0.0.1.
func Get(t testing.TB, n int) []int {
	t.Helper()
	ports := make([]int, 0, n)
	listeners := make([]net.Listener, 0, n)
	lc := net.ListenConfig{}
	for len(ports) < n {
		ln, err := lc.Listen(context.Background(), "tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("testports: %v", err)
		}
		listeners = append(listeners, ln)
		ports = append(ports, ln.Addr().(*net.TCPAddr).Port)
	}
	for _, ln := range listeners {
		_ = ln.Close()
	}
	return ports
}
