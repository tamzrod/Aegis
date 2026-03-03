// internal/config/validate.go
package config

import (
	"fmt"
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

	if err := validateReplicator(cfg); err != nil {
		return err
	}

	return nil
}

// --------------------
// Replicator validation
// --------------------

func validateReplicator(cfg *Config) error {
	if len(cfg.Replicator.Units) == 0 {
		return fmt.Errorf("replicator.units: at least one unit required")
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

		if err := validateUnitConfig(u); err != nil {
			return fmt.Errorf("replicator.units[%d] (%s): %w", i, u.ID, err)
		}
	}

	// Detect replicator write conflicts: overlapping read ranges for same (port, unit_id, FC).
	if err := validateReplicatorWriteConflicts(cfg); err != nil {
		return err
	}

	// Detect status slot conflicts and status_unit_id vs data unit_id collisions.
	if err := validateStatusSlots(cfg); err != nil {
		return err
	}

	return nil
}

func validateUnitConfig(u UnitConfig) error {
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
	target := u.Target
	if target.Port == 0 {
		return fmt.Errorf("target.port must be > 0")
	}
	if target.UnitID == 0 {
		return fmt.Errorf("target.unit_id must be > 0")
	}
	if target.UnitID > 0xFF {
		return fmt.Errorf("target.unit_id must be <= 255")
	}

	// Status block validation
	if target.StatusSlot != nil {
		if target.StatusUnitID == nil {
			return fmt.Errorf("target.status_slot is set but target.status_unit_id is not")
		}
		if *target.StatusUnitID == 0 {
			return fmt.Errorf("target.status_unit_id must be > 0")
		}
		if *target.StatusUnitID > 0xFF {
			return fmt.Errorf("target.status_unit_id must be <= 255")
		}
	}

	// Target mode validation
	switch target.Mode {
	case TargetModeA, TargetModeB, TargetModeC:
		// valid
	default:
		return fmt.Errorf("target.mode: unknown value %q (valid: A, B, C)", target.Mode)
	}

	return nil
}

// validateReplicatorWriteConflicts detects when multiple replicator units target
// the same (port, unit_id) and issue reads for the same FC with overlapping
// address ranges.  Such configurations produce non-deterministic write outcomes.
func validateReplicatorWriteConflicts(cfg *Config) error {
	type writeTarget struct {
		port   uint16
		unitID uint16
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
		wt := writeTarget{port: u.Target.Port, unitID: u.Target.UnitID}
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
							"addresses [%d,%d) and [%d,%d) to target (port=%d, unit_id=%d)",
						a.unitID, b.unitID,
						a.fc, a.start, a.end, b.start, b.end,
						wt.port, wt.unitID,
					)
				}
			}
		}
	}
	return nil
}

// validateStatusSlots ensures:
//   - No two replicator units on the same (port, status_unit_id) share the same status_slot.
//   - No status_unit_id equals a data unit_id on the same port.
func validateStatusSlots(cfg *Config) error {
	// Collect all data unit IDs per port.
	dataUnitsByPort := make(map[uint16]map[uint16]struct{})
	for _, u := range cfg.Replicator.Units {
		port := u.Target.Port
		if dataUnitsByPort[port] == nil {
			dataUnitsByPort[port] = make(map[uint16]struct{})
		}
		dataUnitsByPort[port][u.Target.UnitID] = struct{}{}
	}

	// slotKey identifies a status slot namespace: (port, status_unit_id).
	type slotKey struct {
		port         uint16
		statusUnitID uint16
	}
	// seenSlots maps (port, status_unit_id) → (slot → first unit ID that claimed it).
	seenSlots := make(map[slotKey]map[uint16]string)

	for i, u := range cfg.Replicator.Units {
		if u.Target.StatusSlot == nil {
			continue
		}
		if u.Target.StatusUnitID == nil {
			continue // already caught by per-unit validation
		}

		port := u.Target.Port
		statusUID := *u.Target.StatusUnitID
		slot := *u.Target.StatusSlot

		// status_unit_id must not equal any data unit_id on the same port.
		if dataUnits, ok := dataUnitsByPort[port]; ok {
			if _, conflict := dataUnits[statusUID]; conflict {
				return fmt.Errorf(
					"replicator.units[%d] (%s): target.status_unit_id %d conflicts with "+
						"a data unit_id on port %d",
					i, u.ID, statusUID, port,
				)
			}
		}

		sk := slotKey{port: port, statusUnitID: statusUID}
		if seenSlots[sk] == nil {
			seenSlots[sk] = make(map[uint16]string)
		}
		if prev, exists := seenSlots[sk][slot]; exists {
			return fmt.Errorf(
				"replicator.units[%d] (%s): status_slot %d already used by unit %q "+
					"on (port=%d, status_unit_id=%d)",
				i, u.ID, slot, prev, port, statusUID,
			)
		}
		seenSlots[sk][slot] = u.ID
	}
	return nil
}
