package engine

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"go.etcd.io/bbolt"
)

var (
	bucketMeta     = []byte("meta")
	bucketTasks    = []byte("tasks")
	bucketChunks   = []byte("chunks")
	bucketSettings = []byte("settings")
	bucketHistory  = []byte("history")
	bucketHosts    = []byte("host_profiles")
)

const storeVersion = 1

// Store is a bbolt-backed task store with a single-writer goroutine pattern for progress.
type Store struct {
	db *bbolt.DB
	mu sync.Mutex
}

func OpenStore(path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	db, err := bbolt.Open(path, 0o600, &bbolt.Options{Timeout: 3 * time.Second})
	if err != nil {
		return nil, err
	}
	s := &Store{db: db}
	if err := s.migrate(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

func (s *Store) migrate() error {
	return s.db.Update(func(tx *bbolt.Tx) error {
		for _, name := range [][]byte{bucketMeta, bucketTasks, bucketChunks, bucketSettings, bucketHistory, bucketHosts} {
			if _, err := tx.CreateBucketIfNotExists(name); err != nil {
				return err
			}
		}
		b := tx.Bucket(bucketMeta)
		v := b.Get([]byte("version"))
		if v == nil {
			return b.Put([]byte("version"), []byte(fmt.Sprintf("%d", storeVersion)))
		}
		return nil
	})
}

func (s *Store) Close() error {
	return s.db.Close()
}

func (s *Store) PutTask(t *Task) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	t.UpdatedAt = time.Now()
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := json.Marshal(t)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketTasks).Put([]byte(t.ID), b)
	})
}

func (s *Store) GetTask(id TaskID) (*Task, error) {
	var t Task
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucketTasks).Get([]byte(id))
		if raw == nil {
			return fmt.Errorf("task not found: %s", id)
		}
		return json.Unmarshal(raw, &t)
	})
	if err != nil {
		return nil, err
	}
	return &t, nil
}

func (s *Store) ListTasks() ([]*Task, error) {
	var out []*Task
	err := s.db.View(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketTasks).ForEach(func(k, v []byte) error {
			var t Task
			if err := json.Unmarshal(v, &t); err != nil {
				return err
			}
			out = append(out, &t)
			return nil
		})
	})
	return out, err
}

func (s *Store) DeleteTask(id TaskID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		_ = tx.Bucket(bucketChunks).Delete([]byte(id))
		return tx.Bucket(bucketTasks).Delete([]byte(id))
	})
}

func (s *Store) PutChunks(id TaskID, chunks []Chunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		b, err := json.Marshal(chunks)
		if err != nil {
			return err
		}
		return tx.Bucket(bucketChunks).Put([]byte(id), b)
	})
}

func (s *Store) GetChunks(id TaskID) ([]Chunk, error) {
	var chunks []Chunk
	err := s.db.View(func(tx *bbolt.Tx) error {
		raw := tx.Bucket(bucketChunks).Get([]byte(id))
		if raw == nil {
			return nil
		}
		return json.Unmarshal(raw, &chunks)
	})
	return chunks, err
}

func (s *Store) DeleteChunks(id TaskID) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketChunks).Delete([]byte(id))
	})
}

// GetSetting returns a raw settings value from the settings bucket.
func (s *Store) GetSetting(key string) ([]byte, bool, error) {
	var out []byte
	err := s.db.View(func(tx *bbolt.Tx) error {
		v := tx.Bucket(bucketSettings).Get([]byte(key))
		if v == nil {
			return nil
		}
		out = append([]byte(nil), v...)
		return nil
	})
	return out, out != nil, err
}

// PutSetting writes a raw settings value to the settings bucket.
func (s *Store) PutSetting(key string, value []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.db.Update(func(tx *bbolt.Tx) error {
		return tx.Bucket(bucketSettings).Put([]byte(key), value)
	})
}

func (s *Store) Sync() error {
	return s.db.Sync()
}
