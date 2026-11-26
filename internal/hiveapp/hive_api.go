package hiveapp

import (
	"net/http"

	"github.com/camhive/hive/internal/archivekey"
	"github.com/camhive/hive/internal/capability"
)

// hiveInfo is GET /v0/hive: what this hive is and can do (ADR-0022).
type hiveInfo struct {
	Version      string   `json:"version"`
	Capabilities []string `json:"capabilities"`
	// ArchiveEncryption tells every member whether the archive is locked
	// (ADR-0019), as GET /v0/archive/encryption does.
	ArchiveEncryption archivekey.State `json:"archive_encryption"`
	// NotRecording tells every member the hive does not record (#172).
	NotRecording *NotRecording `json:"not_recording,omitempty"`
}

func (a *api) getHive(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, hiveInfo{Version: a.d.Version, Capabilities: capability.Names(), ArchiveEncryption: a.vaultState(),
		NotRecording: a.notRecording(false)})
}
