// cmd/aegis/runtime.go
// RuntimeManager holds the currently running Aegis configuration and all active
// components derived from it. NewRuntimeManager creates a hollow RuntimeManager
// (no engine running); Start populates it from a validated Config and raw YAML bytes.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"

	"github.com/tamzrod/Aegis/internal/adapter"
	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/engine"
	runtimepkg "github.com/tamzrod/Aegis/internal/runtime"
)

// RuntimeManager holds the active configuration and all running components.
// It implements the WebUI manager interfaces so the WebUI server can interact with it.
type RuntimeManager struct {
	mu               sync.Mutex
	activeConfigYAML []byte
	configPath       string

	// processCtx lives for the lifetime of the process (cancelled on OS shutdown).
	// runtimeCtx is derived from processCtx and cancelled by StopRuntime/rebuild.
	processCtx    context.Context
	runtimeCancel context.CancelFunc

	// servers are the active Modbus TCP adapter listeners.
	servers []*adapter.Server

	// wg tracks all goroutines started by the runtime (pollers, orchestrators, adapters).
	wg sync.WaitGroup

	// listenerStatuses records the bind result for each configured port.
	listenerStatuses []runtimepkg.ListenerStatus

	// state tracks the STOPPED/STARTING/RUNNING/STOPPING lifecycle.
	state runtimepkg.RuntimeManager
}

// NewRuntimeManager creates a hollow RuntimeManager that tracks cfgPath and
// runtime state but has no engine running yet. Call Start after config validation.
func NewRuntimeManager(cfgPath string, processCtx context.Context) *RuntimeManager {
	r := &RuntimeManager{
		configPath: cfgPath,
		processCtx: processCtx,
	}
	r.state.SetStopped()
	return r
}

// SetError marks the runtime as not running and records a startup error.
// It is called by main when config validation or engine startup fails.
func (r *RuntimeManager) SetError(err error) {
	r.state.SetError(err)
}

// Start builds and starts the engine from a validated Config.
// The caller must have previously called config.Validate(cfg).
func (r *RuntimeManager) Start(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, yamlBytes)
}

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

// Rebuild atomically stops the running engine (if any), builds new components
// from cfg, and starts them. It is safe to call whether or not the engine is
// currently running.
func (r *RuntimeManager) Rebuild(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, yamlBytes)
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

// GetActiveConfigYAML returns a copy of the active config YAML bytes.
func (r *RuntimeManager) GetActiveConfigYAML() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.activeConfigYAML))
	copy(out, r.activeConfigYAML)
	return out
}

// ApplyConfig parses yamlBytes, validates, writes to disk, then atomically rebuilds the runtime.
// The new YAML becomes the active config.
func (r *RuntimeManager) ApplyConfig(yamlBytes []byte) error {
	cfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Atomic write: write to a uniquely-named temp file in the same directory
	// as the config (ensures same filesystem for atomic rename), then rename.
	dir := filepath.Dir(r.configPath)
	tmpFile, err := os.CreateTemp(dir, "config.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, werr := tmpFile.Write(yamlBytes); werr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp config file: %w", werr)
	}
	if cerr := tmpFile.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp config file: %w", cerr)
	}
	if cherr := os.Chmod(tmpPath, 0600); cherr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp config file: %w", cherr)
	}
	if rerr := os.Rename(tmpPath, r.configPath); rerr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config file: %w", rerr)
	}

	return r.rebuild(cfg, yamlBytes)
}

// ReloadFromDisk re-reads the config file, validates it, then atomically rebuilds the runtime.
func (r *RuntimeManager) ReloadFromDisk() error {
	r.mu.Lock()
	path := r.configPath
	r.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	cfg, err := config.LoadBytes(data)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, data)
}

// rebuild performs an atomic runtime swap. The caller must hold r.mu.
// It stops the current runtime (if any), builds new components, pre-binds all
// adapter ports synchronously to surface bind errors before returning, then
// starts goroutines. The WebUI HTTP server is never touched by this path.
func (r *RuntimeManager) rebuild(cfg *config.Config, yamlBytes []byte) error {
	// Stop pollers and adapters if running.
	if cancel := r.runtimeCancel; cancel != nil {
		cancel()
		r.runtimeCancel = nil
	}
	for _, srv := range r.servers {
		srv.Shutdown()
	}
	r.servers = nil

	// Wait for all previously-started runtime goroutines to exit before
	// binding new ports or starting new goroutines.
	r.wg.Wait()

	r.state.SetStarting()

	// Build new components.
	store, err := config.BuildMemStore(cfg)
	if err != nil {
		werr := fmt.Errorf("memory store build: %w", err)
		r.state.SetError(werr)
		return werr
	}
	units, err := engine.Build(cfg, store)
	if err != nil {
		werr := fmt.Errorf("engine build: %w", err)
		r.state.SetError(werr)
		return werr
	}

	healthStore := engine.NewBlockHealthStore()

	// Create a new runtime context derived from the process context.
	// Cancelling runtimeCtx stops pollers and orchestrators without
	// affecting the WebUI HTTP server (which must NOT use runtimeCtx).
	runtimeCtx, runtimeCancel := context.WithCancel(r.processCtx)

	authority := adapter.BuildAuthorityRegistry(cfg, healthStore)

	seenPorts := make(map[uint16]struct{})
	for _, u := range cfg.Replicator.Units {
		seenPorts[u.Target.Port] = struct{}{}
	}

	// Pre-bind all adapter ports synchronously so that bind errors are surfaced immediately.
	type boundPort struct {
		port uint16
		ln   net.Listener
	}

	var (
		bound            []boundPort
		listenerStatuses []runtimepkg.ListenerStatus
	)

	for port := range seenPorts {
		addr := fmt.Sprintf(":%d", port)

		ln, bindErr := net.Listen("tcp", addr)
		if bindErr != nil {
			// Close any already-bound listeners before returning.
			for _, b := range bound {
				_ = b.ln.Close()
			}
			runtimeCancel()

			werr := fmt.Errorf("adapter (%s) failed to bind: %w", addr, bindErr)
			listenerStatuses = append(listenerStatuses, runtimepkg.ListenerStatus{
				Port:   port,
				Status: "error",
				Error:  werr.Error(),
			})
			r.listenerStatuses = listenerStatuses
			r.state.SetError(werr)
			return werr
		}

		bound = append(bound, boundPort{port: port, ln: ln})
		listenerStatuses = append(listenerStatuses, runtimepkg.ListenerStatus{
			Port:   port,
			Status: "listening",
			Error:  "",
		})
	}

	// Start adapters using pre-bound listeners.
	var newServers []*adapter.Server
	for _, b := range bound {
		addr := fmt.Sprintf(":%d", b.port)

		// NOTE:
		// - Uses the pre-bound net.Listener to guarantee the bind succeeded.
		// - Debug routing must be OFF by default (cfg.Debug.AdapterRouting default false).
		srv := adapter.NewServerWithListener(addr, b.ln, store, authority, cfg.Debug.AdapterRouting)

		newServers = append(newServers, srv)
		r.wg.Add(1)
		go func(s *adapter.Server) {
			defer r.wg.Done()
			if err := s.Serve(); err != nil {
				log.Printf("aegis: adapter (%s) exited: %v", s.Addr(), err)
			}
		}(srv)
	}

	// Start pollers + orchestrators.
	for _, u := range units {
		out := make(chan engine.PollResult, 8)

		r.wg.Add(2)
		go func(id string, cs counterSource, pw pollWriter, hw blockHealthWriter, ch <-chan engine.PollResult) {
			defer r.wg.Done()
			runOrchestrator(runtimeCtx, id, cs, pw, hw, ch)
		}(u.Poller.UnitID(), u.Poller, u.Writer, healthStore, out)

		go func(p *engine.Poller, ch chan<- engine.PollResult) {
			defer r.wg.Done()
			p.Run(runtimeCtx, ch)
		}(u.Poller, out)
	}

	r.runtimeCancel = runtimeCancel
	r.servers = newServers
	r.activeConfigYAML = yamlBytes
	r.listenerStatuses = listenerStatuses
	r.state.SetRunning()
	return nil
}