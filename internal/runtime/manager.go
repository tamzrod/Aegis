// internal/runtime/manager.go
package runtime

import (
"sync"

"github.com/tamzrod/Aegis/internal/orchestrator"
)

// Re-export types and constants from orchestrator so internal/adapter/webui remains unchanged.
type RuntimeState = orchestrator.RuntimeState
type DeviceStatus = orchestrator.DeviceStatus
type ListenerStatus = orchestrator.ListenerStatus
type StatusBlockSnapshot = orchestrator.StatusBlockSnapshot

// State constants re-exported from orchestrator.
const (
StateStopped  = orchestrator.StateStopped
StateStarting = orchestrator.StateStarting
StateRunning  = orchestrator.StateRunning
StateStopping = orchestrator.StateStopping
)

// RuntimeManager tracks the running state of the replicator engine.
// All methods are safe for concurrent use.
// This type is kept here for backwards compatibility and is used by tests.
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

// GetState returns only the State string from the current RuntimeState.
func (m *RuntimeManager) GetState() string {
m.mu.Lock()
defer m.mu.Unlock()
return m.state.State
}

// SetRunning marks the engine as running and clears any previous error.
func (m *RuntimeManager) SetRunning() {
m.mu.Lock()
defer m.mu.Unlock()
m.state = RuntimeState{Running: true, State: StateRunning}
}

// SetStarting marks the engine as starting.
func (m *RuntimeManager) SetStarting() {
m.mu.Lock()
defer m.mu.Unlock()
m.state = RuntimeState{Running: false, State: StateStarting}
}

// SetStopping marks the engine as stopping.
func (m *RuntimeManager) SetStopping() {
m.mu.Lock()
defer m.mu.Unlock()
m.state = RuntimeState{Running: false, State: StateStopping}
}

// SetStopped marks the engine as stopped and clears any previous error.
func (m *RuntimeManager) SetStopped() {
m.mu.Lock()
defer m.mu.Unlock()
m.state = RuntimeState{Running: false, State: StateStopped}
}

// SetError marks the engine as stopped and records the error message.
// Calling SetError(nil) is a no-op — use SetRunning to clear error state.
func (m *RuntimeManager) SetError(err error) {
if err == nil {
return
}
m.mu.Lock()
defer m.mu.Unlock()
m.state = RuntimeState{Running: false, State: StateStopped, Error: err.Error()}
}
