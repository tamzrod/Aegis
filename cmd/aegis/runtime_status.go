// cmd/aegis/runtime_status.go — observability domain
// Responsibility: reading and decoding runtime status for the WebUI.
// DeviceStatuses provides a summary health view per replicator unit.
// ReadDeviceStatus decodes a full status register block from memory.
// ReadViewerRegisters exposes raw register values to the data viewer.
// Helper functions (healthCodeToString, deriveDeviceStatus, isDevicePolling,
// unpackBitsToUint16, bytesToUint16s) are co-located here as they serve
// only the status-reading concern.
package main

import (
	"fmt"
	"time"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/core"
	"github.com/tamzrod/Aegis/internal/engine"
	runtimepkg "github.com/tamzrod/Aegis/internal/runtime"
)

// statusUnitKey uniquely identifies a device status block by its Modbus addressing tuple.
type statusUnitKey struct {
	port         uint16
	statusUnitID uint16
	statusSlot   uint16
}

// buildStatusUnitIndex constructs a map from (port, statusUnitID, statusSlot) → unit ID
// so that ReadDeviceStatus can resolve the unit ID in O(1) without iterating all units.
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

// DeviceStatuses returns the per-device operational status derived from the
// block health store and current runtime state. If the runtime is not running,
// all configured devices are reported as "offline".
func (r *RuntimeManager) DeviceStatuses() []runtimepkg.DeviceStatus {
	r.mu.Lock()
	running := r.state.Status().Running
	hs := r.healthStore
	cfg := r.activeCfg
	r.mu.Unlock()

	if cfg == nil {
		return nil
	}

	now := time.Now()
	out := make([]runtimepkg.DeviceStatus, 0, len(cfg.Replicator.Units))
	for _, u := range cfg.Replicator.Units {
		status := "offline"
		polling := false
		if running && hs != nil {
			status = deriveDeviceStatus(hs, u)
			polling = isDevicePolling(hs, u, now)
		}
		out = append(out, runtimepkg.DeviceStatus{ID: u.ID, Status: status, Polling: polling})
	}
	return out
}

// ReadDeviceStatus reads and decodes the status register block for the device
// identified by (port, statusUnitID, statusSlot) from the in-process store.
// Returns an error if the store is not initialised or the memory is not found.
func (r *RuntimeManager) ReadDeviceStatus(port, statusUnitID, statusSlot uint16) (*runtimepkg.StatusBlockSnapshot, error) {
	r.mu.Lock()
	st := r.store
	lt := r.latencyTracker
	unitID := r.statusUnitIndex[statusUnitKey{port, statusUnitID, statusSlot}]
	r.mu.Unlock()

	if st == nil {
		return nil, fmt.Errorf("runtime store not available")
	}

	mem, err := st.MustGet(core.MemoryID{Port: port, UnitID: statusUnitID})
	if err != nil {
		return nil, fmt.Errorf("status memory not found (port=%d unit_id=%d): %w", port, statusUnitID, err)
	}

	baseAddr := statusSlot * engine.StatusSlotsPerDevice
	rawBytes := make([]byte, int(engine.StatusSlotsPerDevice)*2)
	if err := mem.ReadRegs(core.AreaHoldingRegs, baseAddr, engine.StatusSlotsPerDevice, rawBytes); err != nil {
		return nil, fmt.Errorf("status read failed (port=%d unit_id=%d slot=%d): %w", port, statusUnitID, statusSlot, err)
	}

	// Convert big-endian byte pairs to uint16 slice.
	regs := make([]uint16, engine.StatusSlotsPerDevice)
	for i := range regs {
		regs[i] = uint16(rawBytes[i*2])<<8 | uint16(rawBytes[i*2+1])
	}

	snap := engine.DecodeStatusBlock(regs)

	healthStr := healthCodeToString(snap.Health)
	online := snap.Health == engine.HealthOK

	var lastMs, avgMs, maxMs uint32
	if lt != nil && unitID != "" {
		lastMs, avgMs, maxMs = lt.Get(unitID)
	}

	return &runtimepkg.StatusBlockSnapshot{
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

// ReadViewerRegisters reads raw register or coil values from the in-process store
// for the device identified by deviceKey. It supports FC1 (coils), FC2 (discrete inputs),
// FC3 (holding registers), and FC4 (input registers).
// The memory is looked up by the device's target (port, unit_id).
func (r *RuntimeManager) ReadViewerRegisters(deviceKey string, fc uint8, address, quantity uint16) ([]uint16, error) {
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

	// Find the unit with the matching ID.
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

	mem, err := st.MustGet(core.MemoryID{Port: targetPort, UnitID: targetUnitID})
	if err != nil {
		return nil, fmt.Errorf("memory not found (port=%d unit_id=%d): %w", targetPort, targetUnitID, err)
	}

	switch fc {
	case 1: // Read Coils
		packed := make([]byte, (int(quantity)+7)/8)
		if err := mem.ReadBits(core.AreaCoils, address, quantity, packed); err != nil {
			return nil, fmt.Errorf("FC1 read failed: %w", err)
		}
		return unpackBitsToUint16(packed, quantity), nil

	case 2: // Read Discrete Inputs
		packed := make([]byte, (int(quantity)+7)/8)
		if err := mem.ReadBits(core.AreaDiscreteInputs, address, quantity, packed); err != nil {
			return nil, fmt.Errorf("FC2 read failed: %w", err)
		}
		return unpackBitsToUint16(packed, quantity), nil

	case 3: // Read Holding Registers
		raw := make([]byte, int(quantity)*2)
		if err := mem.ReadRegs(core.AreaHoldingRegs, address, quantity, raw); err != nil {
			return nil, fmt.Errorf("FC3 read failed: %w", err)
		}
		return bytesToUint16s(raw, quantity), nil

	case 4: // Read Input Registers
		raw := make([]byte, int(quantity)*2)
		if err := mem.ReadRegs(core.AreaInputRegs, address, quantity, raw); err != nil {
			return nil, fmt.Errorf("FC4 read failed: %w", err)
		}
		return bytesToUint16s(raw, quantity), nil

	default:
		return nil, fmt.Errorf("unsupported function code %d", fc)
	}
}

// unpackBitsToUint16 converts packed bit bytes (Modbus coil format) to a slice of
// uint16 values where each element is 0 or 1 for the corresponding coil.
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

// bytesToUint16s converts a big-endian byte slice to a slice of uint16 register values.
func bytesToUint16s(raw []byte, count uint16) []uint16 {
	out := make([]uint16, count)
	for i := uint16(0); i < count; i++ {
		out[i] = uint16(raw[i*2])<<8 | uint16(raw[i*2+1])
	}
	return out
}

// healthCodeToString converts a health uint16 code to a human-readable string.
func healthCodeToString(code uint16) string {
	switch code {
	case engine.HealthOK:
		return "OK"
	case engine.HealthError:
		return "ERROR"
	case engine.HealthStale:
		return "STALE"
	case engine.HealthDisabled:
		return "DISABLED"
	default:
		return "UNKNOWN"
	}
}

// deriveDeviceStatus computes a single status string for a replicator unit
// based on the aggregate health of its read blocks.
func deriveDeviceStatus(hs *engine.BlockHealthStore, u config.UnitConfig) string {
	anyFound := false
	anyError := false
	for idx := range u.Reads {
		_, consecutiveErrors, _, found := hs.GetBlockHealth(u.ID, idx)
		if found {
			anyFound = true
			if consecutiveErrors > 0 {
				anyError = true
			}
		}
	}
	if !anyFound {
		return "warning" // not yet polled
	}
	if anyError {
		return "error"
	}
	return "online"
}

// activePollingThreshold is the window within which a successful poll is
// considered "recent" for the purposes of the polling activity indicator.
const activePollingThreshold = 10 * time.Second

// isDevicePolling returns true if any read block for the unit had a successful
// poll within activePollingThreshold, indicating active polling activity.
func isDevicePolling(hs *engine.BlockHealthStore, u config.UnitConfig, now time.Time) bool {
	threshold := now.Add(-activePollingThreshold)
	for idx := range u.Reads {
		if t, ok := hs.GetLastSuccess(u.ID, idx); ok && t.After(threshold) {
			return true
		}
	}
	return false
}
