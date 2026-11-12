// Package store is the hive's local SQLite persistence (ADR-0003, ADR-0009):
// one file, WAL mode, embedded SQL migrations, a single writer. Camera
// credentials are encrypted at rest with a key the caller derives from the
// hive identity key, so a copied database reveals no passwords.
package store

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"sort"
	"strings"

	_ "modernc.org/sqlite" // database/sql driver
)

//go:embed migrations/*.sql
var migrationFS embed.FS

// Errors.
var (
	ErrNotFound    = errors.New("store: not found")
	ErrCredentials = errors.New("store: cannot decrypt credentials (wrong key?)")
)

// Store is an open hive database.
type Store struct {
	db   *sql.DB
	aead cipher.AEAD
}

// Open opens or creates the database at path, applies pending migrations and
// prepares credential encryption with key (32 bytes).
func Open(ctx context.Context, path string, key []byte) (*Store, error) {
	if len(key) != 32 {
		return nil, errors.New("store: encryption key must be 32 bytes")
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(WAL)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(ON)&_pragma=synchronous(NORMAL)")
	if err != nil {
		return nil, fmt.Errorf("store: open %s: %w", path, err)
	}
	db.SetMaxOpenConns(1) // one writer; SQLite serialises anyway, this makes it explicit
	s := &Store{db: db, aead: aead}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the database.
func (s *Store) Close() error { return s.db.Close() }

// SchemaVersion returns the number of applied migrations.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migration`).Scan(&v)
	return v, err
}

// migrate applies migrations/NNNN_*.sql in order, each in a transaction,
// recording them in schema_migration. Re-running is a no-op.
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migration (version INTEGER PRIMARY KEY, name TEXT NOT NULL, applied_at TEXT NOT NULL DEFAULT (strftime('%Y-%m-%dT%H:%M:%fZ','now')))`); err != nil {
		return fmt.Errorf("store: migration table: %w", err)
	}
	applied, err := s.SchemaVersion(ctx)
	if err != nil {
		return err
	}
	names, err := fs.Glob(migrationFS, "migrations/*.sql")
	if err != nil {
		return err
	}
	sort.Strings(names)
	for i, name := range names {
		version := i + 1
		if version <= applied {
			continue
		}
		if !strings.HasPrefix(name, fmt.Sprintf("migrations/%04d_", version)) {
			return fmt.Errorf("store: migration %s is out of sequence (expected %04d)", name, version)
		}
		body, err := migrationFS.ReadFile(name)
		if err != nil {
			return err
		}
		if err := s.applyMigration(ctx, version, name, string(body)); err != nil {
			return fmt.Errorf("store: apply %s: %w", name, err)
		}
	}
	return nil
}

func (s *Store) applyMigration(ctx context.Context, version int, name, body string) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migration (version, name) VALUES (?, ?)`, version, name); err != nil {
		return err
	}
	return tx.Commit()
}

// seal encrypts a secret: nonce || ciphertext.
func (s *Store) seal(plain string) []byte {
	nonce := make([]byte, s.aead.NonceSize())
	_, _ = rand.Read(nonce)
	return s.aead.Seal(nonce, nonce, []byte(plain), nil)
}

// unseal reverses seal.
func (s *Store) unseal(blob []byte) (string, error) {
	n := s.aead.NonceSize()
	if len(blob) < n {
		return "", ErrCredentials
	}
	plain, err := s.aead.Open(nil, blob[:n], blob[n:], nil)
	if err != nil {
		return "", ErrCredentials
	}
	return string(plain), nil
}
