// cmd/aegis/runtime.go
// Runtime holds the currently running Aegis configuration and all active
// components derived from it.  NewRuntime creates a hollow Runtime (no engine
// running); StartEngine populates it from a validated Config and raw YAML bytes.
package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"os"
	"sync"

	"github.com/tamzrod/Aegis/internal/adapter"
	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/engine"
	runtimepkg "github.com/tamzrod/Aegis/internal/runtime"
)

// Runtime holds the active configuration and all running components.
// It implements webui.Manager so the WebUI server can interact with it.
type Runtime struct {
	mu               sync.Mutex
	activeConfigYAML []byte
	configPath       string
	cancel           context.CancelFunc
	servers          []*adapter.Server
	rtm              runtimepkg.RuntimeManager
}

// NewRuntime creates a hollow Runtime that tracks cfgPath and runtime state
// but has no engine running yet.  Call StartEngine after config validation.
func NewRuntime(cfgPath string) *Runtime {
	return &Runtime{configPath: cfgPath}
}

// SetError marks the runtime as not running and records a startup error.
// It is called by main when config validation or engine startup fails.
func (r *Runtime) SetError(err error) {
	r.rtm.SetError(err)
}

// StartEngine builds and starts the engine from a validated Config.
// The caller must have previously called config.Validate(cfg).
func (r *Runtime) StartEngine(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuildLocked(cfg, yamlBytes)
}

// Stop cancels the running engine context and shuts down all Modbus listeners.
// It is called on graceful process shutdown.
func (r *Runtime) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.cancel != nil {
		r.cancel()
	}
	for _, srv := range r.servers {
		srv.Shutdown()
	}
}

// RuntimeStatus implements webui.StatusProvider.
// It returns a thread-safe copy of the current runtime state.
func (r *Runtime) RuntimeStatus() runtimepkg.RuntimeState {
	return r.rtm.Status()
}

// GetActiveConfigYAML implements webui.Manager.
// It returns a copy of the active config YAML bytes.
func (r *Runtime) GetActiveConfigYAML() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.activeConfigYAML))
	copy(out, r.activeConfigYAML)
	return out
}

// ApplyConfig implements webui.Manager.
// It parses yamlBytes, validates, writes to disk, then atomically rebuilds the runtime.
func (r *Runtime) ApplyConfig(yamlBytes []byte) error {
	cfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := os.WriteFile(r.configPath, yamlBytes, 0600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return r.rebuildLocked(cfg, yamlBytes)
}

// ReloadFromDisk implements webui.Manager.
// It re-reads the config file, validates it, then atomically rebuilds the runtime.
func (r *Runtime) ReloadFromDisk() error {
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
	return r.rebuildLocked(cfg, data)
}

// rebuildLocked performs an atomic runtime swap.  The caller must hold r.mu.
// It stops the old engine and Modbus listeners, then starts new ones.
func (r *Runtime) rebuildLocked(cfg *config.Config, yamlBytes []byte) error {
	// Stop old engine.
	if r.cancel != nil {
		r.cancel()
	}
	// Stop old Modbus listeners (waits for each listener goroutine to exit).
	for _, srv := range r.servers {
		srv.Shutdown()
	}

	// Build new components.
	store, err := config.BuildMemStore(cfg)
	if err != nil {
		return fmt.Errorf("memory store build: %w", err)
	}
	units, err := engine.Build(cfg, store)
	if err != nil {
		return fmt.Errorf("engine build: %w", err)
	}

	healthStore := engine.NewBlockHealthStore()
	ctx, cancel := context.WithCancel(context.Background())

	authority := adapter.BuildAuthorityRegistry(cfg, healthStore)

	seenPorts := make(map[uint16]struct{})
	for _, u := range cfg.Replicator.Units {
		seenPorts[u.Target.Port] = struct{}{}
	}

	var newServers []*adapter.Server
	for port := range seenPorts {
		listenAddr := fmt.Sprintf(":%d", port)
		srv := adapter.NewServer(listenAddr, store, authority)
		newServers = append(newServers, srv)
		go func(listen string, s *adapter.Server) {
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, net.ErrClosed) {
				log.Printf("aegis: adapter (%s) failed: %v", listen, err)
			}
		}(listenAddr, srv)
	}

	for _, u := range units {
		out := make(chan engine.PollResult, 8)
		go runOrchestrator(ctx, u.Poller.UnitID(), u.Poller, u.Writer, healthStore, out)
		go u.Poller.Run(ctx, out)
	}

	r.cancel = cancel
	r.servers = newServers
	r.activeConfigYAML = yamlBytes
	r.rtm.SetRunning()
	return nil
}
