package hiveconfig

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"gopkg.in/yaml.v3"
)

// ErrUpdateSettings means SaveUpdateSettings refused the settings as hive.yaml
// would; ErrUpdateWindow narrows it to the window.
var ErrUpdateSettings = errors.New("hiveconfig: invalid update settings")

// ErrUpdateWindow means update_window is not HH:MM-HH:MM with two different times.
var ErrUpdateWindow = errors.New("hiveconfig: update_window must look like 02:00-05:00")

// UpdateSettings are the owner's update choices (ADR-0018 §6) that the app
// may change (#82); hive-updater reads them from hive.yaml at every check.
type UpdateSettings struct {
	Updates       string `json:"updates"`        // automatic | manual
	UpdateWindow  string `json:"update_window"`  // HH:MM-HH:MM, local time
	UpdateChannel string `json:"update_channel"` // stable | beta
}

// UpdateSettings returns the update choices of this configuration.
func (c Config) UpdateSettings() UpdateSettings {
	return UpdateSettings{Updates: c.Updates, UpdateWindow: c.UpdateWindow, UpdateChannel: c.UpdateChannel}
}

// editMu makes the hive's own edits of hive.yaml (update and storage
// settings) one at a time, so one does not undo the other. It cannot
// cover another process: an owner who saves hive.yaml from an editor that
// read it before the app's change writes the old value back (the running
// hive keeps the app's value until it restarts), and an app change
// between an editor's read and write is lost the same way.
var editMu sync.Mutex

// SaveUpdateSettings validates s like hive.yaml and writes it into
// hive.yaml in place: comments and every other setting stay. The file is
// replaced atomically, so the updater never reads half of it.
func SaveUpdateSettings(p Paths, s UpdateSettings) error {
	editMu.Lock()
	defer editMu.Unlock()
	cfg, _, err := Load(p)
	if err != nil {
		return err
	}
	cfg.Updates, cfg.UpdateWindow, cfg.UpdateChannel = s.Updates, s.UpdateWindow, s.UpdateChannel
	if err := cfg.validateUpdates(); err != nil {
		return fmt.Errorf("%w: %w", ErrUpdateSettings, err)
	}
	raw, err := os.ReadFile(p.Config) //nolint:gosec // path from our own Paths
	if err != nil {
		return fmt.Errorf("hiveconfig: read %s: %w", p.Config, err)
	}
	out, err := setKeys(raw, []entry{{"updates", s.Updates, "!!str"}, {"update_window", s.UpdateWindow, "!!str"}, {"update_channel", s.UpdateChannel, "!!str"}})
	if err != nil {
		return fmt.Errorf("hiveconfig: edit %s: %w", p.Config, err)
	}
	return replaceFile(p.Config, out)
}

// entry is one top-level scalar of hive.yaml with its YAML tag (!!str, !!int).
type entry struct{ key, value, tag string }

// setKeys sets top-level scalar keys of a YAML document, adding missing ones.
func setKeys(raw []byte, kv []entry) ([]byte, error) {
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, err
	}
	if doc.Kind == 0 { // an empty file
		doc = yaml.Node{Kind: yaml.DocumentNode, Content: []*yaml.Node{{Kind: yaml.MappingNode}}}
	}
	m := doc.Content[0]
	if m.Kind != yaml.MappingNode {
		return nil, errors.New("not a mapping")
	}
	for _, e := range kv {
		setKey(m, e)
	}
	var out bytes.Buffer
	enc := yaml.NewEncoder(&out)
	enc.SetIndent(2) // as people write it by hand
	if err := enc.Encode(&doc); err != nil {
		return nil, err
	}
	return out.Bytes(), enc.Close()
}

func setKey(m *yaml.Node, e entry) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == e.key {
			m.Content[i+1].Kind, m.Content[i+1].Tag, m.Content[i+1].Value, m.Content[i+1].Style = yaml.ScalarNode, e.tag, e.value, 0
			return
		}
	}
	m.Content = append(m.Content, &yaml.Node{Kind: yaml.ScalarNode, Value: e.key}, &yaml.Node{Kind: yaml.ScalarNode, Tag: e.tag, Value: e.value})
}

// replaceFile writes data next to path and renames it over path, keeping
// path's permissions. The data and then the rename reach the disk before it
// returns: a power cut on a Pi must leave the old file or the new one,
// never an empty hive.yaml that loses every other setting.
func replaceFile(path string, data []byte) error {
	mode := os.FileMode(0o600)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".hive.yaml-*")
	if err != nil {
		return fmt.Errorf("hiveconfig: write %s: %w", path, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	_, werr := tmp.Write(data)
	err = errors.Join(werr, tmp.Chmod(mode), tmp.Sync(), tmp.Close())
	if err == nil {
		err = os.Rename(tmp.Name(), path)
	}
	if err == nil {
		err = syncDir(filepath.Dir(path))
	}
	if err != nil {
		return fmt.Errorf("hiveconfig: write %s: %w", path, err)
	}
	return nil
}

// syncDir makes a rename in dir durable. Windows cannot open a directory
// for syncing; NTFS journals the rename itself.
func syncDir(dir string) error {
	if runtimeGOOS == "windows" {
		return nil
	}
	d, err := os.Open(dir) //nolint:gosec // the config file's own directory
	if err != nil {
		return err
	}
	return errors.Join(d.Sync(), d.Close())
}
