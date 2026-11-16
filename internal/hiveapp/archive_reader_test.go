package hiveapp_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/hiveapp"
)

// ADR-0019 §6: playback and sound come from the hive's own archive reader,
// over a real recording (H.264 with G.711 stored as LPCM, see
// internal/archive/testdata).
func TestArchiveAnswersFromTheRecordingItself(t *testing.T) {
	f, play, cam := newArchiveAPI(t)
	root := t.TempDir()
	if err := os.CopyFS(filepath.Join(root, string(cam.ID)), os.DirFS("../archive/testdata/recording/cam_gold")); err != nil {
		t.Fatal(err)
	}
	reader := archive.NewReader(root, archive.OpenPlain)
	play.real = hiveapp.ArchiveFetch(reader)
	viewer := f.token(t, auth.RoleViewer, string(cam.ID))
	base := "/v0/cameras/" + string(cam.ID) + "/archive/"

	// A start with a fraction counts from its whole second, as the
	// engine's playback URL did; the answer is the span, video only.
	status, body := getRaw(t, f, base+"play?stream=main&start=2026-11-15T23:22:01.700Z&duration=1", viewer)
	if want := playableSpan(t, reader, string(cam.ID), "2026-11-15T23:22:01Z", time.Second); status != 200 || !bytes.Equal(body, want) {
		t.Fatalf("play: %d, %d bytes, want the %d bytes of the span", status, len(body), len(want))
	}

	// Reading stops at the fragment where the video reaches the end, so
	// the sound can end a few tens of milliseconds early (as it did).
	status, body = getRaw(t, f, base+"audio?stream=main&start=2026-11-15T23:22:01Z&duration=3", viewer)
	if secs := float64(len(body)-44) / (2 * 8000); status != 200 || string(body[:4]) != "RIFF" || secs < 2.8 || secs > 3 {
		t.Fatalf("audio: %d, %.2f s, want about 3 s of 8 kHz WAV", status, secs)
	}

	if r := f.do(t, "GET", base+"play?stream=main&start=2026-11-15T23:30:00Z&duration=10", viewer, nil); r.Status != 502 || r.Body["code"] != "engine" {
		t.Fatalf("no footage: %d %v", r.Status, r.Body)
	}
}

func playableSpan(t *testing.T, r *archive.Reader, camera, start string, length time.Duration) []byte {
	t.Helper()
	at, _ := time.Parse(time.RFC3339, start)
	span, err := r.Span(context.Background(), camera, "main", at, length)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = span.Close() }()
	var out bytes.Buffer
	if err := archive.CopyPlayable(&out, span); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func getRaw(t *testing.T, f *apiFixture, path, token string) (int, []byte) {
	t.Helper()
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+path, nil)
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, body
}
