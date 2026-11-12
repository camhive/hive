// Package journal hands what hive-updater did over to the Hive, which
// reports it to the cloud (ADR-0018 §7). The updater has no cloud link of
// its own; both share the data directory.
package journal

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

const (
	file    = "update-events.jsonl"
	sending = "update-events.sending"
)

// Entry is one thing the updater did.
type Entry struct {
	Kind string         `json:"kind"` // update.applied | update.rolled_back | update.available
	At   time.Time      `json:"at"`
	Data map[string]any `json:"data,omitempty"`
}

// Append adds an entry (one JSON line, written with a single append).
func Append(dir, kind string, data map[string]any) error {
	raw, err := json.Marshal(Entry{Kind: kind, At: time.Now().UTC(), Data: data})
	if err != nil {
		return err
	}
	f, err := os.OpenFile(filepath.Join(dir, file), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600) //nolint:gosec // our data directory
	if err != nil {
		return err
	}
	if _, err := f.Write(append(raw, '\n')); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Drain returns every entry not handed over yet, oldest first, and forgets
// them. Entries are moved to a sending file before they are read, so a
// drain that stops halfway leaves them for the next one: an entry is handed
// over at least once, never lost.
func Drain(dir string) ([]Entry, error) {
	pending, fresh := filepath.Join(dir, sending), filepath.Join(dir, file)
	var out []Entry
	for range 2 { // what an earlier drain left, then what came since
		if _, err := os.Stat(pending); errors.Is(err, os.ErrNotExist) {
			if err := os.Rename(fresh, pending); errors.Is(err, os.ErrNotExist) {
				break
			} else if err != nil {
				return out, err
			}
		}
		got, err := read(pending)
		if err != nil {
			return out, err
		}
		out = append(out, got...)
		if err := os.Remove(pending); err != nil {
			return out, err
		}
	}
	return out, nil
}

// read parses JSON lines, skipping a torn or broken line.
func read(path string) ([]Entry, error) {
	f, err := os.Open(path) //nolint:gosec // our data directory
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	var out []Entry
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var e Entry
		if json.Unmarshal(sc.Bytes(), &e) == nil && e.Kind != "" {
			out = append(out, e)
		}
	}
	return out, sc.Err()
}
