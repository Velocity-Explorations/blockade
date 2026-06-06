// Package store defines the anti-replay token store used by payment verifiers.
// The Store interface is satisfied by MemStore (in-process, zero config) and
// SQLiteStore (file-backed, survives restarts). Both implementations are safe
// for concurrent use.
package store

import (
	"sync"
	"time"
)

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

// PendingEntry holds the state for an issued but not yet paid address.
type PendingEntry struct {
	Sats      int64
	ExpiresAt time.Time
}

// PendingStore persists issued-but-unpaid on-chain payment addresses so they
// survive proxy restarts. Both MemStore and SQLiteStore implement this interface.
type PendingStore interface {
	// AddPending records a newly issued address and its payment requirements.
	AddPending(addr string, sats int64, expiresAt time.Time) error
	// GetPending retrieves the pending entry for addr.
	// Returns (entry, false, nil) if addr is not present.
	GetPending(addr string) (PendingEntry, bool, error)
	// DeletePending removes addr from the pending set.
	DeletePending(addr string) error
	// PruneExpiredPending removes all entries whose expiry is before the given time.
	PruneExpiredPending(before time.Time) error
}

// MemStore is an in-memory Store and PendingStore. State is lost when the
// process exits. It is the default when no db_path is configured.
type MemStore struct {
	mu      sync.RWMutex
	used    map[string]struct{}
	pending map[string]PendingEntry
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		used:    make(map[string]struct{}),
		pending: make(map[string]PendingEntry),
	}
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

func (s *MemStore) AddPending(addr string, sats int64, expiresAt time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending[addr] = PendingEntry{Sats: sats, ExpiresAt: expiresAt}
	return nil
}

func (s *MemStore) GetPending(addr string) (PendingEntry, bool, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	e, ok := s.pending[addr]
	return e, ok, nil
}

func (s *MemStore) DeletePending(addr string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.pending, addr)
	return nil
}

func (s *MemStore) PruneExpiredPending(before time.Time) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for addr, e := range s.pending {
		if before.After(e.ExpiresAt) {
			delete(s.pending, addr)
		}
	}
	return nil
}
