package hiveagent

import (
	"context"
	"encoding/json"

	"github.com/camhive/hive/internal/protocol"
	"github.com/camhive/hive/internal/video"
)

// StateICE is the cloud_state key holding the last pushed ICE servers.
const StateICE = "ice"

// iceDocument is the ice.set payload: STUN/TURN servers with per-hive TURN
// credentials minted by the cloud (ADR-0010 path 3, #30).
type iceDocument struct {
	Servers []struct {
		URLs       []string `json:"urls"`
		Username   string   `json:"username"`
		Credential string   `json:"credential"`
	} `json:"servers"`
}

func (d iceDocument) engineServers() []video.ICEServer {
	var out []video.ICEServer
	for _, s := range d.Servers {
		for _, u := range s.URLs {
			out = append(out, video.ICEServer{URL: u, Username: s.Username, Password: s.Credential})
		}
	}
	return out
}

// iceSet stores the servers and applies them to the running engine.
func (a *Agent) iceSet(ctx context.Context, args json.RawMessage) protocol.Result {
	var doc iceDocument
	if r := decodeArgs(args, &doc); r != nil {
		return *r
	}
	if r := a.putState(ctx, StateICE, args); r != nil {
		return *r
	}
	applied, err := a.applyICE(ctx, doc)
	if err != nil {
		return protocol.Fail(protocol.ErrCodeInternal, "engine refused ICE servers: "+err.Error())
	}
	a.record(ctx, "ice.set", "", map[string]any{"servers": len(doc.Servers), "applied": applied})
	return protocol.Succeed(map[string]any{"applied": applied})
}

// applyICE hands the servers to the engine when it supports runtime
// updates; false when there is no engine yet (applied at SetEngine).
func (a *Agent) applyICE(ctx context.Context, doc iceDocument) (bool, error) {
	a.mu.Lock()
	eng := a.d.Engine
	a.mu.Unlock()
	up, ok := eng.(video.ICEUpdater)
	if eng == nil || !ok {
		return false, nil
	}
	return true, up.SetICEServers(ctx, doc.engineServers())
}

// applyStoredICE replays the last pushed servers (engine start, restart).
func (a *Agent) applyStoredICE(ctx context.Context) {
	if a.d.Store == nil {
		return
	}
	raw, _, err := a.d.Store.CloudState().Get(ctx, StateICE)
	if err != nil {
		return
	}
	var doc iceDocument
	if json.Unmarshal(raw, &doc) != nil {
		return
	}
	if _, err := a.applyICE(ctx, doc); err != nil {
		a.log.Warn("apply stored ICE servers", "err", err)
	}
}
