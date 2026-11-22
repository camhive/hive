package hiveapp

import (
	"net/http"
	"path/filepath"

	"github.com/camhive/hive/internal/auth"
	"github.com/camhive/hive/internal/datamove"
	"github.com/camhive/hive/internal/hiveconfig"
)

// MoveInfo is what GET /v0/hive/move reads (#155): the hive's paths (the
// data directory holds the move.json hive move-data writes), its archive
// directory, and whether the command can move this hive (a systemd
// service on Linux).
type MoveInfo struct {
	Paths      hiveconfig.Paths
	ArchiveDir string
	Supported  bool
	// Moving: the hive started while hive move-data copies (its note says
	// copying), so changes answer 409 moving: they would stay on the old
	// disk. A cancelled or finished move restarts the hive.
	Moving bool
}

// duringMove are the writes that go on while moving: they change only
// what the hive holds in memory.
var duringMove = map[string]bool{
	"POST /v0/local-login/sessions":        true, // signing in; changing the login is refused
	"POST /v0/archive/encryption/unlock":   true,
	"POST /v0/archive/encryption/lock":     true,
	"POST /v0/archive/encryption/presence": true,
}

// whileMoving answers 409 moving to every change but duringMove while
// the hive moves; reads go on.
func whileMoving(m *MoveInfo, next http.Handler) http.Handler {
	if m == nil || !m.Moving {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if !duringMove[r.Method+" "+r.URL.Path] {
				writeError(w, http.StatusConflict, "moving", "the hive is moving to a new disk: changes wait until the move ends")
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// moveFile is one file to save by hand before a move, where it really is.
type moveFile struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// moveView is GET /v0/hive/move: the note with where the hive runs.
type moveView struct {
	datamove.Status
	Supported  bool       `json:"supported"`
	DataDir    string     `json:"data_dir,omitempty"`
	ArchiveDir string     `json:"archive_dir,omitempty"`
	Files      []moveFile `json:"files"`
}

// filesToSave are the five files to copy first, hardest to replace first;
// hive.yaml is in /etc/camhive on a default Linux install.
func filesToSave(p hiveconfig.Paths) []moveFile {
	return []moveFile{{"hive.key", p.Key}, {"cloud.json", p.Link}, {"hive.yaml", p.Config},
		{"archive-key", filepath.Join(p.Data, "archive-key") + string(filepath.Separator)}, {"hive.db", p.DB}}
}

func (a *api) getMove(w http.ResponseWriter, r *http.Request) {
	if claimsFrom(r).Role != auth.RoleOwner {
		writeError(w, http.StatusForbidden, "forbidden", "only the owner follows a move of the hive's data")
		return
	}
	view := moveView{Status: datamove.Status{State: datamove.None}, Files: []moveFile{}}
	if m := a.d.Move; m != nil {
		st, err := datamove.Read(m.Paths.Data)
		if err != nil {
			a.internal(w, err)
			return
		}
		view = moveView{Status: st, Supported: m.Supported, DataDir: m.Paths.Data, ArchiveDir: m.ArchiveDir, Files: filesToSave(m.Paths)}
	}
	writeJSON(w, http.StatusOK, view)
}
