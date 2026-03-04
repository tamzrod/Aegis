// internal/runtime/manager.go
package runtime

import "sync"

// State constants represent the four lifecycle states of the runtime engine.
const (
	StateStopped  = "STOPPED"
	StateStarting = "STARTING"
	StateRunning  = "RUNNING"
	StateStopping = "STOPPING"
)

// DeviceStatus is a snapshot of one replicator unit's current operational status.
// It is produced by DeviceStatusProvider and consumed by the WebUI layer.
type DeviceStatus struct {
	ID      string `json:"id"`
	Status  string `json:"status"`  // "online", "error", "offline", "warning"
	Polling bool   `json:"polling"` // true if a successful poll occurred recently
}

// ListenerStatus describes the bind result for one Modbus TCP adapter port.
// It is safe to copy by value.
type ListenerStatus struct {
	Port   uint16 `json:"port"`
	Status string `json:"status"` // "listening" or "error"
	Error  string `json:"error,omitempty"`
}

// RuntimeState is a snapshot of the replicator engine's operational state.
// It is safe to copy by value.
type RuntimeState struct {
	Running   bool   `json:"running"`
	State     string `json:"state"`
	Error     string `json:"error,omitempty"`
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
