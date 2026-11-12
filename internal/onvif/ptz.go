package onvif

import (
	"context"
	"errors"
	"fmt"
)

// ErrNoPTZ is returned when the camera does not offer a PTZ service.
var ErrNoPTZ = errors.New("onvif: camera has no PTZ service")

// Preset is a stored PTZ position.
type Preset struct {
	Token string
	Name  string
}

func (c *Client) ptzXAddr(ctx context.Context) (string, error) {
	if err := c.resolveServices(ctx); err != nil {
		return "", err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ptz == "" {
		return "", ErrNoPTZ
	}
	return c.ptz, nil
}

// ContinuousMove starts moving at the given velocities in [-1, 1]; call Stop
// to end the movement.
func (c *Client) ContinuousMove(ctx context.Context, profileToken string, pan, tilt, zoom float64) error {
	ptz, err := c.ptzXAddr(ctx)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`<tptz:ContinuousMove xmlns:tptz="%s" xmlns:tt="%s"><tptz:ProfileToken>%s</tptz:ProfileToken>`+
		`<tptz:Velocity><tt:PanTilt x="%g" y="%g"/><tt:Zoom x="%g"/></tptz:Velocity></tptz:ContinuousMove>`,
		nsPTZ, nsSchema, xmlEscape(profileToken), pan, tilt, zoom)
	return c.call(ctx, ptz, body, "ContinuousMoveResponse", nil)
}

// Stop ends pan/tilt and zoom movement.
func (c *Client) Stop(ctx context.Context, profileToken string) error {
	ptz, err := c.ptzXAddr(ctx)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`<tptz:Stop xmlns:tptz="%s"><tptz:ProfileToken>%s</tptz:ProfileToken>`+
		`<tptz:PanTilt>true</tptz:PanTilt><tptz:Zoom>true</tptz:Zoom></tptz:Stop>`, nsPTZ, xmlEscape(profileToken))
	return c.call(ctx, ptz, body, "StopResponse", nil)
}

// GotoPreset moves to a stored preset.
func (c *Client) GotoPreset(ctx context.Context, profileToken, presetToken string) error {
	ptz, err := c.ptzXAddr(ctx)
	if err != nil {
		return err
	}
	body := fmt.Sprintf(`<tptz:GotoPreset xmlns:tptz="%s"><tptz:ProfileToken>%s</tptz:ProfileToken>`+
		`<tptz:PresetToken>%s</tptz:PresetToken></tptz:GotoPreset>`, nsPTZ, xmlEscape(profileToken), xmlEscape(presetToken))
	return c.call(ctx, ptz, body, "GotoPresetResponse", nil)
}

// Presets lists stored presets.
func (c *Client) Presets(ctx context.Context, profileToken string) ([]Preset, error) {
	ptz, err := c.ptzXAddr(ctx)
	if err != nil {
		return nil, err
	}
	var out struct {
		Presets []struct {
			Token string `xml:"token,attr"`
			Name  string `xml:"Name"`
		} `xml:"Preset"`
	}
	body := fmt.Sprintf(`<tptz:GetPresets xmlns:tptz="%s"><tptz:ProfileToken>%s</tptz:ProfileToken></tptz:GetPresets>`, nsPTZ, xmlEscape(profileToken))
	if err := c.call(ctx, ptz, body, "GetPresetsResponse", &out); err != nil {
		return nil, err
	}
	presets := make([]Preset, 0, len(out.Presets))
	for _, p := range out.Presets {
		presets = append(presets, Preset{Token: p.Token, Name: p.Name})
	}
	return presets, nil
}
