package camsim

import (
	"encoding/xml"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
)

// PTZCommand is one recorded PTZ request, for assertions in tests.
type PTZCommand struct {
	Profile string
	Action  string // ContinuousMove, Stop, GotoPreset
	Pan     float64
	Tilt    float64
	Zoom    float64
	Preset  string
}

// ptzLog records PTZ commands received by the mock.
type ptzLog struct {
	mu   sync.Mutex
	cmds []PTZCommand
}

func newPTZLog() *ptzLog { return &ptzLog{} }

func (l *ptzLog) add(c PTZCommand) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.cmds = append(l.cmds, c)
}

// Commands returns a copy of everything recorded so far.
func (l *ptzLog) Commands() []PTZCommand {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]PTZCommand(nil), l.cmds...)
}

var presetTokens = []string{"preset_1", "preset_2", "preset_3"}

// handlePTZ serves the ONVIF PTZ service (ver20).
func handlePTZ(cfg *Config, log *ptzLog, w http.ResponseWriter, req soapRequest) {
	token := textOf(req.Body, "ProfileToken")
	if _, _, ok := lookupProfile(cfg, token); !ok {
		writeFault(w, "NoProfile", "profile token does not exist")
		return
	}
	switch req.Action {
	case "ContinuousMove":
		pan, tilt := attrFloats(req.Body, "PanTilt")
		zoom, _ := attrFloats(req.Body, "Zoom")
		log.add(PTZCommand{Profile: token, Action: "ContinuousMove", Pan: pan, Tilt: tilt, Zoom: zoom})
		writeSOAP(w, http.StatusOK, `<tptz:ContinuousMoveResponse xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`)
	case "Stop":
		log.add(PTZCommand{Profile: token, Action: "Stop"})
		writeSOAP(w, http.StatusOK, `<tptz:StopResponse xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`)
	case "GotoPreset":
		log.add(PTZCommand{Profile: token, Action: "GotoPreset", Preset: textOf(req.Body, "PresetToken")})
		writeSOAP(w, http.StatusOK, `<tptz:GotoPresetResponse xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl"/>`)
	case "GetPresets":
		var b strings.Builder
		b.WriteString(`<tptz:GetPresetsResponse xmlns:tptz="http://www.onvif.org/ver20/ptz/wsdl">`)
		for i, p := range presetTokens {
			fmt.Fprintf(&b, `<tptz:Preset token="%s"><tt:Name>Preset %d</tt:Name></tptz:Preset>`, p, i+1)
		}
		b.WriteString(`</tptz:GetPresetsResponse>`)
		writeSOAP(w, http.StatusOK, b.String())
	default:
		writeFault(w, "ActionNotSupported", req.Action)
	}
}

// attrFloats returns the x and y attributes of the first element named local.
func attrFloats(body, local string) (x, y float64) {
	dec := xml.NewDecoder(strings.NewReader(body))
	for {
		t, err := dec.Token()
		if err != nil {
			return 0, 0
		}
		se, ok := t.(xml.StartElement)
		if !ok || se.Name.Local != local {
			continue
		}
		for _, a := range se.Attr {
			v, _ := strconv.ParseFloat(a.Value, 64)
			switch a.Name.Local {
			case "x":
				x = v
			case "y":
				y = v
			}
		}
		return x, y
	}
}
