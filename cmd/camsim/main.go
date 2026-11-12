// Command camsim runs simulated IP cameras for development and tests.
//
//	camsim --camera front:h264 --camera yard:h265:audio --rtsp-port 8554 --onvif-port 8080
//	camsim --profile quirky --user admin --pass secret
//
// Each camera publishes main and sub RTSP streams through MediaMTX and is
// discoverable through an ONVIF mock with device, media and PTZ services.
// Stop with Ctrl-C.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/camhive/hive/internal/camsim"
	"github.com/camhive/hive/internal/version"
)

type cameraFlags []camsim.Camera

func (c *cameraFlags) String() string { return fmt.Sprint(*c) }

// Set parses "name[:codec][:audio]", e.g. "front", "yard:h265" or
// "door:h264:audio" (a camera with a microphone).
func (c *cameraFlags) Set(v string) error {
	name, rest, _ := strings.Cut(v, ":")
	if name == "" {
		return errors.New("camera name is empty")
	}
	codec, opt, _ := strings.Cut(rest, ":")
	if opt != "" && opt != "audio" {
		return fmt.Errorf("camera %q: unknown option %q (want audio)", name, opt)
	}
	*c = append(*c, camsim.Camera{Name: name, Codec: camsim.Codec(codec), Audio: opt == "audio"})
	return nil
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr *os.File) int {
	fs := flag.NewFlagSet("camsim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var cams cameraFlags
	fs.Var(&cams, "camera", "camera as name[:h264|h265][:audio]; repeatable (default: front:h264, yard:h265)")
	host := fs.String("host", "127.0.0.1", "advertised host")
	rtspPort := fs.Int("rtsp-port", 8554, "RTSP port")
	apiPort := fs.Int("api-port", 9997, "MediaMTX API port")
	onvifPort := fs.Int("onvif-port", 8080, "ONVIF port")
	fps := fs.Int("fps", 15, "frame rate")
	profile := fs.String("profile", "normal", "ONVIF behaviour: normal | quirky (no media XAddr in capabilities, query in stream URI, slow)")
	user := fs.String("user", "", "ONVIF username; when set, WS-UsernameToken digest auth is required")
	pass := fs.String("pass", "", "ONVIF password")
	workDir := fs.String("workdir", "", "directory for generated config and logs (default: temp)")
	showVersion := fs.Bool("version", false, "print version and exit")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *showVersion {
		fmt.Fprintln(stdout, "camhive-camsim", version.Current())
		return 0
	}
	if len(cams) == 0 {
		cams = cameraFlags{{Name: "front", Codec: camsim.H264}, {Name: "yard", Codec: camsim.H265}}
	}
	for i := range cams {
		cams[i].FPS = *fps
	}

	log := slog.New(slog.NewTextHandler(stderr, nil))
	sim, err := camsim.New(camsim.Config{
		Host: *host, RTSPPort: *rtspPort, APIPort: *apiPort, ONVIFPort: *onvifPort,
		Cameras: cams, WorkDir: *workDir,
		Profile: camsim.Profile(*profile), Username: *user, Password: *pass,
	}, camsim.WithLogger(log))
	if err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	defer sim.Stop()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	startCtx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()
	if err := sim.Start(startCtx); err != nil {
		fmt.Fprintln(stderr, err)
		return 1
	}
	cfg := sim.Config()
	fmt.Fprintf(stdout, "camsim ready — ONVIF http://%s:%d/onvif/device_service\n", cfg.Host, cfg.ONVIFPort)
	for _, cam := range cfg.Cameras {
		fmt.Fprintf(stdout, "  %-8s %s  %s\n", cam.Name, cfg.RTSPURL(cam, camsim.Main), cfg.RTSPURL(cam, camsim.Sub))
	}
	<-ctx.Done()
	fmt.Fprintln(stdout, "camsim stopping")
	return 0
}
