package update

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
)

// BundleFiles are the executables of a Hive bundle (ADR-0018 §1).
var BundleFiles = func() []string {
	ext := ""
	if runtime.GOOS == "windows" {
		ext = ".exe"
	}
	return []string{"hive" + ext, "mediamtx" + ext, "hive-updater" + ext}
}()

// ErrNoVersion means there is no version to run or to go back to.
var ErrNoVersion = errors.New("update: no version")

// ErrUnsafeDir means the updater's directory could be written by others.
var ErrUnsafeDir = errors.New("update: directory not private to the updater")

// ErrChanged means a stored bundle no longer matches what was installed.
var ErrChanged = errors.New("update: stored bundle changed since it was installed")

// manifest records each bundle file's SHA-256 when a version is added.
const manifest = "MANIFEST.json"

// Versions is the updater's store under the data directory:
// versions/<version>/ holds a bundle, `current` names the one to run and
// state.json remembers the previous one and the versions that failed.
// Every change is a rename, so a crash leaves the old or the new state.
type Versions struct{ dir string }

type versionsState struct {
	Previous string   `json:"previous,omitempty"`
	Failed   []string `json:"failed,omitempty"`
}

// OpenVersions uses dir (the Hive's data directory) for the store.
func OpenVersions(dir string) *Versions { return &Versions{dir: dir} }

func (s *Versions) root() string          { return filepath.Join(s.dir, "versions") }
func (s *Versions) path(v Version) string { return filepath.Join(s.root(), v.String()) }

// File is the path of one bundle file of version v.
func (s *Versions) File(v Version, name string) string { return filepath.Join(s.path(v), name) }

// Has reports whether a complete bundle of v is stored.
func (s *Versions) Has(v Version) bool {
	for _, n := range append([]string{manifest}, BundleFiles...) {
		if _, err := os.Stat(s.File(v, n)); err != nil {
			return false
		}
	}
	return true
}

// Add copies a bundle from src into the store with a manifest of its
// hashes. A directory already there for v is replaced, never trusted:
// whatever put it there was not this updater installing a verified bundle.
func (s *Versions) Add(v Version, src string) error {
	tmp := s.path(v) + ".partial"
	if err := os.RemoveAll(tmp); err != nil {
		return err
	}
	if err := os.MkdirAll(tmp, 0o750); err != nil {
		return err
	}
	sums := map[string]string{}
	for _, n := range BundleFiles {
		if err := copyFile(filepath.Join(src, n), filepath.Join(tmp, n)); err != nil {
			_ = os.RemoveAll(tmp)
			return fmt.Errorf("update: add %s: %w", v, err)
		}
		sum, err := fileSHA256(filepath.Join(tmp, n))
		if err != nil {
			_ = os.RemoveAll(tmp)
			return err
		}
		sums[n] = sum
	}
	raw, _ := json.Marshal(sums)
	if err := os.WriteFile(filepath.Join(tmp, manifest), raw, 0o600); err != nil {
		_ = os.RemoveAll(tmp)
		return err
	}
	if err := os.RemoveAll(s.path(v)); err != nil {
		return err
	}
	return os.Rename(tmp, s.path(v))
}

// Verify checks a stored bundle against the manifest written by Add.
func (s *Versions) Verify(v Version) error {
	raw, err := os.ReadFile(filepath.Join(s.path(v), manifest))
	if err != nil {
		return fmt.Errorf("%w: %s: %w", ErrChanged, v, err)
	}
	var sums map[string]string
	if err := json.Unmarshal(raw, &sums); err != nil {
		return fmt.Errorf("%w: %s manifest", ErrChanged, v)
	}
	for _, n := range BundleFiles {
		got, err := fileSHA256(s.File(v, n))
		if err != nil || got != sums[n] {
			return fmt.Errorf("%w: %s %s", ErrChanged, v, n)
		}
	}
	return nil
}

func fileSHA256(p string) (string, error) {
	f, err := os.Open(p) //nolint:gosec // a file in the updater's private store
	if err != nil {
		return "", err
	}
	defer func() { _ = f.Close() }()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

func copyFile(from, to string) error {
	in, err := os.Open(from) //nolint:gosec // a bundle directory chosen by the updater
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755) //nolint:gosec // executables
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		return err
	}
	return out.Close()
}

// Current is the version to run.
func (s *Versions) Current() (Version, error) {
	raw, err := os.ReadFile(filepath.Join(s.dir, "current"))
	if errors.Is(err, os.ErrNotExist) {
		return Version{}, ErrNoVersion
	}
	if err != nil {
		return Version{}, err
	}
	return ParseVersion(strings.TrimSpace(string(raw)))
}

// Seed adds the bundle a package installed and selects it unless a newer
// version is already selected (ADR-0018 §5). The package's copy is copied
// again on every start, so the stored one always matches the package.
func (s *Versions) Seed(v Version, src string) error {
	if err := s.Add(v, src); err != nil {
		return err
	}
	cur, err := s.Current()
	if err == nil && cur.Compare(v) >= 0 {
		return nil
	}
	if err != nil && !errors.Is(err, ErrNoVersion) {
		return err
	}
	return s.Switch(v)
}

// Switch selects v and remembers the version it replaces.
func (s *Versions) Switch(v Version) error {
	if !s.Has(v) {
		return fmt.Errorf("update: %s is not stored", v)
	}
	st := s.state()
	if cur, err := s.Current(); err == nil && cur.Compare(v) != 0 {
		st.Previous = cur.String()
	}
	if err := writeAtomic(filepath.Join(s.dir, "current"), []byte(v.String()+"\n")); err != nil {
		return err
	}
	return s.save(st)
}

// Rollback marks the current version failed and goes back to the previous one.
func (s *Versions) Rollback() (Version, error) {
	st := s.state()
	prev, err := ParseVersion(st.Previous)
	if st.Previous == "" || err != nil || !s.Has(prev) {
		return Version{}, ErrNoVersion
	}
	if cur, err := s.Current(); err == nil && !slices.Contains(st.Failed, cur.String()) {
		st.Failed = append(st.Failed, cur.String())
	}
	if err := writeAtomic(filepath.Join(s.dir, "current"), []byte(prev.String()+"\n")); err != nil {
		return Version{}, err
	}
	st.Previous = ""
	return prev, s.save(st)
}

// Failed lists the versions that rolled back here.
func (s *Versions) Failed() []Version {
	var out []Version
	for _, f := range s.state().Failed {
		if v, err := ParseVersion(f); err == nil {
			out = append(out, v)
		}
	}
	return out
}

// Prune removes every stored version except the current and the previous
// one, and leftovers of unfinished copies.
func (s *Versions) Prune() error {
	keep := map[string]bool{s.state().Previous: true}
	if cur, err := s.Current(); err == nil {
		keep[cur.String()] = true
	}
	entries, err := os.ReadDir(s.root())
	if err != nil {
		return err
	}
	for _, e := range entries {
		if !keep[e.Name()] {
			if err := os.RemoveAll(filepath.Join(s.root(), e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (s *Versions) state() versionsState {
	var st versionsState
	if raw, err := os.ReadFile(filepath.Join(s.dir, "update-state.json")); err == nil {
		_ = json.Unmarshal(raw, &st)
	}
	return st
}

func (s *Versions) save(st versionsState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return writeAtomic(filepath.Join(s.dir, "update-state.json"), raw)
}

// writeAtomic replaces a file by renaming a complete temporary copy over it.
func writeAtomic(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
