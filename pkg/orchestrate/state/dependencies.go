package state

import (
	"encoding/json"
	"fmt"

	"github.com/systemstart/nix-compose/pkg/orchestrate/typing"
	"go.etcd.io/bbolt"
)

func (db *DB) addTo(tx *bbolt.Tx, bucket Collection, fromReference, toReference typing.Reference) error {
	collection := tx.Bucket(bucket)
	fromKey := []byte(fromReference.GetId())
	current := collection.Get(fromKey)

	var toList typing.ReferenceList
	if current != nil {
		err := json.Unmarshal(current, &toList)
		if err != nil {
			return fmt.Errorf("unmarshal links for %s failed: %w", fromReference.GetId(), err)
		}
	} else {
		toList = make(typing.ReferenceList, 0)
	}
	toList = append(toList, toReference)
	toList = toList.Unique()

	serializedToList, err := json.Marshal(toList)
	if err != nil {
		return fmt.Errorf("marshal links for %s failed: %w", fromReference.GetId(), err)
	}

	if err = collection.Put(fromKey, serializedToList); err != nil {
		return fmt.Errorf("put links for %s failed: %w", fromReference.GetId(), err)
	}
	return nil
}

// AddLink persists a bidirectional dependency link.
func (db *DB) AddLink(l *Link) error {
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		err := db.addTo(tx, LinksBySourceId, l.Source, l.Target)
		if err != nil {
			return fmt.Errorf("couldn't save link %s -> %s: %w", l.Source, l.Target, err)
		}
		err = db.addTo(tx, LinksByTargetId, l.Target, l.Source)
		if err != nil {
			return fmt.Errorf("couldn't save link %s -> %s: %w", l.Target, l.Source, err)
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("couldn't add link: %w", err)
	}
	return nil
}

// GetDependencies returns the targets that source depends on.
func (db *DB) GetDependencies(source typing.Reference) (typing.ReferenceList, error) {
	var toList typing.ReferenceList

	err := db.bolt.View(func(tx *bbolt.Tx) error {
		collection := tx.Bucket(LinksBySourceId)
		sourceKey := []byte(source.GetId())

		current := collection.Get(sourceKey)
		if current == nil {
			return nil
		}

		err := json.Unmarshal(current, &toList)
		if err != nil {
			return fmt.Errorf("unmarshal dependencies for %s failed: %w", source.GetId(), err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get dependencies for %s failed: %w", source.GetId(), err)
	}

	return toList, nil
}

// GetDepending returns the sources that depend on target.
func (db *DB) GetDepending(target typing.Reference) (typing.ReferenceList, error) {
	var toList typing.ReferenceList

	err := db.bolt.View(func(tx *bbolt.Tx) error {
		collection := tx.Bucket(LinksByTargetId)
		targetKey := []byte(target.GetId())

		current := collection.Get(targetKey)
		if current == nil {
			return nil
		}

		err := json.Unmarshal(current, &toList)
		if err != nil {
			return fmt.Errorf("unmarshal depending for %s failed: %w", target.GetId(), err)
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("get depending for %s failed: %w", target.GetId(), err)
	}

	return toList, nil
}

// RemoveLink removes all outgoing links from a resource and cleans reverse entries.
func (db *DB) RemoveLink(r typing.Reference) error {
	err := db.bolt.Update(func(tx *bbolt.Tx) error {
		sourceKey := []byte(r.GetId())
		sourceCollection := tx.Bucket(LinksBySourceId)

		targets, err := loadTargets(sourceCollection, sourceKey, r.GetId())
		if err != nil {
			return err
		}

		if err := sourceCollection.Delete(sourceKey); err != nil {
			return fmt.Errorf("delete source %s failed: %w", r.GetId(), err)
		}

		return cleanReverseLinks(tx.Bucket(LinksByTargetId), targets, r.GetId())
	})
	if err != nil {
		return fmt.Errorf("couldn't remove link: %w", err)
	}
	return nil
}

// loadTargets reads the target list for a source key from a bucket.
func loadTargets(bucket *bbolt.Bucket, key []byte, id string) (typing.ReferenceList, error) {
	current := bucket.Get(key)
	if current == nil {
		return nil, nil
	}
	var targets typing.ReferenceList
	if err := json.Unmarshal(current, &targets); err != nil {
		return nil, fmt.Errorf("unmarshal links for %s failed: %w", id, err)
	}
	return targets, nil
}

// cleanReverseLinks removes sourceID from each target's reverse link list.
func cleanReverseLinks(targetBucket *bbolt.Bucket, targets typing.ReferenceList, sourceID string) error {
	for _, target := range targets {
		targetKey := []byte(target.GetId())
		raw := targetBucket.Get(targetKey)
		if raw == nil {
			continue
		}
		var sources typing.ReferenceList
		if err := json.Unmarshal(raw, &sources); err != nil {
			return fmt.Errorf("unmarshal target links for %s failed: %w", target.GetId(), err)
		}
		filtered := make(typing.ReferenceList, 0, len(sources))
		for _, s := range sources {
			if s.GetId() != sourceID {
				filtered = append(filtered, s)
			}
		}
		if err := updateOrDeleteBucketEntry(targetBucket, targetKey, filtered, target.GetId()); err != nil {
			return err
		}
	}
	return nil
}

// updateOrDeleteBucketEntry removes the key if filtered is empty, otherwise serializes and puts.
func updateOrDeleteBucketEntry(bucket *bbolt.Bucket, key []byte, filtered typing.ReferenceList, id string) error {
	if len(filtered) == 0 {
		if err := bucket.Delete(key); err != nil {
			return fmt.Errorf("delete target %s failed: %w", id, err)
		}
		return nil
	}
	serialized, err := json.Marshal(filtered)
	if err != nil {
		return fmt.Errorf("marshal target links for %s failed: %w", id, err)
	}
	if err := bucket.Put(key, serialized); err != nil {
		return fmt.Errorf("put target links for %s failed: %w", id, err)
	}
	return nil
}
