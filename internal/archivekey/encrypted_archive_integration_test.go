package archivekey_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/export"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
	"github.com/camhive/hive/internal/testports"
	"github.com/camhive/hive/internal/video"
)

const camID = "cam_enc"

// encHive is the part of a hive the encrypted archive runs through: one
// camsim camera recorded by MediaMTX, the index, the archive key.
type encHive struct {
	t        *testing.T
	ctx      context.Context
	log      *slog.Logger
	ports    []int
	root     string // the archive
	keys     string // the archive key's directory
	st       *store.Store
	eng      *video.MediaMTX
	vault    *archivekey.Vault
	idx      *archive.Index
	simRTSP  int
	runStart []time.Time // the first segment of each run of the engine
}

func newEncHive(t *testing.T, ctx context.Context) *encHive {
	t.Helper()
	ports := testports.Get(t, 9)
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	sim, err := camsim.New(camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "enc", Codec: camsim.H264, FPS: 10, Audio: true, Main: camsim.Resolution{Width: 320, Height: 180}}},
		WorkDir: t.TempDir(),
	}, camsim.WithLogger(log))
	if errors.Is(err, camsim.ErrMissingTool) {
		skipOrFail(t, err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Stop)
	if err := sim.Start(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "hive.db"), make([]byte, 32))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return &encHive{t: t, ctx: ctx, log: log, ports: ports, root: t.TempDir(), keys: t.TempDir(), st: st, simRTSP: ports[0]}
}

// start starts the hive: the archive key opens locked or off, as after
// every start, and the engine records again.
func (h *encHive) start(apiPort, webrtcPort int) {
	h.t.Helper()
	h.vault = archivekey.New(h.keys)
	h.idx = archive.NewIndex(h.st, archive.WithOpener(h.vault.OpenSegment))
	eng, err := video.NewMediaMTX(video.MediaMTXConfig{
		Host: "127.0.0.1", APIPort: apiPort, WebRTCPort: webrtcPort,
		ArchiveDir: h.root, SegmentDuration: 2 * time.Second, WorkDir: h.t.TempDir(),
	}, video.WithLogger(h.log))
	if errors.Is(err, video.ErrMissingTool) {
		skipOrFail(h.t, err)
	}
	if err != nil {
		h.t.Fatal(err)
	}
	h.t.Cleanup(func() { _ = eng.Stop() })
	if err := eng.Start(h.ctx); err != nil {
		h.t.Fatal(err)
	}
	h.eng = eng
	before := len(h.files())
	if err := eng.AddSource(h.ctx, video.Source{Camera: camID, Stream: video.Main, Audio: true, URL: fmt.Sprintf("rtsp://127.0.0.1:%d/enc/main", h.simRTSP)}); err != nil {
		h.t.Fatal(err)
	}
	h.waitFiles(before + 3)
	h.runStart = append(h.runStart, h.newestBefore(before))
}

// files lists the stream's files, sealed or not, oldest first.
func (h *encHive) files() []string {
	entries, _ := os.ReadDir(filepath.Join(h.root, camID, "main"))
	var out []string
	for _, e := range entries {
		if _, ok := archive.ParsePath(h.root, filepath.Join(h.root, camID, "main", e.Name())); ok {
			out = append(out, e.Name())
		}
	}
	return out
}

func (h *encHive) waitFiles(n int) {
	h.t.Helper()
	deadline := time.Now().Add(40 * time.Second)
	for len(h.files()) < n && time.Now().Before(deadline) {
		time.Sleep(250 * time.Millisecond)
	}
	if len(h.files()) < n {
		h.t.Fatalf("%d segment files, want %d", len(h.files()), n)
	}
}

// newestBefore is the start of the first file after the first n.
func (h *encHive) newestBefore(n int) time.Time {
	seg, _ := archive.ParsePath(h.root, filepath.Join(h.root, camID, "main", h.files()[n]))
	return seg.Start
}

func (h *encHive) seal() {
	h.t.Helper()
	if _, err := h.idx.Seal(h.ctx, h.root, h.vault.Recipient(), time.Now()); err != nil {
		h.t.Fatal(err)
	}
	if _, err := h.idx.Reconcile(h.ctx, h.root); err != nil {
		h.t.Fatal(err)
	}
}

// span reads up to a minute of the stream from start.
func (h *encHive) span(start time.Time) ([]byte, error) {
	body, err := archive.NewReader(h.root, h.vault.OpenSegment).Span(h.ctx, camID, "main", start, time.Minute)
	if err != nil {
		return nil, err
	}
	defer func() { _ = body.Close() }()
	return io.ReadAll(body)
}

// A hive with encryption on records across a restart without its owner,
// plays nothing until unlocked and everything after; a copy of its disk
// opens nothing; retention and exports work on the sealed files.
func TestTheEncryptedArchiveAcrossARestartWithoutTheOwner(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	h := newEncHive(t, ctx)
	h.start(h.ports[3], h.ports[4])
	if err := h.vault.Enable(pass); err != nil {
		t.Fatal(err)
	}
	h.seal()
	sealedFirstRun := countSealed(h.files())
	if sealedFirstRun < 2 {
		t.Fatalf("first run: %v; want the closed segments sealed", h.files())
	}

	// The power goes: the engine dies mid-segment, the key with the
	// process. The hive starts again without the owner.
	_ = h.eng.Stop()
	h.start(h.ports[5], h.ports[6])
	if h.vault.State() != archivekey.Locked {
		t.Fatalf("after the restart: %s, want locked", h.vault.State())
	}
	h.seal() // reconciliation: the first run's last segment, cut off, is sealed too
	files := h.files()
	if plain := len(files) - countSealed(files); plain != 1 {
		t.Fatalf("after reconciling: %v; want only the segment being written in clear", files)
	}

	if _, err := h.span(h.runStart[0]); !errors.Is(err, archive.ErrLocked) {
		t.Fatalf("playback while locked: %v, want ErrLocked", err)
	}
	h.assertDiskCopyOpensNothing()

	if err := h.vault.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	for i, start := range h.runStart {
		data, err := h.span(start)
		if err != nil {
			t.Fatalf("run %d after unlocking: %v", i, err)
		}
		if d, err := archive.FMP4Duration(data); err != nil || d < 2*time.Second {
			t.Fatalf("run %d plays %v, %v; want its seconds of footage", i, d, err)
		}
	}
	h.assertExportIsValid(h.runStart[0])
	h.assertRetentionRemovesSealedFiles()
}

func countSealed(files []string) int {
	n := 0
	for _, f := range files {
		if archive.IsSealed(f) {
			n++
		}
	}
	return n
}

// A copy of the disk, archive and keyring, without the passphrase: no
// sealed segment opens, and none holds an MP4 box in clear.
func (h *encHive) assertDiskCopyOpensNothing() {
	h.t.Helper()
	keys := h.t.TempDir()
	copyDir(h.t, h.keys, keys)
	thief := archivekey.New(keys)
	for _, name := range h.files() {
		path := filepath.Join(h.root, camID, "main", name)
		if !archive.IsSealed(name) {
			continue
		}
		if _, err := thief.Open(path); !errors.Is(err, archive.ErrLocked) {
			h.t.Fatalf("%s opened from a copied disk: %v", name, err)
		}
		raw, _ := os.ReadFile(path) //nolint:gosec // the test's archive
		if bytes.Contains(raw, []byte("ftyp")) || bytes.Contains(raw, []byte("moof")) {
			h.t.Fatalf("%s holds MP4 boxes in clear", name)
		}
	}
}

func copyDir(t *testing.T, from, to string) {
	t.Helper()
	entries, err := os.ReadDir(from)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		raw, err := os.ReadFile(filepath.Join(from, e.Name())) //nolint:gosec // the test's own files
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(to, e.Name()), raw, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

// An export of sealed footage is stored sealed and downloads as an MP4
// that ffprobe accepts.
func (h *encHive) assertExportIsValid(start time.Time) {
	h.t.Helper()
	reader := archive.NewReader(h.root, h.vault.OpenSegment)
	fetch := func(ctx context.Context, req export.Request) (io.ReadCloser, error) {
		return reader.Span(ctx, req.Camera, req.Stream, req.Start, req.Duration)
	}
	m := export.NewManager(h.t.TempDir(), fetch, h.st.Exports(), export.WithVault(h.vault))
	job, err := m.Start(h.ctx, export.Request{Camera: camID, Stream: "main", Start: start, Duration: 3 * time.Second, Owner: "acc"})
	if err != nil {
		h.t.Fatal(err)
	}
	deadline := time.Now().Add(20 * time.Second)
	for job.Status == export.StatusRunning && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
		job, _ = m.Get(h.ctx, job.ID)
	}
	if job.Status != export.StatusDone {
		h.t.Fatalf("export %s: %s", job.Status, job.Error)
	}
	if stored, _ := m.FilePath(h.ctx, job.ID); !archive.IsSealed(stored) {
		h.t.Fatalf("export stored as %s, want sealed", stored)
	}
	f, err := m.Open(h.ctx, job.ID)
	if err != nil {
		h.t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	plain, err := io.ReadAll(io.NewSectionReader(f, 0, f.Size()))
	if err != nil {
		h.t.Fatal(err)
	}
	out := filepath.Join(h.t.TempDir(), "export.mp4")
	if err := os.WriteFile(out, plain, 0o600); err != nil {
		h.t.Fatal(err)
	}
	ffprobe, err := exec.LookPath("ffprobe")
	if err != nil {
		h.t.Log("ffprobe not installed; skipping container validation")
		return
	}
	got, err := exec.CommandContext(h.ctx, ffprobe, "-v", "error", "-show_entries", "format=duration", "-of", "csv=p=0", out).Output() //nolint:gosec // test tool with fixed args
	if err != nil {
		h.t.Fatalf("ffprobe rejected the export: %v", err)
	}
	if d, err := strconv.ParseFloat(strings.TrimSpace(string(got)), 64); err != nil || d < 2 || d > 4.5 {
		h.t.Fatalf("export lasts %q, want about 3 s", got)
	}
}

// Retention deletes sealed segments like plain ones, without the key.
func (h *encHive) assertRetentionRemovesSealedFiles() {
	h.t.Helper()
	h.vault.Lock()
	sw := retention.New(h.idx, h.st.Segments(), retention.Policy{DefaultDays: 1}, retention.Options{
		ArchiveDir: h.root, Now: func() time.Time { return time.Now().Add(72 * time.Hour) },
	})
	if _, err := sw.Sweep(h.ctx); err != nil {
		h.t.Fatal(err)
	}
	if n := countSealed(h.files()); n != 0 {
		h.t.Fatalf("%d sealed segments left after retention: %v", n, h.files())
	}
	rows, _ := h.idx.All(h.ctx)
	for _, r := range rows {
		if r.Sealed() {
			h.t.Fatalf("index still lists %s", r.Path)
		}
	}
}

func skipOrFail(t *testing.T, err error) {
	t.Helper()
	if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
		t.Fatalf("CI requires tools: %v", err)
	}
	t.Skipf("skipping: %v", err)
}
