package state

import (
	"encoding/json"
	"fmt"
	"log"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"go.etcd.io/bbolt"
)

// Collection identifies a BoltDB bucket.
type Collection []byte

var (
	DeploymentsById = Collection("deployments-by-id")
	RolloutsById    = Collection("rollouts-by-id")
	LinksBySourceId = Collection("links-by-source-id")
	LinksByTargetId = Collection("links-by-target-id")
	allCollections  = []Collection{
		DeploymentsById,
		RolloutsById,
		LinksBySourceId,
		LinksByTargetId,
	}
)

// CollectionBatchFn is the callback for Batch iteration.
type CollectionBatchFn func(key []byte, value []byte)

// Batch iterates over all entries in a collection.
func (db *DB) Batch(c Collection, batchFn CollectionBatchFn) error {
	log.Printf("state: running batch on %s", c)

	err := db.bolt.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(c)
		if b == nil {
			return fmt.Errorf("bucket %s not found", c)
		}
		return b.ForEach(func(k, v []byte) error {
			batchFn(k, v)
			return nil
		})
	})
	if err != nil {
		return fmt.Errorf("batch on %s failed: %w", c, err)
	}
	return nil
}

// Keys returns all keys in a collection.
func (db *DB) Keys(c Collection) ([]string, error) {
	var result []string
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(c)
		if b == nil {
			return fmt.Errorf("bucket %s not found", c)
		}
		return b.ForEach(func(k, _ []byte) error {
			result = append(result, string(k))
			return nil
		})
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't list keys: %w", err)
	}
	return result, nil
}

// Load retrieves a raw JSON value by key from a collection.
func (db *DB) Load(c Collection, id string) (json.RawMessage, error) {
	var result json.RawMessage
	err := db.bolt.View(func(tx *bbolt.Tx) error {
		b := tx.Bucket(c)
		if b == nil {
			return fmt.Errorf("bucket %s not found", c)
		}
		v := b.Get([]byte(id))
		if v != nil {
			result = make(json.RawMessage, len(v))
			copy(result, v)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("couldn't load entry: %w", err)
	}
	return result, nil
}

// Delete removes an entry by key from a collection.
func (db *DB) Delete(c Collection, id string) error {
	log.Printf("state: deleting id '%s' from collection '%s'", id, c)
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(c)
		if b == nil {
			return fmt.Errorf("bucket %s not found", c)
		}
		return b.Delete([]byte(id))
	})
	if err != nil {
		return fmt.Errorf("db delete failed: %w", err)
	}
	return nil
}

// Save marshals and stores an Identity-bearing value into a collection.
func (db *DB) Save(c Collection, i typing.Identity) error {
	serialized, err := json.Marshal(i)
	if err != nil {
		return fmt.Errorf("couldn't marshal data: %w", err)
	}

	log.Printf("state: storing id '%s' to collection '%s'", i.GetId(), c)
	err = db.bolt.Update(func(tx *bbolt.Tx) error {
		b := tx.Bucket(c)
		if b == nil {
			return fmt.Errorf("bucket %s not found", c)
		}
		return b.Put([]byte(i.GetId()), serialized)
	})
	if err != nil {
		return fmt.Errorf("db update failed: %w", err)
	}

	return nil
}
