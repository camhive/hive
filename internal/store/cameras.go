package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/camhive/hive/internal/domain/camera"
)

// Cameras returns the camera repository.
func (s *Store) Cameras() *Cameras { return &Cameras{s: s} }

// Cameras persists camera.Camera values.
type Cameras struct {
	s *Store
}

const cameraColumns = `id, name, grp, source_kind, source_addr, username, password_enc, streams_json, enabled, created_at, updated_at`

// Put inserts or replaces a camera (idempotent on ID).
func (r *Cameras) Put(ctx context.Context, c camera.Camera) error {
	streams, err := json.Marshal(c.Streams)
	if err != nil {
		return err
	}
	var pw []byte
	if c.Credentials.Password != "" {
		pw = r.s.seal(c.Credentials.Password)
	}
	_, err = r.s.db.ExecContext(ctx, `
		INSERT INTO camera (`+cameraColumns+`) VALUES (?,?,?,?,?,?,?,?,?,?,?)
		ON CONFLICT(id) DO UPDATE SET
		  name=excluded.name, grp=excluded.grp, source_kind=excluded.source_kind, source_addr=excluded.source_addr,
		  username=excluded.username, password_enc=excluded.password_enc, streams_json=excluded.streams_json,
		  enabled=excluded.enabled, updated_at=excluded.updated_at`,
		string(c.ID), c.Name, c.Group, string(c.Source.Kind), c.Source.Address, c.Credentials.Username, pw, string(streams),
		boolInt(c.Enabled), c.CreatedAt.UTC().Format(time.RFC3339Nano), c.UpdatedAt.UTC().Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("store: put camera: %w", err)
	}
	return nil
}

// Get returns one camera or ErrNotFound.
func (r *Cameras) Get(ctx context.Context, id camera.ID) (camera.Camera, error) {
	row := r.s.db.QueryRowContext(ctx, `SELECT `+cameraColumns+` FROM camera WHERE id = ?`, string(id))
	c, err := r.scan(row)
	if errors.Is(err, sql.ErrNoRows) {
		return camera.Camera{}, ErrNotFound
	}
	return c, err
}

// List returns all cameras ordered by name.
func (r *Cameras) List(ctx context.Context) ([]camera.Camera, error) {
	rows, err := r.s.db.QueryContext(ctx, `SELECT `+cameraColumns+` FROM camera ORDER BY name, id`)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var out []camera.Camera
	for rows.Next() {
		c, err := r.scan(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Delete removes a camera; deleting an unknown id is not an error.
func (r *Cameras) Delete(ctx context.Context, id camera.ID) error {
	_, err := r.s.db.ExecContext(ctx, `DELETE FROM camera WHERE id = ?`, string(id))
	return err
}

type scanner interface {
	Scan(dest ...any) error
}

func (r *Cameras) scan(row scanner) (camera.Camera, error) {
	var (
		c                    camera.Camera
		id, kind, streamsRaw string
		pw                   []byte
		enabled              int
		created, updated     string
	)
	if err := row.Scan(&id, &c.Name, &c.Group, &kind, &c.Source.Address, &c.Credentials.Username, &pw, &streamsRaw, &enabled, &created, &updated); err != nil {
		return camera.Camera{}, err
	}
	c.ID, c.Source.Kind, c.Enabled = camera.ID(id), camera.SourceKind(kind), enabled != 0
	if len(pw) > 0 {
		plain, err := r.s.unseal(pw)
		if err != nil {
			return camera.Camera{}, err
		}
		c.Credentials.Password = plain
	}
	if err := json.Unmarshal([]byte(streamsRaw), &c.Streams); err != nil {
		return camera.Camera{}, fmt.Errorf("store: camera %s streams: %w", id, err)
	}
	var err error
	if c.CreatedAt, err = time.Parse(time.RFC3339Nano, created); err != nil {
		return camera.Camera{}, err
	}
	if c.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated); err != nil {
		return camera.Camera{}, err
	}
	return c, nil
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
