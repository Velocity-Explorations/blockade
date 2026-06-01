// Package store defines the anti-replay token store used by payment verifiers.
// The Store interface is satisfied by MemStore (in-process, zero config) and
// SQLiteStore (file-backed, survives restarts). Both implementations are safe
// for concurrent use.
package store

import "sync"

// Store records which payment tokens have been spent so that a token cannot be
// used more than once even across proxy restarts (when using SQLiteStore).
type Store interface {
	// IsUsed reports whether key has already been spent.
	IsUsed(key string) (bool, error)
	// MarkUsed records key as spent. Calling MarkUsed on an already-spent key
	// is a no-op (idempotent).
	MarkUsed(key string) error
	// Close releases any resources held by the store.
	Close() error
}

// MemStore is an in-memory Store. State is lost when the process exits.
// It is the default when no db_path is configured.
type MemStore struct {
	mu   sync.RWMutex
	used map[string]struct{}
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{used: make(map[string]struct{})}
}

func (s *MemStore) IsUsed(key string) (bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.used[key]
	return ok, nil
}

func (s *MemStore) MarkUsed(key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.used[key] = struct{}{}
	return nil
}

func (s *MemStore) Close() error { return nil }
