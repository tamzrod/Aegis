// cmd/aegis/runtime.go
// RuntimeManager holds the currently running Aegis configuration and all active
// components derived from it.  NewRuntimeManager creates a hollow RuntimeManager
// (no engine running); Start populates it from a validated Config and raw YAML bytes.
package main

import (
	"context"
	"errors"
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
// It implements webui.Manager so the WebUI server can interact with it.
type RuntimeManager struct {
	mu               sync.Mutex
	running          bool
	activeConfigYAML []byte
	configPath       string
	cancel           context.CancelFunc
	servers          []*adapter.Server
	state            runtimepkg.RuntimeManager
}

// NewRuntimeManager creates a hollow RuntimeManager that tracks cfgPath and
// runtime state but has no engine running yet.  Call Start after config validation.
func NewRuntimeManager(cfgPath string) *RuntimeManager {
	return &RuntimeManager{configPath: cfgPath}
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

// Stop cancels the running engine context and shuts down all Modbus listeners.
// It is called on graceful process shutdown.
func (r *RuntimeManager) Stop() {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.running {
		if r.cancel != nil {
			r.cancel()
		}
		r.running = false
	}
	for _, srv := range r.servers {
		srv.Shutdown()
	}
	r.servers = nil
}

// Rebuild atomically stops the running engine (if any), builds new components
// from cfg, and starts them.  It is safe to call whether or not the engine is
// currently running.  The caller must hold no locks.
func (r *RuntimeManager) Rebuild(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, yamlBytes)
}

// RuntimeStatus implements webui.StatusProvider.
// It returns a thread-safe copy of the current runtime state.
func (r *RuntimeManager) RuntimeStatus() runtimepkg.RuntimeState {
	return r.state.Status()
}

// GetActiveConfigYAML implements webui.Manager.
// It returns a copy of the active config YAML bytes.
func (r *RuntimeManager) GetActiveConfigYAML() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.activeConfigYAML))
	copy(out, r.activeConfigYAML)
	return out
}

// ApplyConfig implements webui.Manager.
// It parses yamlBytes, validates, writes to disk, then atomically rebuilds the runtime.
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

// ReloadFromDisk implements webui.Manager.
// It re-reads the config file, validates it, then atomically rebuilds the runtime.
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

// rebuild performs an atomic runtime swap.  The caller must hold r.mu.
// If the runtime is currently running, it stops pollers (via context cancel)
// and adapters (via Shutdown) before starting new ones.
func (r *RuntimeManager) rebuild(cfg *config.Config, yamlBytes []byte) error {
	// Stop pollers and adapters if running.
	if r.running {
		if r.cancel != nil {
			r.cancel()
		}
		r.running = false
	}
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

	// Start adapters.
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

	// Start pollers.
	for _, u := range units {
		out := make(chan engine.PollResult, 8)
		go runOrchestrator(ctx, u.Poller.UnitID(), u.Poller, u.Writer, healthStore, out)
		go u.Poller.Run(ctx, out)
	}

	r.cancel = cancel
	r.running = true
	r.servers = newServers
	r.activeConfigYAML = yamlBytes
	r.state.SetRunning()
	return nil
}
