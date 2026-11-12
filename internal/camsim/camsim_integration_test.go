package camsim_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/bluenviron/gortsplib/v5"
	"github.com/bluenviron/gortsplib/v5/pkg/base"
	"github.com/bluenviron/gortsplib/v5/pkg/description"
	"github.com/bluenviron/gortsplib/v5/pkg/format"
	"github.com/pion/rtp"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/testports"
)

// End to end: start the simulator, discover it through ONVIF, play both
// streams of an H.264 and an H.265 camera with a real RTSP client.
func TestSimulatorServesONVIFAndRTSP(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 3)
	cfg := camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{
			{Name: "front", Codec: camsim.H264, FPS: 10},
			{Name: "yard", Codec: camsim.H265, FPS: 10, Main: camsim.Resolution{Width: 960, Height: 540}},
		},
		WorkDir: t.TempDir(),
	}
	sim, err := camsim.New(cfg, camsim.WithLogger(slog.New(slog.NewTextHandler(os.Stderr, nil))))
	if errors.Is(err, camsim.ErrMissingTool) {
		if os.Getenv("CAMSIM_REQUIRE_TOOLS") != "" {
			t.Fatalf("CI requires camsim tools: %v", err)
		}
		t.Skipf("skipping: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Stop)

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sim.Start(ctx); err != nil {
		t.Fatalf("start: %v\n%s", err, dumpLogs(t, cfg.WorkDir))
	}

	// ONVIF: GetProfiles then GetStreamUri, as the hive will do.
	profiles := soapCall(t, fmt.Sprintf("http://127.0.0.1:%d/onvif/media_service", ports[2]),
		`<trt:GetProfiles xmlns:trt="http://www.onvif.org/ver10/media/wsdl"/>`)
	if !strings.Contains(profiles, `token="prof_yard_main"`) {
		t.Fatalf("profiles missing yard/main:\n%s", profiles)
	}
	uri := soapCall(t, fmt.Sprintf("http://127.0.0.1:%d/onvif/media_service", ports[2]),
		`<trt:GetStreamUri xmlns:trt="http://www.onvif.org/ver10/media/wsdl"><trt:ProfileToken>prof_yard_main</trt:ProfileToken></trt:GetStreamUri>`)
	if !strings.Contains(uri, fmt.Sprintf("rtsp://127.0.0.1:%d/yard/main", ports[0])) {
		t.Fatalf("unexpected stream uri:\n%s", uri)
	}

	cases := []struct {
		url  string
		want string
	}{
		{fmt.Sprintf("rtsp://127.0.0.1:%d/front/main", ports[0]), "H264"},
		{fmt.Sprintf("rtsp://127.0.0.1:%d/front/sub", ports[0]), "H264"},
		{fmt.Sprintf("rtsp://127.0.0.1:%d/yard/main", ports[0]), "H265"},
	}
	for _, tc := range cases {
		t.Run(tc.url, func(t *testing.T) {
			codec, frames := playRTSP(t, tc.url, 5)
			if codec != tc.want {
				t.Errorf("codec = %s, want %s", codec, tc.want)
			}
			if frames < 5 {
				t.Errorf("received %d RTP packets, want >= 5", frames)
			}
		})
	}
}

// playRTSP describes and plays url, returns the video codec name and the
// number of RTP packets received before n were counted or the deadline passed.
func playRTSP(t *testing.T, url string, n int) (string, int) {
	t.Helper()
	u, err := base.ParseURL(url)
	if err != nil {
		t.Fatal(err)
	}
	c := gortsplib.Client{Scheme: u.Scheme, Host: u.Host, ReadTimeout: 10 * time.Second}
	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	desc, _, err := c.Describe(u)
	if err != nil {
		t.Fatalf("describe %s: %v", url, err)
	}
	codec := ""
	var forma format.Format
	for _, m := range desc.Medias {
		for _, f := range m.Formats {
			switch f.(type) {
			case *format.H264:
				codec, forma = "H264", f
			case *format.H265:
				codec, forma = "H265", f
			}
		}
	}
	if forma == nil {
		t.Fatalf("no video format in %s", url)
	}
	if err := c.SetupAll(desc.BaseURL, desc.Medias); err != nil {
		t.Fatal(err)
	}
	got := make(chan struct{}, n)
	c.OnPacketRTPAny(func(_ *description.Media, _ format.Format, _ *rtp.Packet) {
		select {
		case got <- struct{}{}:
		default:
		}
	})
	if _, err := c.Play(nil); err != nil {
		t.Fatal(err)
	}
	count := 0
	deadline := time.After(15 * time.Second)
	for count < n {
		select {
		case <-got:
			count++
		case <-deadline:
			return codec, count
		}
	}
	return codec, count
}

func soapCall(t *testing.T, url, body string) string {
	t.Helper()
	env := `<s:Envelope xmlns:s="http://www.w3.org/2003/05/soap-envelope"><s:Body>` + body + `</s:Body></s:Envelope>`
	resp, err := http.Post(url, "application/soap+xml", strings.NewReader(env))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return string(b)
}

func dumpLogs(t *testing.T, dir string) string {
	t.Helper()
	entries, _ := os.ReadDir(dir)
	var b strings.Builder
	for _, e := range entries {
		if strings.HasSuffix(e.Name(), ".log") {
			data, _ := os.ReadFile(dir + "/" + e.Name())
			b.WriteString("--- " + e.Name() + "\n" + string(data) + "\n")
		}
	}
	return b.String()
}

// A WorkDir that does not exist yet must be created, not fail on first write.
func TestStartCreatesMissingWorkDir(t *testing.T) {
	if testing.Short() {
		t.Skip("integration test")
	}
	ports := testports.Get(t, 3)
	cfg := camsim.Config{
		Host: "127.0.0.1", RTSPPort: ports[0], APIPort: ports[1], ONVIFPort: ports[2],
		Cameras: []camsim.Camera{{Name: "one", Codec: camsim.H264, FPS: 5, Main: camsim.Resolution{Width: 320, Height: 180}}},
		WorkDir: t.TempDir() + "/nested/work",
	}
	sim, err := camsim.New(cfg)
	if errors.Is(err, camsim.ErrMissingTool) {
		t.Skipf("skipping: %v", err)
	}
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sim.Stop)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	if err := sim.Start(ctx); err != nil {
		t.Fatalf("start with missing workdir: %v", err)
	}
	if _, err := os.Stat(cfg.WorkDir + "/mediamtx.yml"); err != nil {
		t.Fatalf("config not written into WorkDir: %v", err)
	}
}
