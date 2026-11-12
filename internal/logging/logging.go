// Package logging builds the hive's logger (ADR-0011): JSON lines into a
// rotating file in the data dir, plus the console (text when run in the
// foreground, JSON under a service manager), and the redaction helpers
// that keep credentials out of every line.
package logging

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/url"
	"os"
	"strings"

	"gopkg.in/natefinch/lumberjack.v2"
)

// Config describes where and how to log.
type Config struct {
	Level       string    // debug | info | warn | error
	File        string    // rotating JSON file; empty = none
	MaxSizeMB   int       // rotate above this size; default 20
	MaxFiles    int       // rotated files to keep; default 5
	Console     io.Writer // default os.Stderr; nil Console with "" File logs nowhere
	ConsoleJSON bool      // JSON on the console (service mode) instead of text
}

// New builds the logger; closeFn flushes and closes the file.
func New(cfg Config) (log *slog.Logger, closeFn func() error, err error) {
	level, err := ParseLevel(cfg.Level)
	if err != nil {
		return nil, nil, err
	}
	if cfg.MaxSizeMB == 0 {
		cfg.MaxSizeMB = 20
	}
	if cfg.MaxFiles == 0 {
		cfg.MaxFiles = 5
	}
	opts := &slog.HandlerOptions{Level: level}
	var handlers []slog.Handler
	closeFn = func() error { return nil }

	if cfg.Console != nil {
		if cfg.ConsoleJSON {
			handlers = append(handlers, slog.NewJSONHandler(cfg.Console, opts))
		} else {
			handlers = append(handlers, slog.NewTextHandler(cfg.Console, opts))
		}
	}
	if cfg.File != "" {
		lj := &lumberjack.Logger{Filename: cfg.File, MaxSize: cfg.MaxSizeMB, MaxBackups: cfg.MaxFiles, Compress: false}
		handlers = append(handlers, slog.NewJSONHandler(lj, opts))
		closeFn = lj.Close
	}
	switch len(handlers) {
	case 0:
		return slog.New(slog.DiscardHandler), closeFn, nil
	case 1:
		return slog.New(handlers[0]), closeFn, nil
	default:
		return slog.New(fanout(handlers)), closeFn, nil
	}
}

// ParseLevel maps a config string to a slog level.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToLower(s) {
	case "", "info":
		return slog.LevelInfo, nil
	case "debug":
		return slog.LevelDebug, nil
	case "warn", "warning":
		return slog.LevelWarn, nil
	case "error":
		return slog.LevelError, nil
	}
	return 0, fmt.Errorf("logging: unknown level %q (debug, info, warn, error)", s)
}

// RedactURL replaces a password embedded in a URL with ***. Strings that are
// not URLs are returned unchanged.
func RedactURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.User == nil {
		return raw
	}
	if _, has := u.User.Password(); !has {
		return raw
	}
	u.User = url.UserPassword(u.User.Username(), "***")
	// url.String escapes "***"; keep it readable.
	return strings.Replace(u.String(), url.UserPassword(u.User.Username(), "***").String(), u.User.Username()+":***", 1)
}

// fanout sends every record to all handlers.
type fanout []slog.Handler

// Enabled reports whether any handler wants the level.
func (f fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

// Handle delivers the record to every handler that is enabled for it.
func (f fanout) Handle(ctx context.Context, r slog.Record) error {
	var errs []error
	for _, h := range f {
		if h.Enabled(ctx, r.Level) {
			errs = append(errs, h.Handle(ctx, r.Clone()))
		}
	}
	return errors.Join(errs...)
}

// WithAttrs implements slog.Handler.
func (f fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithAttrs(attrs)
	}
	return out
}

// WithGroup implements slog.Handler.
func (f fanout) WithGroup(name string) slog.Handler {
	out := make(fanout, len(f))
	for i, h := range f {
		out[i] = h.WithGroup(name)
	}
	return out
}

// Default is the console logger used before configuration is read.
func Default() *slog.Logger { return slog.New(slog.NewTextHandler(os.Stderr, nil)) }
