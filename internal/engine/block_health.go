// internal/engine/block_health.go
package engine

import (
	"sync"
	"time"
)

// ReadBlockHealth holds the per-read-block health state produced by the engine.
// It is populated by the polling orchestrator and consumed by the adapter for
// authority enforcement.  The engine only produces this state; it never uses it
// to gate its own writes.
type ReadBlockHealth struct {
	Timeout           bool
	ConsecutiveErrors int
	LastExceptionCode byte
	LastSuccess       time.Time
	LastError         time.Time
}

// BlockHealthKey identifies one read block in the health store.
type BlockHealthKey struct {
	UnitID   string // replicator unit ID (from config)
	BlockIdx int    // index within the unit's reads list
}

// BlockHealthStore is a thread-safe store for per-read-block health state.
// It is written by the engine orchestrator and read by the adapter.
// Both engine and adapter access this store in-process; no network is involved.
type BlockHealthStore struct {
	mu      sync.RWMutex
	entries map[BlockHealthKey]ReadBlockHealth
}

// NewBlockHealthStore creates an empty BlockHealthStore.
func NewBlockHealthStore() *BlockHealthStore {
	return &BlockHealthStore{
		entries: make(map[BlockHealthKey]ReadBlockHealth),
	}
}

// Set writes the health state for one read block.
func (s *BlockHealthStore) Set(key BlockHealthKey, h ReadBlockHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = h
}

// Get reads the health state for one read block.
// Returns the zero value and false if no state has been recorded yet.
func (s *BlockHealthStore) Get(key BlockHealthKey) (ReadBlockHealth, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.entries[key]
	return h, ok
}

// SetBlockHealth sets the health state for one read block using primitive key components.
// This method allows callers to avoid constructing BlockHealthKey directly, preventing
// the internal key layout from leaking into other packages.
func (s *BlockHealthStore) SetBlockHealth(unitID string, blockIdx int, h ReadBlockHealth) {
	s.Set(BlockHealthKey{UnitID: unitID, BlockIdx: blockIdx}, h)
}

// GetBlockHealth returns the health state for one read block as primitive values.
// This method satisfies the adapter.BlockHealthReader interface via structural typing
// without requiring the adapter to import the engine package.
// Returns (timeout, consecutiveErrors, exceptionCode, found).
func (s *BlockHealthStore) GetBlockHealth(unitID string, blockIdx int) (timeout bool, consecutiveErrors int, exceptionCode byte, found bool) {
	h, ok := s.Get(BlockHealthKey{UnitID: unitID, BlockIdx: blockIdx})
	return h.Timeout, h.ConsecutiveErrors, h.LastExceptionCode, ok
}
