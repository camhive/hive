package video

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/camhive/hive/internal/testports"
)

// An engine whose API never comes up must not be left running: an orphaned
// MediaMTX would hold the ports of the next start (and of a new version
// after an update, which would then fail its health check).
func TestStartThatFailsLeavesNoProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell script stand-in")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "mediamtx")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\nexec sleep 3600\n"), 0o755); err != nil { //nolint:gosec // test executable
		t.Fatal(err)
	}
	ports := testports.Get(t, 3)
	m, err := NewMediaMTX(MediaMTXConfig{Binary: bin, Host: "127.0.0.1", APIPort: ports[0], PlaybackPort: ports[1], WebRTCPort: ports[2],
		WorkDir: filepath.Join(dir, "work"), ArchiveDir: filepath.Join(dir, "archive"), SegmentDuration: 10 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()
	if err := m.Start(ctx); err == nil {
		t.Fatal("started without an API")
	}
	if m.PID() != 0 {
		t.Fatalf("process %d left running", m.PID())
	}
	select {
	case <-m.Exited():
	case <-time.After(5 * time.Second):
		t.Fatal("the stand-in did not exit")
	}
}
