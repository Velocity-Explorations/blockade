package store

import "sync"

// PrincipalRecord holds the per-principal state tracked for the cost curve.
type PrincipalRecord struct {
	RequestCount int64
	EnrolledAt   int64 // unix timestamp
}

// PrincipalStore persists per-principal metering state. Implementations must
// be safe for concurrent use.
type PrincipalStore interface {
	CreatePrincipal(id string, enrolledAt int64) error
	GetPrincipal(id string) (PrincipalRecord, bool, error)
	IncrementRequestCount(id string) (newCount int64, err error)
}

// MemPrincipalStore is an in-memory PrincipalStore. State is lost on restart.
type MemPrincipalStore struct {
	mu         sync.Mutex
	principals map[string]*PrincipalRecord
}

func NewMemPrincipalStore() *MemPrincipalStore {
	return &MemPrincipalStore{principals: make(map[string]*PrincipalRecord)}
}

func (s *MemPrincipalStore) CreatePrincipal(id string, enrolledAt int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.principals[id] = &PrincipalRecord{RequestCount: 0, EnrolledAt: enrolledAt}
	return nil
}

func (s *MemPrincipalStore) GetPrincipal(id string) (PrincipalRecord, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.principals[id]
	if !ok {
		return PrincipalRecord{}, false, nil
	}
	return *r, true, nil
}

func (s *MemPrincipalStore) IncrementRequestCount(id string) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.principals[id]
	if !ok {
		return 0, nil
	}
	r.RequestCount++
	return r.RequestCount, nil
}
