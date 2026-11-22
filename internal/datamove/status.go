// Package datamove moves a hive's data directory to a new disk (#155,
// ADR-0024 §6): `hive move-data <new-dir>`. It copies and verifies the
// hive's files in the order of what is hardest to replace, then as much of
// the archive as fits, newest first; it never changes or deletes anything
// on the old disk but its own note, move.json. The note is also how the
// running hive learns that a move is under way: while it says copying, the
// hive starts without recording, and the app shows the progress.
package datamove

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"
)

// File is the move's note in a data directory.
const File = "move.json"

// State of a move.
type State string

// States. The old directory's note goes copying → done; the new one's,
// written once everything is copied, switching → done. A hive records
// unless its own directory's note says copying.
const (
	None      State = "none" // no move was started from here
	Copying   State = "copying"
	Switching State = "switching" // copied; the service is being pointed at the new directory
	Done      State = "done"
	Failed    State = "failed" // cancelled; the hive records from where it is
)

// Status is the note: what the move is doing, or did.
type Status struct {
	State              State      `json:"state"`
	From               string     `json:"from,omitempty"`
	FromOverride       bool       `json:"from_override,omitempty"` // the service ran the hive from From by an earlier move's addition
	To                 string     `json:"to,omitempty"`
	Step               string     `json:"step,omitempty"`
	CopiedBytes        int64      `json:"copied_bytes"`
	PlannedBytes       int64      `json:"planned_bytes"`
	SkippedSegments    int        `json:"skipped_segments"`
	SkippedBytes       int64      `json:"skipped_bytes"`
	SkippedNewest      *time.Time `json:"skipped_newest,omitempty"`
	UnreadableSegments int        `json:"unreadable_segments"`
	UnreadableBytes    int64      `json:"unreadable_bytes"`
	StartedAt          time.Time  `json:"started_at,omitzero"`
	UpdatedAt          time.Time  `json:"updated_at,omitzero"`
	Error              string     `json:"error,omitempty"`
}

// Read returns the note in dataDir; None when there is none.
func Read(dataDir string) (Status, error) {
	raw, err := os.ReadFile(filepath.Join(dataDir, File)) //nolint:gosec // the hive's own data directory
	if errors.Is(err, fs.ErrNotExist) {
		return Status{State: None}, nil
	}
	if err != nil {
		return Status{}, err
	}
	var s Status
	if err := json.Unmarshal(raw, &s); err != nil {
		return Status{}, fmt.Errorf("datamove: %s: %w", File, err)
	}
	return s, nil
}

// Moving reports whether a move from dataDir is under way, so the hive
// must not record: a note it cannot read counts as one.
func Moving(dataDir string) bool {
	s, err := Read(dataDir)
	return err != nil || s.State == Copying
}

// Cancel gives an unfinished move up, from either side. From the old
// directory (copying) the note becomes failed and the hive is restarted to
// record there. From the new one (switching: the service already points
// there, or a start there failed) the service is pointed back at the old
// directory first. Nothing copied is removed.
func Cancel(dataDir string, svc Service, now time.Time) error {
	s, err := Read(dataDir)
	if err != nil {
		return err
	}
	switch s.State {
	case Copying:
		return cancelCopying(dataDir, s, svc, now)
	case Switching:
		return cancelSwitching(dataDir, s, svc, now)
	case None, Done, Failed:
	}
	return fmt.Errorf("datamove: no unfinished move in %s", dataDir)
}

// cancelCopying gives up a move from the old directory and restarts the
// hive there to record.
func cancelCopying(dataDir string, s Status, svc Service, now time.Time) error {
	if err := fail(dataDir, s, now); err != nil {
		return err
	}
	failOther(s.To, now)
	return svc.Restart()
}

// cancelSwitching gives up a move from the new directory: the old one is
// marked first, so the hive records there once the service points back,
// to the definition as it was: as installed (a default install keeps
// hive.yaml in /etc/camhive, which a CAMHIVE_DATA_DIR would hide), or the
// earlier move's addition.
func cancelSwitching(dataDir string, s Status, svc Service, now time.Time) error {
	old, err := Read(s.From)
	if err != nil {
		return err
	}
	if err := fail(s.From, old, now); err != nil {
		return err
	}
	back := "" // the definition as installed runs the hive from the old directory
	if s.FromOverride {
		back = s.From
	}
	if err := svc.Switch(back); err != nil {
		return fmt.Errorf("datamove: run the hive from %s again: %w", s.From, err)
	}
	return fail(dataDir, s, now)
}

func fail(dir string, s Status, now time.Time) error {
	s.State, s.Error, s.UpdatedAt = Failed, "cancelled by the owner", now.UTC()
	return s.write(dir)
}

// failOther marks the new directory's note, if any, cancelled too.
func failOther(dir string, now time.Time) {
	if dir == "" {
		return
	}
	if s, err := Read(dir); err == nil && s.State == Switching {
		_ = fail(dir, s, now)
	}
}

// write replaces the note in dir.
func (s Status) write(dir string) error {
	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := filepath.Join(dir, File+".tmp")
	if err := os.WriteFile(tmp, append(raw, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, File))
}
