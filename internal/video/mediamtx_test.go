package video

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func testCfg() MediaMTXConfig {
	return MediaMTXConfig{
		Host: "127.0.0.1", APIPort: 19997, PlaybackPort: 19996, WebRTCPort: 18889,
		ArchiveDir: "/var/lib/camhive/archive", SegmentDuration: 10 * time.Second,
	}
}

func TestPathNameIsCameraSlashStream(t *testing.T) {
	if got := pathName("front", Main); got != "front/main" {
		t.Fatalf("pathName = %q", got)
	}
}

var testPub = publisher{port: 18554, user: "hive", pass: "0e12"}

func TestBaseYAMLEnablesOnlyWhatTheHiveUses(t *testing.T) {
	y := testCfg().baseYAML(testPub)
	for _, want := range []string{
		"api: yes", "apiAddress: 127.0.0.1:19997",
		"playback: yes", "playbackAddress: 127.0.0.1:19996",
		"webrtc: yes", "webrtcAddress: 127.0.0.1:18889",
		// Media (ICE over UDP) must reach browsers on the LAN; signalling
		// stays with the Hive, which checks access tokens first.
		"webrtcLocalUDPAddress: :18890",
		"rtmp: no", "hls: no", "srt: no", "moq: no",
		"paths: {}",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("yaml missing %q\n%s", want, y)
		}
	}
}

// The Hive publishes camera streams to the engine over RTSP on loopback
// (ADR-0015). Only the Hive, holding this start's random password, may
// publish; reading (WHEP) and playback stay open as before and the API stays
// loopback-only.
func TestBaseYAMLOpensLoopbackRTSPForTheHiveOnly(t *testing.T) {
	y := testCfg().baseYAML(testPub)
	for _, want := range []string{
		"rtsp: yes\nrtspAddress: 127.0.0.1:18554\nrtspTransports: [tcp]\nrtspEncryption: \"no\"\n",
		"authMethod: internal\n",
		"  - user: any\n    pass: \"\"\n    ips: []\n    permissions:\n      - action: read\n      - action: playback\n",
		"  - user: any\n    pass: \"\"\n    ips: [127.0.0.1, \"::1\"]\n    permissions:\n      - action: api\n",
		"  - user: \"hive\"\n    pass: \"0e12\"\n    ips: [127.0.0.1, \"::1\"]\n    permissions:\n      - action: publish\n",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("yaml missing %q\n%s", want, y)
		}
	}
	if strings.Count(y, "action: publish") != 1 {
		t.Errorf("publish must be granted exactly once, to the hive\n%s", y)
	}
}

func TestPublisherURLCarriesCredentialsAndPath(t *testing.T) {
	got := testPub.url("cam_1/main")
	if got != "rtsp://hive:0e12@127.0.0.1:18554/cam_1/main" {
		t.Fatalf("url = %s", got)
	}
	p, err := newPublisher(0)
	if err != nil {
		t.Fatal(err)
	}
	q, _ := newPublisher(0)
	if p.port == 0 || len(p.pass) < 32 || p.pass == q.pass {
		t.Fatalf("publisher must get a free port and a fresh random password: %+v %+v", p, q)
	}
	if fixed, _ := newPublisher(18555); fixed.port != 18555 {
		t.Fatalf("configured port ignored: %d", fixed.port)
	}
}

func TestPathConfigRecordsFMP4UnderArchiveDirWithoutAutoDelete(t *testing.T) {
	cfg := testCfg()
	body := cfg.pathConfig()
	if strings.Contains(string(body), "rtsp://") {
		t.Fatalf("camera URL (and its credentials) must not reach the engine: %s", body)
	}
	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		t.Fatal(err)
	}
	checks := map[string]any{
		"source":                "publisher", // the Hive pulls the camera and publishes (ADR-0015)
		"overridePublisher":     true,
		"record":                true,
		"recordFormat":          "fmp4",
		"recordSegmentDuration": "10s",
		"recordDeleteAfter":     "0s", // retention is the Hive's job, not the engine's
	}
	for k, want := range checks {
		if m[k] != want {
			t.Errorf("%s = %v, want %v", k, m[k], want)
		}
	}
	rp, _ := m["recordPath"].(string)
	if !strings.HasPrefix(rp, "/var/lib/camhive/archive/") || !strings.Contains(rp, "%path") || !strings.Contains(rp, "%Y-%m-%d_%H-%M-%S-%f_%z") {
		t.Errorf("recordPath = %q", rp)
	}
}

func TestPlaybackAndWHEPURLs(t *testing.T) {
	cfg := testCfg()
	start := time.Date(2026, 11, 9, 10, 30, 0, 0, time.UTC)
	got := cfg.PlaybackURL("front", Sub, start, 90*time.Second)
	want := "http://127.0.0.1:19996/get?duration=90&format=fmp4&path=front%2Fsub&start=2026-11-09T10%3A30%3A00Z"
	if got != want {
		t.Fatalf("PlaybackURL\n got %s\nwant %s", got, want)
	}
	if got := cfg.WHEPURL("front", Main); got != "http://127.0.0.1:18889/front/main/whep" {
		t.Fatalf("WHEPURL = %s", got)
	}
}

func TestSameSourceComparesEverythingThatNeedsANewPull(t *testing.T) {
	a := Source{Camera: "front", Stream: Main, URL: "rtsp://cam/main"}
	b := a
	b.Audio = true
	if a == b {
		t.Fatal("switching audio must count as a different source")
	}
}

func TestParseStatusFromMediaMTXPathJSON(t *testing.T) {
	raw := `{"name":"front/main","ready":true,"tracks":["H264","MPEG-4 Audio"],"readers":[{"type":"webRTCSession","id":"x"}],"bytesReceived":12345}`
	st, err := parseStatus([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if !st.Ready || st.BytesReceived != 12345 || st.Readers != 1 || len(st.Tracks) != 2 || st.Tracks[0] != "H264" {
		t.Fatalf("status = %+v", st)
	}
}

func TestParseSegmentsSortedOldestFirstAndNullDuration(t *testing.T) {
	raw := `{"name":"front/main","segments":[
		{"start":"2026-11-09T10:00:10Z","duration":10},
		{"start":"2026-11-09T10:00:00Z","duration":null}]}`
	segs, err := parseSegments([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	if len(segs) != 2 || !segs[0].Start.Before(segs[1].Start) {
		t.Fatalf("segments not sorted: %+v", segs)
	}
	if segs[0].Duration != 0 || segs[1].Duration != 10*time.Second {
		t.Fatalf("durations = %v, %v", segs[0].Duration, segs[1].Duration)
	}
}

func TestIsAlreadyExistsErrorBody(t *testing.T) {
	if !isAlreadyExists([]byte(`{"status":"error","error":"path already exists"}`)) {
		t.Error("should detect 'already exists'")
	}
	if isAlreadyExists([]byte(`{"status":"error","error":"invalid source"}`)) {
		t.Error("other errors are not 'already exists'")
	}
}

func TestConfigValidationAndDefaults(t *testing.T) {
	var c MediaMTXConfig
	if err := c.validate(); err == nil {
		t.Error("ArchiveDir is required")
	}
	c = MediaMTXConfig{ArchiveDir: "/x"}
	c.applyDefaults()
	if c.Host == "" || c.APIPort == 0 || c.PlaybackPort == 0 || c.WebRTCPort == 0 || c.SegmentDuration == 0 {
		t.Fatalf("defaults missing: %+v", c)
	}
	if err := c.validate(); err != nil {
		t.Fatal(err)
	}
}

func TestBaseYAMLRendersICEServersForTURN(t *testing.T) {
	cfg := testCfg()
	cfg.ICEServers = []ICEServer{
		{URL: "turn:turn.camhive.app:3478", Username: "hive", Password: "s3cret"},
		{URL: "stun:stun.camhive.app:3478"},
	}
	y := cfg.baseYAML(testPub)
	for _, want := range []string{
		"webrtcICEServers2:",
		"  - url: turn:turn.camhive.app:3478\n    username: hive\n    password: s3cret\n",
		"  - url: stun:stun.camhive.app:3478\n",
	} {
		if !strings.Contains(y, want) {
			t.Errorf("yaml missing %q\n%s", want, y)
		}
	}
	if strings.Contains(testCfg().baseYAML(testPub), "webrtcICEServers2:\n  -") {
		t.Error("no ICE servers configured must render none")
	}
}

func TestWriteConfigCreatesDirectoriesAndRejectsUnusableOnes(t *testing.T) {
	m := &MediaMTX{MediaMTXConfig: testCfg()}
	m.ArchiveDir = filepath.Join(t.TempDir(), "archive")
	path, pub, err := m.writeConfig()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(m.WorkDir) })
	y, _ := os.ReadFile(path) //nolint:gosec // test temp dir
	if pub.pass == "" || !strings.Contains(string(y), pub.pass) {
		t.Fatalf("config at %s does not carry this start's publisher", path)
	}
	if _, err := os.Stat(m.ArchiveDir); err != nil {
		t.Fatalf("archive dir not created: %v", err)
	}

	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	bad := &MediaMTX{MediaMTXConfig: testCfg()}
	bad.WorkDir = filepath.Join(file, "work")
	if _, _, err := bad.writeConfig(); err == nil {
		t.Error("a work dir under a file must fail")
	}
	bad.WorkDir, bad.ArchiveDir = t.TempDir(), filepath.Join(file, "archive")
	if _, _, err := bad.writeConfig(); err == nil {
		t.Error("an archive dir under a file must fail")
	}
}
