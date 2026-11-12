// Package diag builds the diagnostic bundle (ADR-0011): a zip the owner can
// inspect and choose to send to support. It replaces the legacy "remote
// access" backdoor with something the owner triggers, sees and controls.
// Nothing is uploaded here. The identity key and the database never go in;
// password-like config values are redacted.
package diag

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/camhive/hive/internal/hiveconfig"
	"github.com/camhive/hive/internal/store"
)

// Inputs are what the bundle is built from. Store may be nil.
type Inputs struct {
	Paths         hiveconfig.Paths
	Store         *store.Store
	Version       string
	EngineVersion func(ctx context.Context) string // optional
	Free          func(dir string) (int64, error)  // optional; archive volume free bytes
}

// Build writes camhive-diag-<timestamp>.zip into the data dir and returns
// its path and the entry names.
func Build(ctx context.Context, in Inputs) (string, []string, error) {
	if err := os.MkdirAll(in.Paths.Data, 0o750); err != nil {
		return "", nil, err
	}
	out := filepath.Join(in.Paths.Data, "camhive-diag-"+time.Now().UTC().Format("20060102-150405")+".zip")
	f, err := os.Create(out) //nolint:gosec // path under our data dir
	if err != nil {
		return "", nil, err
	}
	b := &bundle{zw: zip.NewWriter(f)}
	b.addText("README.txt", readme)
	b.addJSON("versions.json", versions(ctx, in))
	b.addConfig(in.Paths.Config)
	b.addDisk(in)
	b.addStore(ctx, in.Store)
	b.addLogs("logs", in.Paths.Logs)
	b.addLogs("engine", filepath.Join(in.Paths.Data, "engine"))
	if err := b.zw.Close(); err != nil {
		_ = f.Close()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		return "", nil, err
	}
	sort.Strings(b.entries)
	if b.err != nil {
		return out, b.entries, fmt.Errorf("diag: bundle written with problems: %w", b.err)
	}
	return out, b.entries, nil
}

const readme = `Camhive diagnostic bundle

Collected on the owner's request. Contents: versions, redacted configuration,
cameras (no passwords), activity and deletion logs, disk usage, hive and
video engine logs. Not included: the hive identity key, the database, any
video. Review before sending to support.
`

type bundle struct {
	zw      *zip.Writer
	entries []string
	err     error
}

func (b *bundle) add(name string, r io.Reader) {
	w, err := b.zw.Create(name)
	if err != nil {
		b.err = err
		return
	}
	if _, err := io.Copy(w, r); err != nil {
		b.err = err
		return
	}
	b.entries = append(b.entries, name)
}

func (b *bundle) addText(name, body string) { b.add(name, strings.NewReader(body)) }

func (b *bundle) addJSON(name string, v any) {
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		b.err = err
		return
	}
	b.add(name, strings.NewReader(string(raw)))
}

func versions(ctx context.Context, in Inputs) map[string]any {
	v := map[string]any{"hive": in.Version, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "collected_at": time.Now().UTC()}
	if in.EngineVersion != nil {
		v["engine"] = in.EngineVersion(ctx)
	}
	host, _ := os.Hostname()
	v["hostname"] = host
	return v
}

// secretKeyRE matches YAML keys whose values must not leave the machine.
var secretKeyRE = regexp.MustCompile(`(?im)^(\s*[\w.-]*(password|secret|token|key|passwd)[\w.-]*\s*:\s*)(.+)$`)

// addConfig adds hive.yaml with password-like values replaced by ***.
// cloud_public_keys are public by definition but are redacted too: simpler
// rule, nothing to argue about.
func (b *bundle) addConfig(path string) {
	raw, err := os.ReadFile(path) //nolint:gosec // our config path
	if err != nil {
		return
	}
	redacted := secretKeyRE.ReplaceAllString(string(raw), "${1}***")
	b.addText("config.yaml", redacted)
}

func (b *bundle) addDisk(in Inputs) {
	d := map[string]any{"archive_dir": in.Paths.Archive, "data_dir": in.Paths.Data}
	if in.Free != nil {
		if free, err := in.Free(in.Paths.Archive); err == nil {
			d["archive_free_bytes"] = free
		} else {
			d["archive_free_error"] = err.Error()
		}
	}
	var files int
	var bytes int64
	_ = filepath.WalkDir(in.Paths.Archive, func(_ string, e os.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if st, err := e.Info(); err == nil {
				files++
				bytes += st.Size()
			}
		}
		return nil
	})
	d["archive_files"], d["archive_bytes"] = files, bytes
	b.addJSON("disk.json", d)
}

type cameraEntry struct {
	ID       string            `json:"id"`
	Name     string            `json:"name"`
	Group    string            `json:"group"`
	Kind     string            `json:"kind"`
	Address  string            `json:"address"`
	Username string            `json:"username"`
	Streams  map[string]string `json:"streams"`
	Enabled  bool              `json:"enabled"`
}

func (b *bundle) addStore(ctx context.Context, st *store.Store) {
	if st == nil {
		return
	}
	cams, err := st.Cameras().List(ctx)
	if err != nil {
		b.err = err
		return
	}
	entries := make([]cameraEntry, 0, len(cams))
	for _, c := range cams {
		e := cameraEntry{ID: string(c.ID), Name: c.Name, Group: c.Group, Kind: string(c.Source.Kind), Address: c.Source.Address, Username: c.Credentials.Username, Streams: map[string]string{}, Enabled: c.Enabled}
		for k, s := range c.Streams {
			e.Streams[string(k)] = s.URL
		}
		entries = append(entries, e)
	}
	b.addJSON("cameras.json", entries)
	if actions, err := st.Actions().Recent(ctx, 500); err == nil {
		b.addJSON("actions.json", actions)
	}
	if dels, err := st.Segments().Deletions(ctx, 500); err == nil {
		b.addJSON("deletions.json", dels)
	}
	if usage, err := st.Segments().Usage(ctx); err == nil {
		b.addJSON("archive-usage.json", usage)
	}
}

// addLogs adds every *.log file in dir under prefix/.
func (b *bundle) addLogs(prefix, dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.IsDir() || !strings.Contains(e.Name(), ".log") {
			continue
		}
		f, err := os.Open(filepath.Join(dir, e.Name())) //nolint:gosec // listed from our own dir
		if err != nil {
			continue
		}
		b.add(prefix+"/"+e.Name(), f)
		_ = f.Close()
	}
}
