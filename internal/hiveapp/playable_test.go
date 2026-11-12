package hiveapp_test

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"testing"

	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/fmp4/seekablebuffer"
	"github.com/bluenviron/mediacommon/v2/pkg/formats/mp4/codecs"

	"github.com/camhive/hive/internal/auth"
)

// Seen on the dev stack: a camera with G.711 audio records an LPCM ("ipcm")
// track, Chrome refused the whole answer ("Unsupported audio format") and the
// archive never played. Playback sends the video tracks only.
func TestPlaybackSendsOnlyTheVideoTracks(t *testing.T) {
	f, eng, cam := newArchiveAPI(t)
	eng.body = videoWithLPCM(t)
	req, _ := http.NewRequestWithContext(context.Background(), "GET", f.srv.URL+"/v0/cameras/"+string(cam.ID)+"/archive/play?stream=main&start=2026-11-09T10:00:00Z&duration=60", nil)
	req.Header.Set("Authorization", "Bearer "+f.token(t, auth.RoleViewer, string(cam.ID)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var init fmp4.Init
	if err := init.Unmarshal(bytes.NewReader(body)); err != nil {
		t.Fatalf("%d: %v", resp.StatusCode, err)
	}
	if len(init.Tracks) != 1 || !init.Tracks[0].Codec.IsVideo() {
		t.Fatalf("tracks: %+v", init.Tracks)
	}
}

func videoWithLPCM(t *testing.T) []byte {
	t.Helper()
	sps := []byte{0x67, 0x42, 0xc0, 0x28, 0xd9, 0x00, 0x78, 0x02, 0x27, 0xe5, 0x84, 0x00, 0x00, 0x03, 0x00, 0x04, 0x00, 0x00, 0x03, 0x00, 0xf0, 0x3c, 0x60, 0xc9, 0x20}
	init := fmp4.Init{Tracks: []*fmp4.InitTrack{
		{ID: 1, TimeScale: 90000, Codec: &codecs.H264{SPS: sps, PPS: []byte{0x08}}},
		{ID: 2, TimeScale: 8000, Codec: &codecs.LPCM{BitDepth: 16, SampleRate: 8000, ChannelCount: 1}},
	}}
	part := fmp4.Part{Tracks: []*fmp4.PartTrack{
		{ID: 1, Samples: []*fmp4.Sample{{Duration: 90000, Payload: []byte{0, 0, 0, 1, 0x65}}}},
		{ID: 2, Samples: []*fmp4.Sample{{Duration: 8000, Payload: make([]byte, 16)}}},
	}}
	var buf seekablebuffer.Buffer
	if err := init.Marshal(&buf); err != nil {
		t.Fatal(err)
	}
	if err := part.Marshal(&buf); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}
