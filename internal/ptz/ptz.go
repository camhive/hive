// Package ptz moves cameras: a thin controller over the ONVIF client that
// knows how a camera.Camera maps to an ONVIF device and profile, validates
// velocities, and answers "unsupported" for cameras that cannot move (RTSP
// sources, ONVIF devices without a PTZ service or without a known profile).
package ptz

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/camhive/hive/internal/domain/camera"
	"github.com/camhive/hive/internal/onvif"
)

// Errors.
var (
	ErrUnsupported = errors.New("ptz: camera does not support PTZ")
	ErrInvalid     = errors.New("ptz: velocities must be within [-1, 1]")
)

// Preset is a stored position.
type Preset = onvif.Preset

// Controller drives PTZ on cameras. Safe for concurrent use.
type Controller struct {
	mu      sync.Mutex
	clients map[camera.ID]*onvif.Client
	newFn   func(xaddr string, creds onvif.Credentials) *onvif.Client
}

// New creates a Controller.
func New() *Controller {
	return &Controller{clients: map[camera.ID]*onvif.Client{}, newFn: func(x string, c onvif.Credentials) *onvif.Client { return onvif.NewClient(x, c) }}
}

// client returns the ONVIF client for an ONVIF camera with a main profile.
func (c *Controller) client(cam camera.Camera) (*onvif.Client, string, error) {
	if cam.Source.Kind != camera.SourceONVIF {
		return nil, "", ErrUnsupported
	}
	token := cam.Streams[camera.Main].ProfileToken
	if token == "" {
		return nil, "", fmt.Errorf("%w: main stream has no ONVIF profile", ErrUnsupported)
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	cl, ok := c.clients[cam.ID]
	if !ok {
		cl = c.newFn(cam.Source.Address, onvif.Credentials{Username: cam.Credentials.Username, Password: cam.Credentials.Password})
		c.clients[cam.ID] = cl
	}
	return cl, token, nil
}

// Forget drops the cached client (after credentials or address change).
func (c *Controller) Forget(id camera.ID) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.clients, id)
}

// Supported reports whether the camera offers PTZ. A transport error is an
// error, not "unsupported": the caller should not hide a dead camera.
func (c *Controller) Supported(ctx context.Context, cam camera.Camera) (bool, error) {
	cl, _, err := c.client(cam)
	if errors.Is(err, ErrUnsupported) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	caps, err := cl.Capabilities(ctx)
	if err != nil {
		return false, err
	}
	return caps.PTZ, nil
}

// Move starts continuous movement; velocities in [-1, 1].
func (c *Controller) Move(ctx context.Context, cam camera.Camera, pan, tilt, zoom float64) error {
	for _, v := range []float64{pan, tilt, zoom} {
		if v < -1 || v > 1 {
			return ErrInvalid
		}
	}
	cl, token, err := c.client(cam)
	if err != nil {
		return err
	}
	return mapNoPTZ(cl.ContinuousMove(ctx, token, pan, tilt, zoom))
}

// Stop ends movement.
func (c *Controller) Stop(ctx context.Context, cam camera.Camera) error {
	cl, token, err := c.client(cam)
	if err != nil {
		return err
	}
	return mapNoPTZ(cl.Stop(ctx, token))
}

// Presets lists stored positions.
func (c *Controller) Presets(ctx context.Context, cam camera.Camera) ([]Preset, error) {
	cl, token, err := c.client(cam)
	if err != nil {
		return nil, err
	}
	p, err := cl.Presets(ctx, token)
	return p, mapNoPTZ(err)
}

// GotoPreset moves to a stored position.
func (c *Controller) GotoPreset(ctx context.Context, cam camera.Camera, preset string) error {
	cl, token, err := c.client(cam)
	if err != nil {
		return err
	}
	return mapNoPTZ(cl.GotoPreset(ctx, token, preset))
}

func mapNoPTZ(err error) error {
	if errors.Is(err, onvif.ErrNoPTZ) {
		return fmt.Errorf("%w: %w", ErrUnsupported, err)
	}
	return err
}
