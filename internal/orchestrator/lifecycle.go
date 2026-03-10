// internal/orchestrator/lifecycle.go
// Package orchestrator provides the top-level coordination layer for Aegis.
// It holds the active configuration, manages the running engine components, and
// implements the WebUI manager interfaces.
package orchestrator

import (
	"context"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/memory"
	"github.com/tamzrod/Aegis/internal/puller"
)

// ---- Runtime state types ----

// State constants represent the four lifecycle states of the runtime engine.
const (
	StateStopped  = "STOPPED"
	StateStarting = "STARTING"
	StateRunning  = "RUNNING"
	StateStopping = "STOPPING"
)

// RuntimeState is a snapshot of the replicator engine's operational state.
type RuntimeState struct {
	Running bool   `json:"running"`
	State   string `json:"state"`
	Error   string `json:"error,omitempty"`
}

// DeviceStatus is a snapshot of one replicator unit's current operational status.
type DeviceStatus struct {
	ID      string `json:"id"`
	Status  string `json:"status"`
	Polling bool   `json:"polling"`
}

// ListenerStatus describes the bind result for one Modbus TCP adapter port.
type ListenerStatus struct {
	Port   uint16 `json:"port"`
	Status string `json:"status"`
	Error  string `json:"error,omitempty"`
}

// StatusBlockSnapshot holds the decoded contents of a device status register block.
type StatusBlockSnapshot struct {
	Health              string `json:"health"`
	Online              bool   `json:"online"`
	SecondsInError      uint16 `json:"seconds_in_error"`
	RequestsTotal       uint32 `json:"requests_total"`
	ResponsesValid      uint32 `json:"responses_valid"`
	TimeoutsTotal       uint32 `json:"timeouts_total"`
	TransportErrors     uint32 `json:"transport_errors"`
	ConsecutiveFailCurr uint16 `json:"consecutive_fail_curr"`
	ConsecutiveFailMax  uint16 `json:"consecutive_fail_max"`

	LastPollMs uint32 `json:"last_poll_ms"`
	AvgPollMs  uint32 `json:"avg_poll_ms"`
	MaxPollMs  uint32 `json:"max_poll_ms"`
}

// ---- Internal state machine ----

// runtimeStateManager tracks the STOPPED/STARTING/RUNNING/STOPPING lifecycle.
type runtimeStateManager struct {
	mu    sync.Mutex
	state RuntimeState
}

func (m *runtimeStateManager) Status() RuntimeState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state
}

func (m *runtimeStateManager) GetState() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.state.State
}

func (m *runtimeStateManager) SetRunning() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: true, State: StateRunning}
}

func (m *runtimeStateManager) SetStarting() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: false, State: StateStarting}
}

func (m *runtimeStateManager) SetStopping() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: false, State: StateStopping}
}

func (m *runtimeStateManager) SetStopped() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: false, State: StateStopped}
}

func (m *runtimeStateManager) SetError(err error) {
	if err == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.state = RuntimeState{Running: false, State: StateStopped, Error: err.Error()}
}

// ---- Latency tracker ----

// PollLatencyTracker records per-unit poll latency statistics.
type PollLatencyTracker struct {
	mu      sync.Mutex
	entries map[string]*latencyEntry
}

type latencyEntry struct {
	last  uint32
	max   uint32
	sumMs uint64
	count uint64
}

// NewPollLatencyTracker creates an empty PollLatencyTracker.
func NewPollLatencyTracker() *PollLatencyTracker {
	return &PollLatencyTracker{entries: make(map[string]*latencyEntry)}
}

// Record stores a single poll duration sample for the given unit.
func (t *PollLatencyTracker) Record(unitID string, ms uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[unitID]
	if !ok {
		e = &latencyEntry{}
		t.entries[unitID] = e
	}
	e.last = ms
	if ms > e.max {
		e.max = ms
	}
	e.sumMs += uint64(ms)
	e.count++
}

// Get returns the last, average, and maximum poll latency in milliseconds.
func (t *PollLatencyTracker) Get(unitID string) (last, avg, max uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[unitID]
	if !ok || e.count == 0 {
		return 0, 0, 0
	}
	return e.last, uint32(e.sumMs / e.count), e.max
}

// ---- Manager ----

// statusUnitKey uniquely identifies a device status block by its Modbus addressing tuple.
type statusUnitKey struct {
	port         uint16
	statusUnitID uint16
	statusSlot   uint16
}

// Manager holds the active configuration and all running components.
// It implements the WebUI manager interfaces.
type Manager struct {
	mu               sync.Mutex
	activeConfigYAML []byte
	configPath       string

	processCtx    context.Context
	runtimeCancel context.CancelFunc

	servers []*memory.Server

	wg sync.WaitGroup

	listenerStatuses []ListenerStatus

	healthStore    *memory.BlockHealthStore
	latencyTracker *PollLatencyTracker

	statusUnitIndex map[statusUnitKey]string

	store     memory.Store
	activeCfg *config.Config

	state runtimeStateManager
}

// NewManager creates a hollow Manager with no engine running yet.
func NewManager(cfgPath string, processCtx context.Context) *Manager {
	r := &Manager{
		configPath: cfgPath,
		processCtx: processCtx,
	}
	r.state.SetStopped()
	return r
}

// SetError marks the runtime as not running and records a startup error.
func (r *Manager) SetError(err error) {
	r.state.SetError(err)
}

// SetActiveConfigYAML sets the active config YAML bytes without starting the engine.
func (r *Manager) SetActiveConfigYAML(b []byte) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.activeConfigYAML = b
}

// Start builds and starts the engine from a validated Config.
func (r *Manager) Start(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, yamlBytes)
}

// StopRuntime stops the running engine without changing the active config.
func (r *Manager) StopRuntime() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.state.GetState()
	if st != StateRunning {
		return fmt.Errorf("cannot stop: runtime state is %s", st)
	}
	r.state.SetStopping()

	cancel := r.runtimeCancel
	servers := append([]*memory.Server(nil), r.servers...)

	if cancel != nil {
		cancel()
	}
	for _, srv := range servers {
		srv.Shutdown()
	}
	r.wg.Wait()

	r.servers = nil
	r.runtimeCancel = nil
	r.listenerStatuses = nil
	r.state.SetStopped()

	log.Println("aegis: runtime stopped")
	return nil
}

// StartRuntime starts the runtime using the active config.
func (r *Manager) StartRuntime() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	st := r.state.GetState()
	if st != StateStopped {
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
func (r *Manager) Stop() {
	r.mu.Lock()
	cancel := r.runtimeCancel
	servers := append([]*memory.Server(nil), r.servers...)
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

// Rebuild atomically stops the running engine (if any) and starts with the new config.
func (r *Manager) Rebuild(cfg *config.Config, yamlBytes []byte) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, yamlBytes)
}

// RuntimeStatus returns a thread-safe copy of the current runtime state.
func (r *Manager) RuntimeStatus() RuntimeState {
	return r.state.Status()
}

// ListenerStatuses returns a copy of the per-port listener status slice.
func (r *Manager) ListenerStatuses() []ListenerStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ListenerStatus, len(r.listenerStatuses))
	copy(out, r.listenerStatuses)
	return out
}

// DeviceStatuses returns the per-device operational status.
func (r *Manager) DeviceStatuses() []DeviceStatus {
	r.mu.Lock()
	running := r.state.Status().Running
	hs := r.healthStore
	cfg := r.activeCfg
	r.mu.Unlock()

	if cfg == nil {
		return nil
	}

	now := time.Now()
	out := make([]DeviceStatus, 0, len(cfg.Replicator.Units))
	for _, u := range cfg.Replicator.Units {
		status := "offline"
		polling := false
		if running && hs != nil {
			status = deriveDeviceStatus(hs, u)
			polling = isDevicePolling(hs, u, now)
		}
		out = append(out, DeviceStatus{ID: u.ID, Status: status, Polling: polling})
	}
	return out
}

// ReadDeviceStatus reads and decodes the status register block for the device.
func (r *Manager) ReadDeviceStatus(port, statusUnitID, statusSlot uint16) (*StatusBlockSnapshot, error) {
	r.mu.Lock()
	st := r.store
	lt := r.latencyTracker
	unitID := r.statusUnitIndex[statusUnitKey{port, statusUnitID, statusSlot}]
	r.mu.Unlock()

	if st == nil {
		return nil, fmt.Errorf("runtime store not available")
	}

	mem, err := st.MustGet(memory.MemoryID{Port: port, UnitID: statusUnitID})
	if err != nil {
		return nil, fmt.Errorf("status memory not found (port=%d unit_id=%d): %w", port, statusUnitID, err)
	}

	baseAddr := statusSlot * memory.StatusSlotsPerDevice
	rawBytes := make([]byte, int(memory.StatusSlotsPerDevice)*2)
	if err := mem.ReadRegs(memory.AreaHoldingRegs, baseAddr, memory.StatusSlotsPerDevice, rawBytes); err != nil {
		return nil, fmt.Errorf("status read failed (port=%d unit_id=%d slot=%d): %w", port, statusUnitID, statusSlot, err)
	}

	regs := make([]uint16, memory.StatusSlotsPerDevice)
	for i := range regs {
		regs[i] = uint16(rawBytes[i*2])<<8 | uint16(rawBytes[i*2+1])
	}

	snap := memory.DecodeStatusBlock(regs)

	healthStr := healthCodeToString(snap.Health)
	online := snap.Health == memory.HealthOK

	var lastMs, avgMs, maxMs uint32
	if lt != nil && unitID != "" {
		lastMs, avgMs, maxMs = lt.Get(unitID)
	}

	return &StatusBlockSnapshot{
		Health:              healthStr,
		Online:              online,
		SecondsInError:      snap.SecondsInError,
		RequestsTotal:       snap.RequestsTotal,
		ResponsesValid:      snap.ResponsesValidTotal,
		TimeoutsTotal:       snap.TimeoutsTotal,
		TransportErrors:     snap.TransportErrorsTotal,
		ConsecutiveFailCurr: snap.ConsecutiveFailCurr,
		ConsecutiveFailMax:  snap.ConsecutiveFailMax,
		LastPollMs:          lastMs,
		AvgPollMs:           avgMs,
		MaxPollMs:           maxMs,
	}, nil
}

// ReadViewerRegisters reads raw register or coil values from the in-process store.
func (r *Manager) ReadViewerRegisters(deviceKey string, fc uint8, address, quantity uint16) ([]uint16, error) {
	r.mu.Lock()
	st := r.store
	cfg := r.activeCfg
	r.mu.Unlock()

	if st == nil {
		return nil, fmt.Errorf("runtime store not available")
	}
	if cfg == nil {
		return nil, fmt.Errorf("no active configuration")
	}

	var targetPort uint16
	var targetUnitID uint16
	found := false
	for _, u := range cfg.Replicator.Units {
		if u.ID == deviceKey {
			targetPort = u.Target.Port
			targetUnitID = uint16(u.Target.UnitID)
			found = true
			break
		}
	}
	if !found {
		return nil, fmt.Errorf("device %q not found in active configuration", deviceKey)
	}

	mem, err := st.MustGet(memory.MemoryID{Port: targetPort, UnitID: targetUnitID})
	if err != nil {
		return nil, fmt.Errorf("memory not found (port=%d unit_id=%d): %w", targetPort, targetUnitID, err)
	}

	switch fc {
	case 1:
		packed := make([]byte, (int(quantity)+7)/8)
		if err := mem.ReadBits(memory.AreaCoils, address, quantity, packed); err != nil {
			return nil, fmt.Errorf("FC1 read failed: %w", err)
		}
		return unpackBitsToUint16(packed, quantity), nil

	case 2:
		packed := make([]byte, (int(quantity)+7)/8)
		if err := mem.ReadBits(memory.AreaDiscreteInputs, address, quantity, packed); err != nil {
			return nil, fmt.Errorf("FC2 read failed: %w", err)
		}
		return unpackBitsToUint16(packed, quantity), nil

	case 3:
		raw := make([]byte, int(quantity)*2)
		if err := mem.ReadRegs(memory.AreaHoldingRegs, address, quantity, raw); err != nil {
			return nil, fmt.Errorf("FC3 read failed: %w", err)
		}
		return bytesToUint16s(raw, quantity), nil

	case 4:
		raw := make([]byte, int(quantity)*2)
		if err := mem.ReadRegs(memory.AreaInputRegs, address, quantity, raw); err != nil {
			return nil, fmt.Errorf("FC4 read failed: %w", err)
		}
		return bytesToUint16s(raw, quantity), nil

	default:
		return nil, fmt.Errorf("unsupported function code %d", fc)
	}
}

// GetActiveConfigYAML returns a copy of the active config YAML bytes.
func (r *Manager) GetActiveConfigYAML() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.activeConfigYAML))
	copy(out, r.activeConfigYAML)
	return out
}

// ApplyConfig parses yamlBytes, validates, writes to disk, then atomically rebuilds the runtime.
func (r *Manager) ApplyConfig(yamlBytes []byte) error {
	cfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

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

// ReloadFromDisk re-reads the config file, validates it, then rebuilds the runtime.
func (r *Manager) ReloadFromDisk() error {
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

// UpdatePasswordHash writes a new bcrypt password hash to the auth section of config.yaml.
func (r *Manager) UpdatePasswordHash(hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	rawYAML, err := os.ReadFile(r.configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	var root map[string]interface{}
	if err := yaml.Unmarshal(rawYAML, &root); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if root == nil {
		root = make(map[string]interface{})
	}

	authMap, _ := root["auth"].(map[string]interface{})
	if authMap == nil {
		authMap = make(map[string]interface{})
	}
	if _, ok := authMap["username"]; !ok {
		authMap["username"] = "admin"
	}
	authMap["password_hash"] = hash
	root["auth"] = authMap

	updated, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	dir := filepath.Dir(r.configPath)
	tmpFile, err := os.CreateTemp(dir, "config.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, werr := tmpFile.Write(updated); werr != nil {
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

	r.activeConfigYAML = updated
	return nil
}

// rebuild performs an atomic runtime swap. The caller must hold r.mu.
func (r *Manager) rebuild(cfg *config.Config, yamlBytes []byte) error {
	if cancel := r.runtimeCancel; cancel != nil {
		cancel()
		r.runtimeCancel = nil
	}
	for _, srv := range r.servers {
		srv.Shutdown()
	}
	r.servers = nil

	r.wg.Wait()

	r.state.SetStarting()

	store, err := config.BuildMemStore(cfg)
	if err != nil {
		werr := fmt.Errorf("memory store build: %w", err)
		r.state.SetError(werr)
		return werr
	}
	units, err := puller.Build(cfg, store)
	if err != nil {
		werr := fmt.Errorf("engine build: %w", err)
		r.state.SetError(werr)
		return werr
	}

	healthStore := memory.NewBlockHealthStore()
	latencyTracker := NewPollLatencyTracker()

	runtimeCtx, runtimeCancel := context.WithCancel(r.processCtx)

	authority := memory.BuildAuthorityRegistry(cfg, healthStore)

	seenPorts := make(map[uint16]struct{})
	for _, u := range cfg.Replicator.Units {
		seenPorts[u.Target.Port] = struct{}{}
	}

	type boundPort struct {
		port uint16
		ln   net.Listener
	}

	var (
		bound            []boundPort
		listenerStatuses []ListenerStatus
	)

	for port := range seenPorts {
		addr := fmt.Sprintf(":%d", port)

		ln, bindErr := net.Listen("tcp", addr)
		if bindErr != nil {
			for _, b := range bound {
				_ = b.ln.Close()
			}
			runtimeCancel()

			werr := fmt.Errorf("adapter (%s) failed to bind: %w", addr, bindErr)
			listenerStatuses = append(listenerStatuses, ListenerStatus{
				Port:   port,
				Status: "error",
				Error:  werr.Error(),
			})
			r.listenerStatuses = listenerStatuses
			r.state.SetError(werr)
			return werr
		}

		bound = append(bound, boundPort{port: port, ln: ln})
		listenerStatuses = append(listenerStatuses, ListenerStatus{
			Port:   port,
			Status: "listening",
			Error:  "",
		})
	}

	var newServers []*memory.Server
	for _, b := range bound {
		addr := fmt.Sprintf(":%d", b.port)

		srv := memory.NewServerWithListener(addr, b.ln, store, authority, cfg.Debug.AdapterRouting)

		newServers = append(newServers, srv)
		r.wg.Add(1)
		go func(s *memory.Server) {
			defer r.wg.Done()
			if err := s.Serve(); err != nil {
				log.Printf("aegis: adapter (%s) exited: %v", s.Addr(), err)
			}
		}(srv)
	}

	for _, u := range units {
		out := make(chan memory.PollResult, 8)

		r.wg.Add(2)
		go func(id string, cs counterSource, pw pollWriter, hw blockHealthWriter, ch <-chan memory.PollResult) {
			defer r.wg.Done()
			runOrchestrator(runtimeCtx, id, cs, pw, hw, ch, latencyTracker)
		}(u.Poller.UnitID(), u.Poller, u.Writer, healthStore, out)

		go func(p *puller.Poller, ch chan<- memory.PollResult) {
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

// buildStatusUnitIndex constructs a map from (port, statusUnitID, statusSlot) → unit ID.
func buildStatusUnitIndex(cfg *config.Config) map[statusUnitKey]string {
	idx := make(map[statusUnitKey]string, len(cfg.Replicator.Units))
	for _, u := range cfg.Replicator.Units {
		tgt := u.Target
		if tgt.StatusUnitID == nil || tgt.StatusSlot == nil {
			continue
		}
		k := statusUnitKey{
			port:         tgt.Port,
			statusUnitID: *tgt.StatusUnitID,
			statusSlot:   *tgt.StatusSlot,
		}
		idx[k] = u.ID
	}
	return idx
}

// ---- Helpers ----

func healthCodeToString(code uint16) string {
	switch code {
	case memory.HealthOK:
		return "OK"
	case memory.HealthError:
		return "ERROR"
	case memory.HealthStale:
		return "STALE"
	case memory.HealthDisabled:
		return "DISABLED"
	default:
		return "UNKNOWN"
	}
}

func unpackBitsToUint16(packed []byte, count uint16) []uint16 {
	out := make([]uint16, count)
	for i := uint16(0); i < count; i++ {
		byteIdx := i / 8
		bitIdx := i % 8
		if int(byteIdx) < len(packed) && (packed[byteIdx]>>bitIdx)&1 == 1 {
			out[i] = 1
		}
	}
	return out
}

func bytesToUint16s(raw []byte, count uint16) []uint16 {
	out := make([]uint16, count)
	for i := uint16(0); i < count; i++ {
		out[i] = uint16(raw[i*2])<<8 | uint16(raw[i*2+1])
	}
	return out
}
