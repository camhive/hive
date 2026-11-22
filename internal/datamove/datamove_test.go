package datamove_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/camhive/hive/internal/archive"
	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/datamove"
	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/retention"
	"github.com/camhive/hive/internal/store"
)

func init() { archivekey.WorkFactor = 10 } // fast scrypt

const pass = "correct horse battery staple 2026"

var storeKey = bytes.Repeat([]byte{7}, 32)

var recording = filepath.Join("..", "archive", "testdata", "recording", "cam_gold", "main")

// service is the hive's OS service as the tests see it: the directory
// it runs the hive from, and how its next switch fails.
type service struct {
	restarts int
	switches []string
	runsFrom string
	copying  bool // move.json said copying when the hive was restarted
	from     string
	// failSwitch: the next switch fails and puts the old definition back;
	// failStuck: the next switch fails after pointing the service at the
	// new directory (the rollback failed too).
	failSwitch, failStuck bool
}

func (s *service) Restart() error {
	st, err := datamove.Read(s.from)
	s.copying = err == nil && st.State == datamove.Copying
	s.restarts++
	return nil
}

func (s *service) Switch(dir string) error {
	s.switches = append(s.switches, dir)
	if dir == "" {
		dir = s.from // as installed: the old directory in these tests
	}
	switch {
	case s.failSwitch:
		s.failSwitch = false
		return errors.New("camhive-hive did not start")
	case s.failStuck:
		s.failStuck = false
		s.runsFrom = dir
		return errors.New("camhive-hive did not start, and the old definition could not be put back")
	}
	s.runsFrom = dir
	return nil
}

// oldHive is a claimed hive with an encrypted archive of one camera.
type oldHive struct {
	paths    hiveconfig.Paths
	archive  string
	segments []string // plain bytes of each segment, by file name, newest last
	plain    map[string][]byte
}

func newOldHive(t *testing.T, copies int) oldHive {
	t.Helper()
	paths := hiveconfig.Paths{}.WithDataDir(filepath.Join(t.TempDir(), "old"))
	if err := paths.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := hiveconfig.Load(paths); err != nil { // writes hive.yaml with archive_dir
		t.Fatal(err)
	}
	write(t, paths.Key, "PRIVATE KEY", 0o600)
	write(t, paths.Link, `{"hive_id":"hv_1"}`, 0o600)
	write(t, paths.Cert, "CERT", 0o644)
	write(t, filepath.Join(paths.Data, "exports", "ex_1.mp4"), "an export", 0o600)
	h := oldHive{paths: paths, archive: paths.Archive, plain: map[string][]byte{}}

	vault := archivekey.New(filepath.Join(paths.Data, "archive-key"))
	if err := vault.Enable(pass); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadDir(recording)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(paths.Archive, "cam_gold", "main")
	start := time.Date(2026, 11, 20, 8, 0, 0, 0, time.UTC)
	for i := range copies {
		b, err := os.ReadFile(filepath.Join(recording, src[i%len(src)].Name()))
		if err != nil {
			t.Fatal(err)
		}
		name := start.Add(time.Duration(i)*10*time.Minute).Format("2006-01-02_15-04-05") + "-000000_Z.mp4"
		write(t, filepath.Join(dir, name), string(b), 0o644)
		h.segments = append(h.segments, name)
		h.plain[name] = b
	}
	st, err := store.Open(context.Background(), paths.DB, storeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	idx := archive.NewIndex(st, archive.WithOpener(vault.OpenSegment))
	if _, err := idx.Reconcile(context.Background(), paths.Archive); err != nil {
		t.Fatal(err)
	}
	if _, err := idx.Seal(context.Background(), paths.Archive, vault.Recipient(), time.Now().Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	return h
}

func write(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}

// tree is every file under dir with its SHA-256.
func tree(t *testing.T, dir string) map[string][32]byte {
	t.Helper()
	out := map[string][32]byte{}
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		b, err := os.ReadFile(p) //nolint:gosec // test files
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(dir, p)
		out[rel] = sha256.Sum256(b)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

// bigDisk is a disk with room for everything.
func bigDisk(string) (retention.Disk, error) {
	return retention.Disk{Total: 1 << 40, Free: 1 << 39}, nil
}

func (h oldHive) options(to string, svc *service) datamove.Options {
	svc.from = h.paths.Data
	if svc.runsFrom == "" {
		svc.runsFrom = h.paths.Data
	}
	oldDisk := filepath.Dir(h.paths.Data)
	return datamove.Options{From: h.paths, ArchiveDir: h.archive, To: to, StoreKey: storeKey, Service: svc, Disk: bigDisk, Progress: io.Discard,
		DeviceOf: func(dir string) (uint64, error) { // the old hive's temp directory is the old disk
			if dir == oldDisk || strings.HasPrefix(dir, oldDisk+"/") {
				return 1, nil
			}
			return 2, nil
		}}
}

func TestAMovedHiveStartsUnlocksAndReadsItsEncryptedArchiveThere(t *testing.T) {
	h := newOldHive(t, 3)
	h.unrelatedWALWrite(t)
	before := tree(t, filepath.Dir(h.paths.Data))
	to := filepath.Join(t.TempDir(), "new", "camhive")
	svc := &service{}
	res, err := datamove.Move(context.Background(), h.options(to, svc))
	if err != nil {
		t.Fatal(err)
	}
	if svc.restarts != 1 || !svc.copying || len(svc.switches) != 1 || svc.runsFrom != to {
		t.Fatalf("service: %+v; want one restart into the move, then the hive run from %s", svc, to)
	}
	if res.SkippedSegments != 0 || res.CopiedSegments != 3 {
		t.Fatalf("result = %+v, want 3 segments copied, none skipped", res)
	}
	for _, f := range []string{"hive.key", "cloud.json", "hive.crt", "exports/ex_1.mp4", "archive-key/keyring.age"} {
		a, errA := os.ReadFile(filepath.Join(h.paths.Data, f))
		b, errB := os.ReadFile(filepath.Join(to, f))
		if errA != nil || errB != nil || !bytes.Equal(a, b) {
			t.Fatalf("%s not copied: %v %v", f, errA, errB)
		}
	}
	if st, err := os.Stat(filepath.Join(to, "hive.key")); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("hive.key mode = %v, %v; want 0600", st.Mode(), err)
	}
	newPaths := hiveconfig.Paths{}.WithDataDir(to)
	cfg, _, err := hiveconfig.Load(newPaths)
	if err != nil || cfg.ArchiveDir != filepath.Join(to, "archive") {
		t.Fatalf("new hive.yaml archive_dir = %q, %v", cfg.ArchiveDir, err)
	}

	// It starts there: the database opens, every row is on the new disk, the archive unlocks and reads.
	st, err := store.Open(context.Background(), newPaths.DB, storeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.Segments().All(context.Background())
	if err != nil || len(rows) != 3 {
		t.Fatalf("rows = %d, %v; want 3", len(rows), err)
	}
	vault := archivekey.New(filepath.Join(to, "archive-key"))
	if err := vault.Unlock(pass); err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !strings.HasPrefix(r.Path, cfg.ArchiveDir+string(filepath.Separator)) || !strings.HasSuffix(r.Path, archive.SealedExt) {
			t.Fatalf("row path %s is not a sealed segment on the new disk", r.Path)
		}
		f, err := vault.OpenSegment(archive.Segment{Path: r.Path})
		if err != nil {
			t.Fatal(err)
		}
		got := make([]byte, f.Size())
		if _, err := f.ReadAt(got, 0); err != nil && !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		_ = f.Close()
		name := strings.TrimSuffix(filepath.Base(r.Path), archive.SealedExt)
		if !bytes.Equal(got, h.plain[name]) {
			t.Fatalf("%s does not read back as the recording", name)
		}
	}
	if acts, err := st.Actions().Recent(context.Background(), 10); err != nil || len(acts) != 2 || acts[0].Action != "hive.data_moved" || acts[1].Action != "camera.added" {
		t.Fatalf("actions = %+v, %v; want camera.added from the log, then hive.data_moved", acts, err)
	}

	// The old disk is never changed, except the move's own note.
	after := tree(t, filepath.Dir(h.paths.Data))
	for f, sum := range before {
		if after[f] != sum && !strings.HasPrefix(f, "old/hive.db") {
			t.Errorf("old disk: %s changed or went", f)
		}
	}
	for _, dir := range []string{h.paths.Data, to} {
		if st, err := datamove.Read(dir); err != nil || st.State != datamove.Done || st.To != to {
			t.Fatalf("status in %s = %+v, %v; want done", dir, st, err)
		}
	}
}

// unrelatedWALWrite leaves a write in hive.db's write-ahead log and keeps
// the database open, as the running hive does: the move must checkpoint
// it into the copy.
func (h oldHive) unrelatedWALWrite(t *testing.T) {
	t.Helper()
	st, err := store.Open(context.Background(), h.paths.DB, storeKey)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	if err := st.Actions().Append(context.Background(), store.Action{Actor: "owner", Action: "camera.added", Target: "cam_gold"}); err != nil {
		t.Fatal(err)
	}
}

func TestWhatDoesNotFitStaysOnTheOldDiskAndLeavesTheIndex(t *testing.T) {
	h := newOldHive(t, 5)
	to := filepath.Join(t.TempDir(), "new")
	svc := &service{}
	o := h.options(to, svc)
	sizes := map[string]int64{}
	for _, n := range h.segments {
		st, err := os.Stat(filepath.Join(h.archive, "cam_gold", "main", n+archive.SealedExt))
		if err != nil {
			t.Fatal(err)
		}
		sizes[n] = st.Size()
	}
	// The disk is 80 % full when the newest two and half of the third are on it; only the archive takes room.
	room := sizes[h.segments[4]] + sizes[h.segments[3]] + sizes[h.segments[2]]/2
	total := room * 10 / 8
	o.Disk = func(string) (retention.Disk, error) {
		used := int64(0)
		for _, sum := range du(t, filepath.Join(to, "archive")) {
			used += sum
		}
		return retention.Disk{Total: total, Free: total - used}, nil
	}
	var out bytes.Buffer
	o.Progress = &out
	res, err := datamove.Move(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.CopiedSegments != 2 || res.SkippedSegments != 3 {
		t.Fatalf("result = %+v, want the newest 2 copied, 3 left behind", res)
	}
	third := time.Date(2026, 11, 20, 8, 20, 0, 0, time.UTC)
	if !res.SkippedNewest.Equal(third) {
		t.Fatalf("newest left behind = %v, want %v", res.SkippedNewest, third)
	}
	if !strings.Contains(out.String(), "did not fit: 3 segments") {
		t.Fatalf("output does not say what did not fit:\n%s", out.String())
	}
	st, err := store.Open(context.Background(), filepath.Join(to, "hive.db"), storeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.Segments().All(context.Background())
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows = %d, %v; want the 2 copied segments only", len(rows), err)
	}
	if n := len(du(t, filepath.Join(h.archive))); n != 5 {
		t.Fatalf("old archive has %d files, want all 5", n)
	}
}

func du(t *testing.T, dir string) map[string]int64 {
	t.Helper()
	out := map[string]int64{}
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			if st, err := d.Info(); err == nil {
				out[p] = st.Size()
			}
		}
		return nil
	})
	return out
}

func TestASecondRunResumesWithoutCopyingAgain(t *testing.T) {
	h := newOldHive(t, 3)
	to := filepath.Join(t.TempDir(), "new")
	svc := &service{failSwitch: true}
	if _, err := datamove.Move(context.Background(), h.options(to, svc)); err == nil {
		t.Fatal("the first run did not report the failed switch")
	}
	if st, _ := datamove.Read(h.paths.Data); st.State != datamove.Copying || svc.runsFrom != h.paths.Data {
		t.Fatalf("after a failed switch the old note is %s and the hive runs from %s; want copying, the old directory", st.State, svc.runsFrom)
	}
	seg := filepath.Join(to, "archive", "cam_gold", "main", h.segments[0]+archive.SealedExt)
	before, err := os.Stat(seg)
	if err != nil {
		t.Fatal(err)
	}
	// A copy cut off in the middle: a truncated .moving beside the final name.
	part := filepath.Join(to, "archive", "cam_gold", "main", h.segments[1]+archive.SealedExt)
	if err := os.Remove(part); err != nil {
		t.Fatal(err)
	}
	write(t, part+".moving", "trunc", 0o600)
	other := filepath.Join(t.TempDir(), "elsewhere")
	if _, err := datamove.Move(context.Background(), h.options(other, &service{})); err == nil {
		t.Fatal("a move to another directory started while one is unfinished")
	}
	res, err := datamove.Move(context.Background(), h.options(to, svc))
	if err != nil {
		t.Fatal(err)
	}
	if after, err := os.Stat(seg); err != nil || !os.SameFile(before, after) {
		t.Fatalf("a verified segment was copied again: %v", err)
	}
	if _, err := os.Stat(part + ".moving"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the truncated .moving is still there: %v", err)
	}
	if got, err := os.ReadFile(part); err != nil || len(got) < 1000 {
		t.Fatalf("the cut-off segment was not copied again: %d bytes, %v", len(got), err)
	}
	if res.CopiedSegments != 3 || svc.runsFrom != to {
		t.Fatalf("result %+v, runs from %s", res, svc.runsFrom)
	}
	again, err := datamove.Move(context.Background(), h.options(to, svc))
	if err != nil || !again.AlreadyDone {
		t.Fatalf("a run after the move = %+v, %v; want already done", again, err)
	}
	if _, err := datamove.Move(context.Background(), h.options(other, svc)); err == nil {
		t.Fatal("a second move from a directory already moved away was not refused")
	}
}

func TestAMoveIntoOrAroundItselfIsRefused(t *testing.T) {
	h := newOldHive(t, 1)
	for _, to := range []string{filepath.Join(h.paths.Data, "sub"), "relative/dir", filepath.Dir(h.paths.Data)} {
		if _, err := datamove.Move(context.Background(), h.options(to, &service{})); err == nil {
			t.Errorf("move to %s was not refused", to)
		}
	}
	if res, err := datamove.Move(context.Background(), h.options(h.paths.Data, &service{})); err != nil || !res.AlreadyDone {
		t.Errorf("a move to where the hive runs = %+v, %v; want already there", res, err)
	}
}

// #155 review B3: systemd splits or expands such paths, and the hive would
// start somewhere else with a new identity.
func TestATargetSystemdWouldMangleIsRefused(t *testing.T) {
	h := newOldHive(t, 1)
	for _, to := range []string{"/mnt/new disk/camhive", "/mnt/50%/camhive", `/mnt/a\b`, `/mnt/"x"`, "/mnt/$HOME", "/mnt/it's", "/mnt/tab\there",
		"/mnt/a;rm", "/mnt/a&b", "/mnt/a|b", "/mnt/(x)", "/mnt/<x>", "/mnt/*", "/mnt/a?", "/mnt/#x", "/mnt/`x`", "/mnt/a!b", "/mnt/~x", "/mnt/{a,b}", "/mnt/[a]"} {
		if _, err := datamove.Move(context.Background(), h.options(to, &service{})); err == nil || !strings.Contains(err.Error(), "must not contain") {
			t.Errorf("%q: %v, want refused for its characters", to, err)
		}
	}
	if why := datamove.SafePath("/mnt/newdisk/camhive-2"); why != "" {
		t.Errorf("a plain path is refused: %s", why)
	}
}

func TestTheDiskBeingReplacedAndFATAreRefused(t *testing.T) {
	h := newOldHive(t, 1)
	o := h.options(filepath.Join(t.TempDir(), "new"), &service{})
	o.DeviceOf = func(string) (uint64, error) { return 7, nil }
	if _, err := datamove.Move(context.Background(), o); err == nil || !strings.Contains(err.Error(), "same disk") {
		t.Errorf("same device: %v", err)
	}
	o = h.options(filepath.Join(t.TempDir(), "new"), &service{})
	o.FSType = func(string) (string, error) { return "exfat", nil }
	if _, err := datamove.Move(context.Background(), o); err == nil || !strings.Contains(err.Error(), "ext4") {
		t.Errorf("exfat: %v", err)
	}
}

// #155 review B1: archive_dir and archive-key may be links to another disk.
func TestLinkedArchiveAndKeyringAreCopiedFromWhereTheyAre(t *testing.T) {
	h := newOldHive(t, 3)
	elsewhere := filepath.Join(t.TempDir(), "usb")
	if err := os.MkdirAll(elsewhere, 0o750); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"archive", "archive-key"} {
		real := filepath.Join(elsewhere, d)
		if err := os.Rename(filepath.Join(h.paths.Data, d), real); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(real, filepath.Join(h.paths.Data, d)); err != nil {
			t.Fatal(err)
		}
	}
	to := filepath.Join(t.TempDir(), "new")
	res, err := datamove.Move(context.Background(), h.options(to, &service{}))
	if err != nil {
		t.Fatal(err)
	}
	if res.CopiedSegments != 3 {
		t.Fatalf("copied %d segments through the link, want 3", res.CopiedSegments)
	}
	if _, err := os.Stat(filepath.Join(to, "archive-key", "keyring.age")); err != nil {
		t.Fatalf("keyring behind a link not copied: %v", err)
	}
	if rows := rowsIn(t, to); rows != 3 {
		t.Fatalf("new index has %d rows, want 3", rows)
	}
}

func TestALinkInsideTheKeyringStopsTheMoveAndOneInTheArchiveIsReported(t *testing.T) {
	h := newOldHive(t, 2)
	if err := os.Symlink("/etc/hostname", filepath.Join(h.paths.Data, "archive-key", "extra")); err != nil {
		t.Fatal(err)
	}
	to := filepath.Join(t.TempDir(), "new")
	_, err := datamove.Move(context.Background(), h.options(to, &service{}))
	if err == nil || !strings.Contains(err.Error(), "symbolic link") {
		t.Fatalf("a link in archive-key: %v, want the move stopped", err)
	}
	if err := os.Remove(filepath.Join(h.paths.Data, "archive-key", "extra")); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("/etc/hostname", filepath.Join(h.archive, "cam_gold", "main", "odd.mp4")); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	o := h.options(to, &service{}) // the unfinished move goes on
	o.Progress = &out
	res, err := datamove.Move(context.Background(), o)
	if err != nil || len(res.Unusual) != 1 || !strings.Contains(out.String(), "not copied, not files: 1") {
		t.Fatalf("a link in the archive: %+v, %v\n%s", res, err, out.String())
	}
}

func TestAnArchiveWithoutFootageButAnIndexIsRefused(t *testing.T) {
	h := newOldHive(t, 2)
	if err := os.RemoveAll(h.archive); err != nil { // the disk is not mounted
		t.Fatal(err)
	}
	if _, err := datamove.Move(context.Background(), h.options(filepath.Join(t.TempDir(), "new"), &service{})); err == nil || !strings.Contains(err.Error(), "mounted") {
		t.Fatalf("empty archive with an index: %v", err)
	}
}

// #155 review B2: a bad sector in one segment must not stop the move.
func TestAnUnreadableSegmentIsLeftBehindAndReported(t *testing.T) {
	h := newOldHive(t, 3)
	bad := h.segments[1]
	restore := datamove.FailReading(func(p string) bool { return strings.Contains(p, bad) })
	defer restore()
	var out bytes.Buffer
	o := h.options(filepath.Join(t.TempDir(), "new"), &service{})
	o.Progress = &out
	res, err := datamove.Move(context.Background(), o)
	if err != nil {
		t.Fatal(err)
	}
	if res.CopiedSegments != 2 || res.UnreadableSegments != 1 || res.UnreadableBytes == 0 || !strings.Contains(out.String(), "could not be read from the old disk: 1 segments") {
		t.Fatalf("result %+v\n%s", res, out.String())
	}
	if rows := rowsIn(t, o.To); rows != 2 {
		t.Fatalf("new index has %d rows, want the 2 copied", rows)
	}
	if st, _ := datamove.Read(o.To); st.UnreadableSegments != 1 {
		t.Fatalf("note = %+v, want the unreadable one counted", st)
	}
	// The identity must arrive: a read error there stops the move.
	restore()
	defer datamove.FailReading(func(p string) bool { return strings.HasSuffix(p, "hive.key") })()
	if _, err := datamove.Move(context.Background(), h.options(filepath.Join(t.TempDir(), "new2"), &service{})); err == nil {
		t.Fatal("an unreadable hive.key did not stop the move")
	}
}

// #155 review B4: a start that fails after the service was pointed at the
// new directory is finished by a rerun from there, or cancelled from there.
func TestAStuckSwitchIsFinishedOrCancelledFromTheNewSide(t *testing.T) {
	for _, cancel := range []bool{false, true} {
		h := newOldHive(t, 1)
		to := filepath.Join(t.TempDir(), "new")
		svc := &service{failStuck: true}
		if _, err := datamove.Move(context.Background(), h.options(to, svc)); err == nil {
			t.Fatal("the stuck switch was not reported")
		}
		if st, _ := datamove.Read(to); st.State != datamove.Switching || svc.runsFrom != to {
			t.Fatalf("new note %s, runs from %s; want switching at the new directory", st.State, svc.runsFrom)
		}
		if st, _ := datamove.Read(h.paths.Data); st.State != datamove.Copying {
			t.Fatalf("old note %s, want copying (not done before the hive runs)", st.State)
		}
		// The command now finds the hive at the new directory (the service says so).
		fromNew := h.options(to, svc)
		fromNew.From = hiveconfig.Paths{}.WithDataDir(to)
		if cancel {
			if err := datamove.Cancel(to, svc, time.Now()); err != nil {
				t.Fatal(err)
			}
			if svc.runsFrom != h.paths.Data || datamove.Moving(h.paths.Data) || svc.switches[len(svc.switches)-1] != "" {
				t.Fatalf("after cancel the hive runs from %s (last switch %q), moving %v; want the old directory as installed, recording", svc.runsFrom, svc.switches[len(svc.switches)-1], datamove.Moving(h.paths.Data))
			}
			continue
		}
		if _, err := datamove.Move(context.Background(), fromNew); err != nil {
			t.Fatal(err)
		}
		for _, dir := range []string{h.paths.Data, to} {
			if st, _ := datamove.Read(dir); st.State != datamove.Done {
				t.Fatalf("note in %s = %s, want done", dir, st.State)
			}
		}
	}
}

// #155 review: a write-ahead log an earlier cut-off run left beside the
// copy must not be replayed into the new database.
func TestAStaleLogBesideTheCopyIsRemoved(t *testing.T) {
	h := newOldHive(t, 1)
	to := filepath.Join(t.TempDir(), "new")
	write(t, filepath.Join(to, "hive.db-wal"), "left by a cut-off run", 0o600)
	write(t, filepath.Join(to, "hive.db-shm"), "left by a cut-off run", 0o600)
	if _, err := datamove.Move(context.Background(), h.options(to, &service{})); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{"hive.db-wal", "hive.db-shm"} {
		if b, err := os.ReadFile(filepath.Join(to, f)); err == nil && string(b) == "left by a cut-off run" {
			t.Fatalf("%s survived the move", f)
		}
	}
}

// #155 review: the copied footage leaves min_free_gb free, or the new
// hive's sweeper deletes it at once.
func TestTheFreeSpaceFloorLimitsTheFootage(t *testing.T) {
	h := newOldHive(t, 3)
	o := h.options(filepath.Join(t.TempDir(), "new"), &service{})
	o.Disk = func(string) (retention.Disk, error) { return retention.Disk{Total: 10 << 30, Free: 10 << 30}, nil }
	o.MinFreeBytes = 10 << 30 // the floor is the whole disk
	res, err := datamove.Move(context.Background(), o)
	if err != nil || res.CopiedSegments != 0 || res.SkippedSegments != 3 {
		t.Fatalf("result %+v, %v; want nothing copied into the floor", res, err)
	}
}

func rowsIn(t *testing.T, dir string) int {
	t.Helper()
	st, err := store.Open(context.Background(), filepath.Join(dir, "hive.db"), storeKey)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = st.Close() }()
	rows, err := st.Segments().All(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return len(rows)
}

func TestCancelLetsTheHiveRecordAgain(t *testing.T) {
	h := newOldHive(t, 1)
	svc := &service{failSwitch: true}
	to := filepath.Join(t.TempDir(), "new")
	_, _ = datamove.Move(context.Background(), h.options(to, svc))
	if !datamove.Moving(h.paths.Data) {
		t.Fatal("not moving after a failed switch")
	}
	restarts := svc.restarts
	if err := datamove.Cancel(h.paths.Data, svc, time.Now()); err != nil {
		t.Fatal(err)
	}
	if datamove.Moving(h.paths.Data) || svc.restarts != restarts+1 {
		t.Fatal("still moving after cancel, or the hive was not restarted to record")
	}
	if st, _ := datamove.Read(h.paths.Data); st.State != datamove.Failed || st.Error == "" {
		t.Fatalf("status = %+v, want failed with a reason", st)
	}
	if st, _ := datamove.Read(to); st.State != datamove.Failed {
		t.Fatalf("the new directory's note = %s, want failed too", st.State)
	}
}
