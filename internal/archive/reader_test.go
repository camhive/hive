package archive_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
)

// testdata/recording holds three segments MediaMTX v1.21.3 recorded from
// camsim (H.264 and G.711 stored as 16-bit LPCM, 2 s segments, 1 s parts).
// The digests are of the answers MediaMTX's own playback server (/get,
// format=fmp4) gave for the same spans before the Hive took playback over
// (ADR-0019 §6): the Hive's reader must give the same bytes.
var engineAnswers = []struct {
	name   string
	start  string
	length time.Duration
	sha256 string
}{
	{"inside one segment", "2026-11-15T23:22:01Z", time.Second, "60709c6d2fba61929909ab7470d98c8afa2413b6432284e0a4957a93272a723e"},
	{"across a segment boundary", "2026-11-15T23:22:01Z", 3 * time.Second, "441509ba56a345aaa3dcd646a2986e68eb9607cf0c43984a815bb9b00e1056d7"},
	{"starting before the footage", "2026-11-15T23:21:57Z", 4 * time.Second, "032a85b34084463a14a1a1cc31b4fe7c49e3d50c8007af54f3a10849e66c6973"},
	{"all of it", "2026-11-15T23:21:59Z", time.Minute, "dbc4dbce9daa0ce48961230d74457bba7b3b2830a450f8832bfaff5a6c994694"},
	{"running past the end", "2026-11-15T23:22:06Z", 10 * time.Second, "fe4d98822e64a70389961a7c6fa15f1ca6bfcdde8ad7c960d9e9f69f2b445b70"},
}

func TestReaderGivesTheSameBytesAsTheEnginePlaybackServer(t *testing.T) {
	r := archive.NewReader("testdata/recording", archive.OpenPlain)
	for _, c := range engineAnswers {
		t.Run(c.name, func(t *testing.T) {
			start, _ := time.Parse(time.RFC3339, c.start)
			got := readSpan(t, r, start, c.length)
			sum := sha256.Sum256(got)
			if hex.EncodeToString(sum[:]) != c.sha256 {
				out := filepath.Join(t.TempDir(), "got.mp4")
				_ = os.WriteFile(out, got, 0o600)
				t.Fatalf("%d bytes differ from the engine's answer; written to %s", len(got), out)
			}
		})
	}
}

// seekAnswers start past the first fragment of a segment, where the reader
// skips the fragments before the start (issue #100). The digests are of
// what the reader gave before that, when it read every fragment from the
// start of the segment: skipping must not change a byte. The camera's
// groups of pictures are long, so most of them still resume at the
// segment's first fragment; the 3 s span from 02.9 resumes past it with
// both tracks.
var seekAnswers = []struct {
	start  string
	length time.Duration
	sha256 string
}{
	{"2026-11-15T23:22:02.2Z", time.Second, "26ba324167002d3659e76794450be1dfaef589ae421536928d7f682babb996b8"},
	{"2026-11-15T23:22:02.9Z", 3 * time.Second, "980f3aa859d87f87becbc2c92eecae46dc2a33642d0c30607d189767e742930e"},
	{"2026-11-15T23:22:04.9Z", 500 * time.Millisecond, "00a25031883dc0f220cc0cf8f660e8307ab266894c265ffbc9e3b82135ac91a7"},
	{"2026-11-15T23:22:06.3Z", 10 * time.Second, "ee34df9a4cade58ca225e67324c56003d5472f718a6f96d66df06cb0e5e93bc6"},
}

func TestSeekingPastTheFirstFragmentGivesTheSameBytes(t *testing.T) {
	r := archive.NewReader("testdata/recording", archive.OpenPlain)
	for _, c := range seekAnswers {
		start, _ := time.Parse(time.RFC3339Nano, c.start)
		got := readSpan(t, r, start, c.length)
		sum := sha256.Sum256(got)
		if hex.EncodeToString(sum[:]) != c.sha256 {
			t.Errorf("%s +%v: %d bytes, digest %x", c.start, c.length, len(got), sum)
		}
	}
}

func TestReaderFindsNoFootageAfterTheRecording(t *testing.T) {
	r := archive.NewReader("testdata/recording", archive.OpenPlain)
	start, _ := time.Parse(time.RFC3339, "2026-11-15T23:22:35Z")
	if _, err := r.Span(context.Background(), "cam_gold", "main", start, 10*time.Second); !errors.Is(err, archive.ErrNoFootage) {
		t.Fatalf("err = %v, want ErrNoFootage", err)
	}
	if _, err := r.Span(context.Background(), "cam_none", "main", start, 10*time.Second); !errors.Is(err, archive.ErrNoFootage) {
		t.Fatalf("unknown camera: err = %v, want ErrNoFootage", err)
	}
}

func readSpan(t *testing.T, r *archive.Reader, start time.Time, length time.Duration) []byte {
	t.Helper()
	body, err := r.Span(context.Background(), "cam_gold", "main", start, length)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = body.Close() }()
	got, err := io.ReadAll(body)
	if err != nil {
		t.Fatal(err)
	}
	return got
}
