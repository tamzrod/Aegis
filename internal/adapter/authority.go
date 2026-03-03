// internal/adapter/authority.go
package adapter

import (
	"encoding/binary"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/core"
)

// statusBlockSize is the number of holding registers per device status block.
// This mirrors engine.StatusSlotsPerDevice (= 30) and is protocol-locked.
const statusBlockSize = 30

// statusHealthOffset is the register offset within a status block where the
// health code is stored. Mirrors engine.slotHealthCode (= 2).
const statusHealthOffset uint16 = 2

// healthOK is the health code indicating a healthy upstream device.
// Mirrors engine.HealthOK (= 1).
const healthOK uint16 = 1

// HealthChecker reports whether a given (port, unitID) memory's upstream is healthy.
type HealthChecker interface {
	IsHealthyFor(port uint16, unitID uint16) bool
}

// statusEntry locates a single status block in the store for one data memory unit.
type statusEntry struct {
	statusPort   uint16
	statusUnitID uint16
	baseAddr     uint16 // = slot * statusBlockSize
}

type dataKey struct {
	port   uint16
	unitID uint16
}

// StoreHealthChecker reads health codes directly from the status plane in the store.
// It is constructed from config by BuildHealthChecker.
type StoreHealthChecker struct {
	store   core.Store
	entries map[dataKey][]statusEntry
}

// IsHealthyFor returns true if all configured status blocks for (port, unitID)
// report health == OK. Returns true if no status block is configured for the unit.
func (c *StoreHealthChecker) IsHealthyFor(port uint16, unitID uint16) bool {
	entries := c.entries[dataKey{port: port, unitID: unitID}]
	if len(entries) == 0 {
		return true
	}
	for _, e := range entries {
		statusID := core.MemoryID{Port: e.statusPort, UnitID: e.statusUnitID}
		mem, ok := c.store.Get(statusID)
		if !ok {
			// Status memory not present — skip check for this entry.
			continue
		}
		buf := make([]byte, 2)
		if err := mem.ReadRegs(core.AreaHoldingRegs, e.baseAddr+statusHealthOffset, 1, buf); err != nil {
			// Cannot read health register — skip check for this entry.
			continue
		}
		if binary.BigEndian.Uint16(buf) != healthOK {
			return false
		}
	}
	return true
}

// BuildHealthChecker constructs a HealthChecker from the validated config and an
// already-built store.  Assumes Validate() has already passed.
func BuildHealthChecker(cfg *config.Config, store core.Store) HealthChecker {
	entries := make(map[dataKey][]statusEntry)

	listenerPort := make(map[string]uint16)
	for _, l := range cfg.Server.Listeners {
		port, err := config.ParseListenPort(l.Listen)
		if err != nil {
			continue
		}
		listenerPort[l.ID] = port
	}

	for _, u := range cfg.Replicator.Units {
		if u.Source.StatusSlot == nil || u.Target.StatusUnitID == nil {
			continue
		}
		port, ok := listenerPort[u.Target.ListenerID]
		if !ok {
			continue
		}
		dk := dataKey{port: port, unitID: u.Target.UnitID}
		e := statusEntry{
			statusPort:   port,
			statusUnitID: *u.Target.StatusUnitID,
			baseAddr:     *u.Source.StatusSlot * statusBlockSize,
		}
		entries[dk] = append(entries[dk], e)
	}

	return &StoreHealthChecker{store: store, entries: entries}
}

// isWriteFC returns true for write function codes (FC 5, 6, 15, 16).
func isWriteFC(fc uint8) bool {
	return fc == 5 || fc == 6 || fc == 15 || fc == 16
}

// isReadFC returns true for read function codes (FC 1, 2, 3, 4).
func isReadFC(fc uint8) bool {
	return fc >= 1 && fc <= 4
}

// enforceAuthority checks the authority mode for the given request and returns an
// exception PDU if the request must be rejected, or (nil, false) if it may proceed.
//
//   - Write FCs (5, 6, 15, 16): rejected with 0x01 unless mode == "standalone".
//   - Read FCs (1, 2, 3, 4):    rejected with 0x0B in "strict" mode when health != OK.
func enforceAuthority(mode string, fc uint8, port uint16, unitID uint16, health HealthChecker) ([]byte, bool) {
	if isWriteFC(fc) {
		if mode != config.AuthorityModeStandalone {
			return BuildExceptionPDU(fc, 0x01), true
		}
		return nil, false
	}
	if isReadFC(fc) && mode == config.AuthorityModeStrict {
		if !health.IsHealthyFor(port, unitID) {
			return BuildExceptionPDU(fc, 0x0B), true
		}
	}
	return nil, false
}
