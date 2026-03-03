// internal/config/validate.go
package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Validate performs structural validation on the configuration.
// It enforces bounds and consistency only.
// It does NOT mutate the configuration.
// If validation fails, the process must exit immediately.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if err := validateServer(cfg); err != nil {
		return err
	}

	if err := validateReplicator(cfg); err != nil {
		return err
	}

	return nil
}

// --------------------
// Server validation
// --------------------

func validateServer(cfg *Config) error {
	if len(cfg.Server.Listeners) == 0 {
		return fmt.Errorf("server.listeners: at least one listener required")
	}

	seenIDs := make(map[string]struct{})
	seenAddrs := make(map[string]struct{})

	// Track all (port, unit_id) pairs to detect duplicates
	type memKey struct {
		port   uint16
		unitID uint16
	}
	seenMem := make(map[memKey]string)

	for i, l := range cfg.Server.Listeners {
		if l.ID == "" {
			return fmt.Errorf("server.listeners[%d]: id is required", i)
		}
		if _, ok := seenIDs[l.ID]; ok {
			return fmt.Errorf("server.listeners[%d]: duplicate id %q", i, l.ID)
		}
		seenIDs[l.ID] = struct{}{}

		if strings.TrimSpace(l.Listen) == "" {
			return fmt.Errorf("server.listeners[%d] (%s): listen is required", i, l.ID)
		}
		if _, ok := seenAddrs[l.Listen]; ok {
			return fmt.Errorf("server.listeners[%d] (%s): duplicate listen address %q", i, l.ID, l.Listen)
		}
		seenAddrs[l.Listen] = struct{}{}

		port, err := parseListenPort(l.Listen)
		if err != nil {
			return fmt.Errorf("server.listeners[%d] (%s): invalid listen %q: %w", i, l.ID, l.Listen, err)
		}

		if len(l.Memory) == 0 {
			return fmt.Errorf("server.listeners[%d] (%s): at least one memory block required", i, l.ID)
		}

		for j, m := range l.Memory {
			path := fmt.Sprintf("server.listeners[%d] (%s).memory[%d]", i, l.ID, j)

			if m.UnitID == 0 {
				return fmt.Errorf("%s: unit_id must be > 0", path)
			}
			if m.UnitID > 0xFF {
				return fmt.Errorf("%s: unit_id must be <= 255", path)
			}

			if err := validateAreaDef(path, "coils", m.Coils); err != nil {
				return err
			}
			if err := validateAreaDef(path, "discrete_inputs", m.DiscreteInputs); err != nil {
				return err
			}
			if err := validateAreaDef(path, "holding_registers", m.HoldingRegs); err != nil {
				return err
			}
			if err := validateAreaDef(path, "input_registers", m.InputRegs); err != nil {
				return err
			}

			if err := validateStateSealingDef(path, m); err != nil {
				return err
			}

			mk := memKey{port: port, unitID: m.UnitID}
			if prev, ok := seenMem[mk]; ok {
				return fmt.Errorf(
					"memory identity conflict: (port=%d unit_id=%d) defined in %s and %s",
					port, m.UnitID, prev, path,
				)
			}
			seenMem[mk] = path
		}

		// A: Detect overlapping address ranges per area type within the same listener.
		// Two memory definitions (regardless of unit_id) must not use overlapping ranges
		// for the same area type; this makes overlap explicit even across different units.
		if err := validateMemoryAreaOverlap(l); err != nil {
			return fmt.Errorf("server.listeners[%d] (%s): %w", i, l.ID, err)
		}
	}

	return nil
}

func validateAreaDef(path, name string, a AreaDef) error {
	if a.Count == 0 {
		return nil
	}
	end := uint32(a.Start) + uint32(a.Count)
	if end > 0x10000 {
		return fmt.Errorf(
			"%s.%s: start(%d)+count(%d) exceeds 16-bit address space",
			path, name, a.Start, a.Count,
		)
	}
	return nil
}

func validateStateSealingDef(path string, m MemoryDef) error {
	if m.StateSealing == nil {
		return nil
	}

	area := strings.ToLower(strings.TrimSpace(m.StateSealing.Area))
	if area != "coil" {
		return fmt.Errorf("%s.state_sealing.area must be 'coil'", path)
	}

	if m.Coils.Count == 0 {
		return fmt.Errorf("%s.state_sealing requires coils to be allocated", path)
	}

	start := m.Coils.Start
	count := m.Coils.Count
	addr := m.StateSealing.Address
	endExclusive := uint32(start) + uint32(count)

	if uint32(addr) < uint32(start) || uint32(addr) >= endExclusive {
		return fmt.Errorf(
			"%s.state_sealing.address (%d) out of bounds for coils [%d..%d)",
			path, addr, start, uint16(endExclusive),
		)
	}

	return nil
}

// --------------------
// Replicator validation
// --------------------

// memSizeKey identifies a memory block by listener and unit ID.
// Used to look up allocated memory sizes during cross-unit validation.
type memSizeKey struct {
	listenerID string
	unitID     uint16
}

func validateReplicator(cfg *Config) error {
	// Build a lookup: listenerID → port
	listenerPort := make(map[string]uint16)
	for _, l := range cfg.Server.Listeners {
		port, err := parseListenPort(l.Listen)
		if err != nil {
			// Already validated in validateServer
			continue
		}
		listenerPort[l.ID] = port
	}

	// Build holding-register count lookup per (listenerID, unitID) for status capacity check.
	memHRCount := make(map[memSizeKey]uint16)
	for _, l := range cfg.Server.Listeners {
		for _, m := range l.Memory {
			memHRCount[memSizeKey{listenerID: l.ID, unitID: m.UnitID}] = m.HoldingRegs.Count
		}
	}

	seenIDs := make(map[string]struct{})

	for i, u := range cfg.Replicator.Units {
		if u.ID == "" {
			return fmt.Errorf("replicator.units[%d]: id is required", i)
		}
		if _, ok := seenIDs[u.ID]; ok {
			return fmt.Errorf("replicator.units[%d]: duplicate id %q", i, u.ID)
		}
		seenIDs[u.ID] = struct{}{}

		if err := validateUnitConfig(u, listenerPort); err != nil {
			return fmt.Errorf("replicator.units[%d] (%s): %w", i, u.ID, err)
		}
	}

	// B: Detect replicator write conflicts.
	// Multiple units targeting the same (listener_id, unit_id) must not issue reads
	// for the same FC with overlapping address ranges, as they would produce
	// conflicting writes to the same destination registers.
	if err := validateReplicatorWriteConflicts(cfg); err != nil {
		return err
	}

	// C: Detect status slot conflicts and validate slot capacity.
	if err := validateStatusSlots(cfg, memHRCount); err != nil {
		return err
	}

	return nil
}

func validateUnitConfig(u UnitConfig, listenerPort map[string]uint16) error {
	// Source
	if strings.TrimSpace(u.Source.Endpoint) == "" {
		return fmt.Errorf("source.endpoint is required")
	}
	if u.Source.TimeoutMs <= 0 {
		return fmt.Errorf("source.timeout_ms must be > 0")
	}
	if u.Source.DeviceName != "" {
		for i := 0; i < len(u.Source.DeviceName); i++ {
			if u.Source.DeviceName[i] > 0x7F {
				return fmt.Errorf("source.device_name must contain ASCII characters only")
			}
		}
		if len(u.Source.DeviceName) > 16 {
			return fmt.Errorf("source.device_name must be <= 16 characters")
		}
	}

	// Reads
	if len(u.Reads) == 0 {
		return fmt.Errorf("reads: at least one read block required")
	}
	for j, r := range u.Reads {
		if r.FC < 1 || r.FC > 4 {
			return fmt.Errorf("reads[%d]: fc must be 1, 2, 3, or 4", j)
		}
		if r.Quantity == 0 {
			return fmt.Errorf("reads[%d]: quantity must be > 0", j)
		}
		if r.IntervalMs <= 0 {
			return fmt.Errorf("reads[%d]: interval_ms must be > 0", j)
		}
	}

	// Target
	t := u.Target
	if strings.TrimSpace(t.ListenerID) == "" {
		return fmt.Errorf("target.listener_id is required")
	}
	if _, ok := listenerPort[t.ListenerID]; !ok {
		return fmt.Errorf("target.listener_id %q: no matching listener", t.ListenerID)
	}
	if t.UnitID == 0 {
		return fmt.Errorf("target.unit_id must be > 0")
	}
	if t.UnitID > 0xFF {
		return fmt.Errorf("target.unit_id must be <= 255")
	}

	// Status block validation
	if u.Source.StatusSlot != nil {
		if t.StatusUnitID == nil {
			return fmt.Errorf("source.status_slot is set but target.status_unit_id is not")
		}
		if *t.StatusUnitID == 0 {
			return fmt.Errorf("target.status_unit_id must be > 0")
		}
		if *t.StatusUnitID > 0xFF {
			return fmt.Errorf("target.status_unit_id must be <= 255")
		}
	}

	return nil
}

// validateMemoryAreaOverlap checks that no two memory definitions with the same
// unit_id under the same listener have overlapping address ranges for the same
// area type.  Since (port, unit_id) uniqueness is enforced separately, this check
// is future-proof for configs that declare ranges in separate blocks per unit.
func validateMemoryAreaOverlap(l ListenerConfig) error {
	type areaEntry struct {
		unitID uint16
		start  uint16
		end    uint16 // exclusive
		path   string
	}

	// collect compares all entries for a given area type, restricting overlap
	// detection to entries that share the same unit_id.
	collect := func(areaName string, defs []areaEntry) error {
		for i := 0; i < len(defs); i++ {
			for j := i + 1; j < len(defs); j++ {
				a, b := defs[i], defs[j]
				if a.unitID != b.unitID {
					// Different Modbus unit IDs represent independent address spaces.
					continue
				}
				if a.start < b.end && b.start < a.end {
					return fmt.Errorf(
						"memory area overlap: unit_id=%d, area=%s: %s [%d,%d) overlaps %s [%d,%d)",
						a.unitID, areaName, a.path, a.start, a.end, b.path, b.start, b.end,
					)
				}
			}
		}
		return nil
	}

	var coils, di, hr, ir []areaEntry
	for j, m := range l.Memory {
		path := fmt.Sprintf("memory[%d] (unit_id=%d)", j, m.UnitID)
		if m.Coils.Count > 0 {
			coils = append(coils, areaEntry{m.UnitID, m.Coils.Start, m.Coils.Start + m.Coils.Count, path})
		}
		if m.DiscreteInputs.Count > 0 {
			di = append(di, areaEntry{m.UnitID, m.DiscreteInputs.Start, m.DiscreteInputs.Start + m.DiscreteInputs.Count, path})
		}
		if m.HoldingRegs.Count > 0 {
			hr = append(hr, areaEntry{m.UnitID, m.HoldingRegs.Start, m.HoldingRegs.Start + m.HoldingRegs.Count, path})
		}
		if m.InputRegs.Count > 0 {
			ir = append(ir, areaEntry{m.UnitID, m.InputRegs.Start, m.InputRegs.Start + m.InputRegs.Count, path})
		}
	}

	for _, check := range []struct {
		name    string
		entries []areaEntry
	}{
		{"coils", coils},
		{"discrete_inputs", di},
		{"holding_registers", hr},
		{"input_registers", ir},
	} {
		if err := collect(check.name, check.entries); err != nil {
			return err
		}
	}
	return nil
}

// validateReplicatorWriteConflicts detects when multiple replicator units target
// the same (listener_id, unit_id) and issue reads for the same FC with overlapping
// address ranges.  Such configurations produce non-deterministic write outcomes.
func validateReplicatorWriteConflicts(cfg *Config) error {
	type writeTarget struct {
		listenerID string
		unitID     uint16
	}
	type readEntry struct {
		fc      uint8
		start   uint16
		end     uint16 // exclusive
		unitIdx int
		unitID  string
	}

	targetReads := make(map[writeTarget][]readEntry)
	for i, u := range cfg.Replicator.Units {
		wt := writeTarget{listenerID: u.Target.ListenerID, unitID: u.Target.UnitID}
		for _, r := range u.Reads {
			targetReads[wt] = append(targetReads[wt], readEntry{
				fc:      r.FC,
				start:   r.Address,
				end:     r.Address + r.Quantity,
				unitIdx: i,
				unitID:  u.ID,
			})
		}
	}

	for wt, entries := range targetReads {
		for i := 0; i < len(entries); i++ {
			for j := i + 1; j < len(entries); j++ {
				a, b := entries[i], entries[j]
				if a.unitIdx == b.unitIdx {
					// Same replicator unit — not a conflict between units.
					continue
				}
				if a.fc != b.fc {
					continue
				}
				if a.start < b.end && b.start < a.end {
					return fmt.Errorf(
						"replicator write conflict: units %q and %q both write FC%d "+
							"addresses [%d,%d) and [%d,%d) to target (listener=%s, unit_id=%d)",
						a.unitID, b.unitID,
						a.fc, a.start, a.end, b.start, b.end,
						wt.listenerID, wt.unitID,
					)
				}
			}
		}
	}
	return nil
}

// validateStatusSlots ensures:
//   - No two replicator units share the same status_slot.
//   - Each status_slot fits within the allocated status memory:
//     status_memory.holding_registers.count >= (slot+1)*30
func validateStatusSlots(cfg *Config, memHRCount map[memSizeKey]uint16) error {
	seenSlots := make(map[uint16]string) // slot → first unit ID that claimed it

	for i, u := range cfg.Replicator.Units {
		if u.Source.StatusSlot == nil {
			continue
		}
		slot := *u.Source.StatusSlot

		if prev, ok := seenSlots[slot]; ok {
			return fmt.Errorf(
				"replicator.units[%d] (%s): status_slot %d already used by unit %q",
				i, u.ID, slot, prev,
			)
		}
		seenSlots[slot] = u.ID

		// Check that the status memory can accommodate this slot.
		// Each slot occupies 30 consecutive holding registers.
		if u.Target.StatusUnitID == nil {
			continue // already caught by per-unit validation
		}
		key := memSizeKey{listenerID: u.Target.ListenerID, unitID: *u.Target.StatusUnitID}
		count := memHRCount[key]
		required := uint32(slot+1) * 30
		if uint32(count) < required {
			return fmt.Errorf(
				"replicator.units[%d] (%s): status_slot %d requires status memory "+
					"holding_registers.count >= %d (got %d)",
				i, u.ID, slot, required, count,
			)
		}
	}
	return nil
}

// --------------------
// Helpers
// --------------------

// parseListenPort parses "host:port" and returns the port as uint16.
func parseListenPort(listen string) (uint16, error) {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("invalid listen address (expected host:port): %w", err)
	}

	n, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port out of range: %d", n)
	}

	return uint16(n), nil
}
