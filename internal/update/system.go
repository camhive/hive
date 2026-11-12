package update

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"sync"
	"time"
)

// Platform is this machine's bundle platform, e.g. linux_arm64.
func Platform() string { return runtime.GOOS + "_" + runtime.GOARCH }

// ExecRunner starts `<exe> run` with the updater's environment, output
// going to the updater's own output (the service log).
type ExecRunner struct {
	Args   []string  // default: run
	Stdout io.Writer // default os.Stdout
	Stderr io.Writer // default os.Stderr
	Grace  time.Duration
}

// Start implements Runner.
// The Hive outlives the context of the check that started it: the
// supervisor stops it explicitly.
func (r ExecRunner) Start(_ context.Context, _ Version, exe string) (Process, error) {
	args := r.Args
	if len(args) == 0 {
		args = []string{"run"}
	}
	cmd := exec.Command(exe, args...) //nolint:gosec,noctx // the verified bundle; stopped by Stop, not by a context
	cmd.Stdout, cmd.Stderr = orDefault(r.Stdout, os.Stdout), orDefault(r.Stderr, os.Stderr)
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	grace := r.Grace
	if grace == 0 {
		grace = 30 * time.Second
	}
	p := &execProcess{cmd: cmd, done: make(chan struct{}), grace: grace}
	go func() { p.err = cmd.Wait(); close(p.done) }()
	return p, nil
}

func orDefault(w, d io.Writer) io.Writer {
	if w == nil {
		return d
	}
	return w
}

type execProcess struct {
	cmd   *exec.Cmd
	done  chan struct{}
	err   error
	grace time.Duration
	once  sync.Once
}

// Wait implements Process.
func (p *execProcess) Wait() error { <-p.done; return p.err }

// Stop asks the Hive to stop (it flushes the archive and stops the engine),
// and kills it after the grace period.
func (p *execProcess) Stop() error {
	p.once.Do(func() {
		_ = interrupt(p.cmd.Process)
		select {
		case <-p.done:
		case <-time.After(p.grace):
			_ = p.cmd.Process.Kill()
			<-p.done
		}
	})
	return nil
}

// HTTPHealth asks the Hive's loopback API (e.g. http://127.0.0.1:16016).
func HTTPHealth(base string, client *http.Client) HealthFunc {
	if client == nil {
		client = &http.Client{Timeout: 5 * time.Second}
	}
	return func(ctx context.Context) (Health, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/healthz", nil)
		if err != nil {
			return Health{}, err
		}
		resp, err := client.Do(req)
		if err != nil {
			return Health{}, err
		}
		defer func() { _ = resp.Body.Close() }()
		var body struct {
			Status    string `json:"status"`
			Version   string `json:"version"`
			Recording int    `json:"recording"`
		}
		if resp.StatusCode != http.StatusOK || json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&body) != nil || body.Status != "ok" {
			return Health{}, fmt.Errorf("update: hive not healthy (http %d)", resp.StatusCode)
		}
		v, err := ParseVersion(body.Version)
		if err != nil {
			return Health{}, err
		}
		return Health{Version: v, Recording: body.Recording}, nil
	}
}

// SourceChannel offers a Source's bundle for one platform to the
// Supervisor; Fetch downloads what the last Check offered.
type SourceChannel struct {
	Source   Source
	Platform string
	HTTP     *http.Client

	mu   sync.Mutex
	last *Offer
}

// Check implements Channel.
func (c *SourceChannel) Check(ctx context.Context) (Target, error) {
	o, err := c.Source.Offer(ctx, c.Platform)
	if err != nil {
		return Target{}, err
	}
	c.mu.Lock()
	c.last = &o
	c.mu.Unlock()
	return o.Target, nil
}

// Fetch implements Channel.
func (c *SourceChannel) Fetch(ctx context.Context, dst string) error {
	c.mu.Lock()
	o := c.last
	c.mu.Unlock()
	if o == nil {
		return fmt.Errorf("update: nothing offered yet")
	}
	return o.Download(ctx, c.HTTP, dst)
}
