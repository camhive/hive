package update

import (
	"context"
	"log/slog"
	"os"
	"time"
)

// Process is a running Hive.
type Process interface {
	Wait() error // returns when the process exits
	Stop() error // asks it to exit, then makes it
}

// Runner starts a Hive version's executable.
type Runner interface {
	Start(ctx context.Context, v Version, exe string) (Process, error)
}

// Health is what a running Hive says about itself on its loopback API.
type Health struct {
	Version   Version
	Recording int // cameras recording
}

// HealthFunc asks the running Hive how it is.
type HealthFunc func(ctx context.Context) (Health, error)

// Channel is where updates come from: the signed target for this platform
// and its download (Source and Offer behind it).
type Channel interface {
	Check(ctx context.Context) (Target, error)
	Fetch(ctx context.Context, dst string) error
}

// Policy is how the owner wants updates (ADR-0018 §6, #82): the mode, the
// window, and the channel's source (nil: no updates, a dev build or no
// trusted root).
type Policy struct {
	Mode   Mode
	Window Window
	Source Channel
}

// SupervisorConfig wires a Supervisor.
type SupervisorConfig struct {
	Versions *Versions
	Runner   Runner
	Health   HealthFunc
	// Policy is asked at every check, so a changed setting applies at the
	// next one without a restart.
	Policy        func() Policy
	Hive          string        // the Hive's id, for the rollout
	Updater       Version       // this updater's version
	CheckEvery    time.Duration // default 1 h
	HealthTimeout time.Duration // default 5 min (ADR-0005)
	Poll          time.Duration // health polling; default 5 s
	Restart       time.Duration // pause before restarting a crashed Hive; default 5 s
	Report        func(kind string, data map[string]any)
	Logger        *slog.Logger
	Now           func() time.Time
	// NotBefore: no update check while the clock reads earlier (the
	// updater's build time). A clock in the past would accept expired
	// metadata (security review).
	NotBefore time.Time
}

// Supervisor runs the current Hive version, restarts it when it dies, and
// installs updates with a health check and a rollback (ADR-0018 §4). One
// goroutine owns the child process: Run.
type Supervisor struct {
	cfg    SupervisorConfig
	proc   Process
	gen    int
	exited chan exit
	checks chan checkRequest
	told   string // the version last reported as available
}

type checkRequest struct {
	asked bool
	done  chan struct{}
}

type exit struct {
	gen int
	err error
}

// NewSupervisor fills defaults.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	def := func(d *time.Duration, v time.Duration) {
		if *d == 0 {
			*d = v
		}
	}
	def(&cfg.CheckEvery, time.Hour)
	def(&cfg.HealthTimeout, 5*time.Minute)
	def(&cfg.Poll, 5*time.Second)
	def(&cfg.Restart, 5*time.Second)
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	if cfg.Report == nil {
		cfg.Report = func(string, map[string]any) {}
	}
	if cfg.Now == nil {
		cfg.Now = time.Now
	}
	if cfg.Policy == nil {
		cfg.Policy = func() Policy { return Policy{} }
	}
	return &Supervisor{cfg: cfg, exited: make(chan exit, 4), checks: make(chan checkRequest)}
}

// Run starts the Hive, checks for updates at once and then every
// CheckEvery, and stops the Hive when ctx ends.
func (s *Supervisor) Run(ctx context.Context) {
	s.start(ctx)
	s.check(ctx, false)
	t := time.NewTicker(s.cfg.CheckEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			s.stop()
			return
		case e := <-s.exited:
			if e.gen != s.gen {
				continue // a process we stopped ourselves
			}
			s.cfg.Logger.Warn("hive exited; restarting", "err", e.err)
			s.proc = nil
			if !sleep(ctx, s.cfg.Restart) {
				return
			}
			s.start(ctx)
		case <-t.C:
			s.check(ctx, false)
		case req := <-s.checks:
			s.check(ctx, req.asked)
			close(req.done)
		}
	}
}

// CheckNow runs one update check in Run's goroutine and waits for it.
func (s *Supervisor) CheckNow(ctx context.Context) { s.request(ctx, false) }

// AskNow is the owner asking to update now (`hive update`): in manual mode
// it installs what is offered, in any mode it ignores the window.
func (s *Supervisor) AskNow(ctx context.Context) { s.request(ctx, true) }

func (s *Supervisor) request(ctx context.Context, asked bool) {
	req := checkRequest{asked: asked, done: make(chan struct{})}
	select {
	case s.checks <- req:
		<-req.done
	case <-ctx.Done():
	}
}

func sleep(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// start runs the current version; a start error counts as an exit.
func (s *Supervisor) start(ctx context.Context) {
	s.gen++
	gen := s.gen
	cur, err := s.cfg.Versions.Current()
	if err != nil {
		s.exited <- exit{gen: gen, err: err}
		return
	}
	if err := s.cfg.Versions.Verify(cur); err != nil {
		// Never run a bundle that changed since it was installed; go back.
		s.cfg.Logger.Error("refusing to start a changed bundle", "version", cur, "err", err)
		s.cfg.Report("update.integrity_failed", map[string]any{"version": cur.String()})
		if prev, rerr := s.cfg.Versions.Rollback(); rerr == nil && s.cfg.Versions.Verify(prev) == nil {
			cur = prev
		} else {
			s.exited <- exit{gen: gen, err: err}
			return
		}
	}
	p, err := s.cfg.Runner.Start(ctx, cur, s.cfg.Versions.File(cur, BundleFiles[0]))
	if err != nil {
		s.exited <- exit{gen: gen, err: err}
		return
	}
	s.proc = p
	go func() { s.exited <- exit{gen: gen, err: p.Wait()} }()
}

func (s *Supervisor) stop() {
	if s.proc != nil {
		_ = s.proc.Stop()
		s.proc = nil
	}
	s.gen++ // its exit is ours, not a crash
}

func (s *Supervisor) restart(ctx context.Context) {
	s.stop()
	s.start(ctx)
}

// check asks the channel and installs what the policy says to.
func (s *Supervisor) check(ctx context.Context, asked bool) {
	policy := s.cfg.Policy()
	if policy.Source == nil {
		return // no channel: a dev build or no trusted root
	}
	if now := s.cfg.Now(); now.Before(s.cfg.NotBefore) {
		s.cfg.Logger.Warn("update check skipped: the clock reads earlier than this updater was built", "now", now, "built", s.cfg.NotBefore)
		return
	}
	target, err := policy.Source.Check(ctx)
	if err != nil {
		s.cfg.Logger.Info("update check", "err", err)
		return
	}
	cur, err := s.cfg.Versions.Current()
	if err != nil {
		return
	}
	verdict := s.decide(policy, cur, target, asked)
	s.cfg.Logger.Info("update check", "running", cur, "offered", target.Version, "verdict", verdict)
	switch verdict {
	case UpToDate:
	case Install:
		s.install(ctx, policy.Source, cur, target)
	case Available, Wait, UpdaterTooOld:
		s.tell(verdict, cur, target.Version)
	}
}

// decide applies the policy; when the owner asked, nobody waits for the
// window.
func (s *Supervisor) decide(policy Policy, cur Version, target Target, asked bool) Verdict {
	verdict := Decide(Situation{Hive: s.cfg.Hive, Running: cur, Updater: s.cfg.Updater, Failed: s.cfg.Versions.Failed(),
		Window: policy.Window, Mode: policy.Mode, Asked: asked, Now: s.cfg.Now()}, target)
	if asked && verdict == Wait {
		return Install
	}
	return verdict
}

// tell reports an update the owner should know about, once per version,
// not hourly: available (manual mode, or waiting for the window in
// automatic mode) or needing a newer updater from a package.
func (s *Supervisor) tell(verdict Verdict, cur, offered Version) {
	if s.told == offered.String() {
		return
	}
	s.told = offered.String()
	s.cfg.Report("update."+map[Verdict]string{Available: "available", Wait: "available", UpdaterTooOld: "needs_package"}[verdict],
		map[string]any{"running": cur.String(), "offered": offered.String()})
}

// install downloads, switches, and keeps the new version only when it is
// healthy in time; otherwise it rolls back and marks the version failed.
func (s *Supervisor) install(ctx context.Context, src Channel, from Version, t Target) {
	tmp, err := os.MkdirTemp(s.cfg.Versions.dir, "download-") // fresh, never a path someone prepared
	if err != nil {
		s.cfg.Logger.Warn("update download", "err", err)
		return
	}
	defer func() { _ = os.RemoveAll(tmp) }()
	if err := src.Fetch(ctx, tmp); err != nil {
		s.cfg.Logger.Warn("update download", "version", t.Version, "err", err)
		return
	}
	if err := s.cfg.Versions.Add(t.Version, tmp); err != nil {
		s.cfg.Logger.Warn("update stage", "version", t.Version, "err", err)
		return
	}
	before, _ := s.cfg.Health(ctx)
	if err := s.cfg.Versions.Switch(t.Version); err != nil {
		s.cfg.Logger.Warn("update switch", "err", err)
		return
	}
	s.restart(ctx)
	data := map[string]any{"from": from.String(), "to": t.Version.String()}
	if s.healthy(ctx, t.Version, before.Recording) {
		_ = s.cfg.Versions.Prune()
		s.cfg.Logger.Info("update applied", "from", from, "to", t.Version)
		s.cfg.Report("update.applied", data)
		return
	}
	if _, err := s.cfg.Versions.Rollback(); err != nil {
		s.cfg.Logger.Error("rollback", "err", err)
	}
	s.restart(ctx)
	s.cfg.Logger.Warn("update rolled back", "from", from, "to", t.Version)
	s.cfg.Report("update.rolled_back", data)
}

// healthy waits for the new version to answer with its version and to
// record at least as many cameras as before, within HealthTimeout.
func (s *Supervisor) healthy(ctx context.Context, want Version, recording int) bool {
	deadline := time.NewTimer(s.cfg.HealthTimeout)
	defer deadline.Stop()
	poll := time.NewTicker(s.cfg.Poll)
	defer poll.Stop()
	for {
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return false
		case e := <-s.exited:
			if e.gen == s.gen {
				s.proc = nil
				return false
			}
		case <-poll.C:
			if h, err := s.cfg.Health(ctx); err == nil && h.Version.Compare(want) == 0 && h.Recording >= recording {
				return true
			}
		}
	}
}
