// cmd/aegis/runtime_lifecycle.go — lifecycle control domain
// Responsibility: external lifecycle operations on the RuntimeManager.
// StopRuntime/StartRuntime expose guarded stop-and-start via the WebUI.
// Stop is the graceful-shutdown path called by main on OS signal.
// RuntimeStatus and ListenerStatuses expose read-only state snapshots.
package main

import (
	"fmt"
	"log"

	"github.com/tamzrod/Aegis/internal/adapter"
	"github.com/tamzrod/Aegis/internal/config"
	runtimepkg "github.com/tamzrod/Aegis/internal/runtime"
)

// StopRuntime stops the running engine without changing the active config.
// Returns an error if the runtime is not in RUNNING state.
func (r *RuntimeManager) StopRuntime() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.state.GetState()
	if st != runtimepkg.StateRunning {
		return fmt.Errorf("cannot stop: runtime state is %s", st)
	}
	r.state.SetStopping()

	cancel := r.runtimeCancel
	servers := append([]*adapter.Server(nil), r.servers...)

	// Cancel runtime context to stop pollers and orchestrators.
	if cancel != nil {
		cancel()
	}
	// Shut down Modbus TCP adapters.
	for _, srv := range servers {
		srv.Shutdown()
	}
	// Wait for all runtime goroutines to exit.
	// NOTE: We intentionally hold r.mu here: runtime goroutines must not acquire r.mu.
	r.wg.Wait()

	r.servers = nil
	r.runtimeCancel = nil
	r.listenerStatuses = nil
	r.state.SetStopped()

	log.Println("aegis: runtime stopped")
	return nil
}

// StartRuntime starts the runtime using the active config.
// Returns an error if the runtime is not in STOPPED state.
func (r *RuntimeManager) StartRuntime() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.state.GetState()
	if st != runtimepkg.StateStopped {
		return fmt.Errorf("cannot start: runtime state is %s", st)
	}
	yamlBytes := r.activeConfigYAML
	if len(yamlBytes) == 0 {
		return fmt.Errorf("cannot start: no active config loaded")
	}

	cfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	return r.rebuild(cfg, yamlBytes)
}

// Stop cancels the running engine context and shuts down all Modbus listeners.
// It is called on graceful process shutdown.
func (r *RuntimeManager) Stop() {
	r.mu.Lock()
	cancel := r.runtimeCancel
	servers := append([]*adapter.Server(nil), r.servers...)
	r.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	for _, srv := range servers {
		srv.Shutdown()
	}
	r.wg.Wait()

	r.mu.Lock()
	r.servers = nil
	r.runtimeCancel = nil
	r.listenerStatuses = nil
	r.state.SetStopped()
	r.mu.Unlock()
}

// RuntimeStatus returns a thread-safe copy of the current runtime state.
func (r *RuntimeManager) RuntimeStatus() runtimepkg.RuntimeState {
	return r.state.Status()
}

// ListenerStatuses returns a copy of the per-port listener status slice.
func (r *RuntimeManager) ListenerStatuses() []runtimepkg.ListenerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]runtimepkg.ListenerStatus, len(r.listenerStatuses))
	copy(out, r.listenerStatuses)
	return out
}
