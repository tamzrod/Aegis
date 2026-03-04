// internal/runtime/manager.go
package runtime

import "sync"

// RuntimeState is a snapshot of the replicator engine's operational state.
// It is safe to copy by value.
type RuntimeState struct {
	Running bool   `json:"running"`
	Error   string `json:"error,omitempty"`
}

// RuntimeManager tracks the running state of the replicator engine.
// All methods are safe for concurrent use.
type RuntimeManager struct {
	mu    sync.Mutex
	state RuntimeState
}

// Status returns a copy of the current RuntimeState.
func (m *RuntimeManager) Status() RuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

// SetRunning marks the engine as running and clears any previous error.
func (m *RuntimeManager) SetRunning() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: true}
}

// SetError marks the engine as not running and records the error message.
// Calling SetError(nil) is a no-op — use SetRunning to clear error state.
func (m *RuntimeManager) SetError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: false, Error: err.Error()}
}
