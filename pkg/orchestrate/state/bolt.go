package state

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"go.etcd.io/bbolt"
)

// DefaultDBPath returns the default BoltDB path: ~/.local/share/nix-compose/state.bolt
func DefaultDBPath() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("determining home directory: %w", err)
	}
	return filepath.Join(home, ".local", "share", "nix-compose", "state.bolt"), nil
}

// DB wraps a bbolt database with collections for orchestration state.
type DB struct {
	bolt *bbolt.DB
	path string
}

// Open opens (or creates) the BoltDB at the given path and ensures all buckets exist.
func Open(path string) (*DB, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("creating db directory: %w", err)
	}

	log.Printf("state: opening %s", path)
	boltDB, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 2 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("opening bolt db %s: %w", path, err)
	}

	db := &DB{bolt: boltDB, path: path}

	err = boltDB.Update(func(tx *bbolt.Tx) error {
		for _, b := range allCollections {
			if _, err := tx.CreateBucketIfNotExists(b); err != nil {
				return fmt.Errorf("creating bucket %s: %w", b, err)
			}
		}
		return nil
	})
	if err != nil {
		_ = boltDB.Close()
		return nil, fmt.Errorf("initializing buckets: %w", err)
	}

	return db, nil
}

// Close closes the underlying BoltDB.
func (db *DB) Close() error {
	if db.bolt == nil {
		return nil
	}
	if err := db.bolt.Close(); err != nil {
		return fmt.Errorf("closing bolt db: %w", err)
	}
	return nil
}

// Bolt returns the raw bbolt.DB for advanced use.
func (db *DB) Bolt() *bbolt.DB {
	return db.bolt
}
