// Package capability is the one list of what this Hive build can do
// (ADR-0022). The app asks for it (GET /v0/hive, the heartbeat) instead of
// comparing versions. Names are never removed or renamed; an incompatible
// change to a feature gets a new name (archive.delete.v2).
package capability

// Capability is one named feature of the Hive's software and the endpoints
// of api/hive-openapi.yaml that implement it ("METHOD /path").
type Capability struct {
	Name      string
	Endpoints []string
}

// The list, sorted by name.
//
// camera.identity, camera.retention, camera.sensitivity and motion are field-level features (ADR-0022 §1): their
// endpoints are older than they are, and the capability says the fields
// are there (a camera's identity and network; motion events with an end).
// The endpoint test only proves those endpoints exist.
var all = []Capability{
	{Name: "archive.autolock", Endpoints: []string{"PATCH /v0/archive/encryption/autolock", "POST /v0/archive/encryption/presence"}}, // #147, ADR-0019 addendum
	{Name: "archive.delete", Endpoints: []string{"POST /v0/archive/deletions"}},                                                      // ADR-0016
	{Name: "archive.encryption", Endpoints: []string{ // ADR-0019
		"GET /v0/archive/encryption", "POST /v0/archive/encryption", "POST /v0/archive/encryption/unlock",
		"POST /v0/archive/encryption/lock", "POST /v0/archive/encryption/passphrase", "GET /v0/archive/encryption/key-file",
	}},
	{Name: "camera.care", Endpoints: []string{"GET /v0/cameras/{id}/care"}},                                                 // #163
	{Name: "camera.identity", Endpoints: []string{"GET /v0/cameras/{id}", "PATCH /v0/cameras/{id}"}},                        // ADR-0017
	{Name: "camera.retention", Endpoints: []string{"PATCH /v0/cameras/{id}"}},                                               // #61, #124
	{Name: "camera.sensitivity", Endpoints: []string{"PATCH /v0/cameras/{id}"}},                                             // #157
	{Name: "camera.sub_stream", Endpoints: []string{"PATCH /v0/cameras/{id}"}},                                              // #63
	{Name: "disk.health", Endpoints: []string{"GET /v0/storage", "GET /v0/hive/move"}},                                      // #155, ADR-0024 §6
	{Name: "local_login", Endpoints: []string{"PUT /v0/local-login", "POST /v0/local-login/sessions"}},                      // #57
	{Name: "motion", Endpoints: []string{"GET /v0/cameras/{id}/events"}},                                                    // ADR-0020
	{Name: "storage", Endpoints: []string{"GET /v0/storage", "PATCH /v0/hive/storage"}},                                     // #150, ADR-0024
	{Name: "updates", Endpoints: []string{"GET /v0/hive/updates", "PUT /v0/hive/updates", "POST /v0/hive/updates/install"}}, // #82
}

// All returns the list with its endpoints.
func All() []Capability { return all }

// Names returns the capability names this build serves, sorted.
func Names() []string {
	out := make([]string, len(all))
	for i, c := range all {
		out[i] = c.Name
	}
	return out
}
