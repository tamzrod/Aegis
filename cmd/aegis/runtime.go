// cmd/aegis/runtime.go
// RuntimeManager holds the currently running Aegis configuration and all active
// components derived from it. NewRuntimeManager creates a hollow RuntimeManager
// (no engine running); Start/Rebuild populate it from a validated Config.
// The rebuild function is the core engine of this file: it atomically stops the
// current runtime (if any), builds new components, pre-binds all ports, and
// starts goroutines.
package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"sync"

	"github.com/tamzrod/Aegis/internal/adapter"
	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/core"
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

	// healthStore is the per-read-block health state for the current runtime.
	// It is replaced on each rebuild and read by DeviceStatuses.
	healthStore *engine.BlockHealthStore

	// latencyTracker records per-unit poll latency statistics.
	// It is replaced on each rebuild and read by ReadDeviceStatus.
	latencyTracker *PollLatencyTracker

	// statusUnitIndex maps (port, statusUnitID, statusSlot) to unit ID.
	// It is built during rebuild and used by ReadDeviceStatus for O(1) lookup.
	statusUnitIndex map[statusUnitKey]string

	// store is the in-process register store for the current runtime.
	// It is replaced on each rebuild and read by ReadDeviceStatus.
	store core.Store

	// activeCfg is the most recently built config, used by DeviceStatuses.
	activeCfg *config.Config

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

// Rebuild atomically stops the running engine (if any), builds new components
// from cfg, and starts them. It is safe to call whether or not the engine is
// currently running.
func (r *RuntimeManager) Rebuild(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, yamlBytes)
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
	latencyTracker := NewPollLatencyTracker()

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
			runOrchestrator(runtimeCtx, id, cs, pw, hw, ch, latencyTracker)
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
	r.healthStore = healthStore
	r.latencyTracker = latencyTracker
	r.statusUnitIndex = buildStatusUnitIndex(cfg)
	r.store = store
	r.activeCfg = cfg
	r.state.SetRunning()
	return nil
}
