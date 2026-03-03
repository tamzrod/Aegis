// internal/core/store.go
package core

import "sync"

// Store is the authoritative in-process memory registry.
// Both the Modbus TCP server adapter and the replication engine depend on this interface.
//
// Architectural rule (LOCKED):
//   - All memory access is in-process. No TCP loopback.
//   - The engine writes directly into Store; the adapter reads from the same Store.
type Store interface {
	Get(id MemoryID) (*Memory, bool)
	MustGet(id MemoryID) (*Memory, error)
}

// MemStore is the authoritative implementation of Store.
// It is safe for concurrent use.
type MemStore struct {
	mu   sync.RWMutex
	data map[MemoryID]*Memory
}

// NewMemStore creates an empty MemStore.
func NewMemStore() *MemStore {
	return &MemStore{
		data: make(map[MemoryID]*Memory),
	}
}

// Add registers a Memory instance under the given MemoryID.
// Returns an error if the id is invalid or mem is nil.
func (s *MemStore) Add(id MemoryID, mem *Memory) error {
	if s == nil {
		return ErrNilStore
	}
	if err := id.Validate(); err != nil {
		return err
	}
	if mem == nil {
		return ErrNilMemory
	}

	s.mu.Lock()
	s.data[id] = mem
	s.mu.Unlock()

	return nil
}

// Get returns the Memory for the given id, or (nil, false) if not found.
func (s *MemStore) Get(id MemoryID) (*Memory, bool) {
	if s == nil {
		return nil, false
	}
	s.mu.RLock()
	mem, ok := s.data[id]
	s.mu.RUnlock()
	return mem, ok
}

// MustGet returns the Memory for the given id, or ErrUnknownMemoryID if not found.
func (s *MemStore) MustGet(id MemoryID) (*Memory, error) {
	mem, ok := s.Get(id)
	if !ok {
		return nil, ErrUnknownMemoryID
	}
	return mem, nil
}
