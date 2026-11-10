// Package store persists Heimdall's own state: findings, scans, rehearsals,
// migrations, freshness reports and the activity feed.
//
// Traffic data is deliberately not stored here. The gateway's log store is
// the source of truth for traffic; Heimdall reads aggregates from it on
// demand and keeps only its conclusions, so its state stays small no matter
// how much traffic the gateway carries.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync/atomic"
	"time"

	bolt "go.etcd.io/bbolt"
)

// Buckets.
const (
	Findings   = "findings"   // key: provider/model
	Scans      = "scans"      // key: time-ordered id
	Rehearsals = "rehearsals" // key: time-ordered id
	Migrations = "migrations" // key: provider/model
	Freshness  = "freshness"  // key: time-ordered id
	Events     = "events"     // key: time-ordered id
	Meta       = "meta"
)

var buckets = []string{Findings, Scans, Rehearsals, Migrations, Freshness, Events, Meta}

// ErrNotFound is returned by Get when the key does not exist.
var ErrNotFound = errors.New("not found")

// Store is a small transactional key-value store in one file.
type Store struct {
	db  *bolt.DB
	seq atomic.Uint64
}

// Open opens (or creates) the store in dir.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	db, err := bolt.Open(filepath.Join(dir, "heimdall.db"), 0o600, &bolt.Options{Timeout: 5 * time.Second})
	if err != nil {
		return nil, fmt.Errorf("opening store: %w", err)
	}
	err = db.Update(func(tx *bolt.Tx) error {
		for _, b := range buckets {
			if _, err := tx.CreateBucketIfNotExists([]byte(b)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		db.Close()
		return nil, err
	}
	return &Store{db: db}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// NewID returns an identifier that sorts by creation time.
func (s *Store) NewID(prefix string) string {
	return fmt.Sprintf("%s_%s_%04d", prefix, time.Now().UTC().Format("20060102T150405.000"), s.seq.Add(1)%10000)
}

// Put stores v under key.
func (s *Store) Put(bucket, key string, v any) error {
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(bucket)).Put([]byte(key), data) })
}

// Get loads the value at key into out.
func (s *Store) Get(bucket, key string, out any) error {
	return s.db.View(func(tx *bolt.Tx) error {
		data := tx.Bucket([]byte(bucket)).Get([]byte(key))
		if data == nil {
			return ErrNotFound
		}
		return json.Unmarshal(data, out)
	})
}

// Delete removes a key.
func (s *Store) Delete(bucket, key string) error {
	return s.db.Update(func(tx *bolt.Tx) error { return tx.Bucket([]byte(bucket)).Delete([]byte(key)) })
}

// Each calls fn with every value in the bucket, in key order. With newestFirst
// it walks time-ordered buckets from the latest entry back. fn returns false
// to stop.
func (s *Store) Each(bucket string, newestFirst bool, fn func(key string, data []byte) bool) error {
	return s.db.View(func(tx *bolt.Tx) error {
		c := tx.Bucket([]byte(bucket)).Cursor()
		if newestFirst {
			for k, v := c.Last(); k != nil; k, v = c.Prev() {
				if !fn(string(k), v) {
					break
				}
			}
			return nil
		}
		for k, v := c.First(); k != nil; k, v = c.Next() {
			if !fn(string(k), v) {
				break
			}
		}
		return nil
	})
}

// List decodes up to limit values from a bucket (limit <= 0 means all).
func List[T any](s *Store, bucket string, newestFirst bool, limit int) ([]T, error) {
	out := []T{}
	err := s.Each(bucket, newestFirst, func(_ string, data []byte) bool {
		var v T
		if json.Unmarshal(data, &v) == nil {
			out = append(out, v)
		}
		return limit <= 0 || len(out) < limit
	})
	return out, err
}

// Trim keeps only the newest keep entries of a time-ordered bucket.
func (s *Store) Trim(bucket string, keep int) error {
	return s.db.Update(func(tx *bolt.Tx) error {
		b := tx.Bucket([]byte(bucket))
		n := b.Stats().KeyN
		if n <= keep {
			return nil
		}
		c := b.Cursor()
		var doomed [][]byte
		for k, _ := c.First(); k != nil && len(doomed) < n-keep; k, _ = c.Next() {
			doomed = append(doomed, append([]byte{}, k...))
		}
		for _, k := range doomed {
			if err := b.Delete(k); err != nil {
				return err
			}
		}
		return nil
	})
}
