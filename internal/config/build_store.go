// internal/config/build_store.go
package config

import (
	"fmt"

	"github.com/tamzrod/Aegis/internal/core"
)

// BuildMemStore derives a core.MemStore from the validated replicator configuration.
//
// Data memory surfaces are derived from replicator reads:
//   - For each (port, unit_id): compute the bounding range per FC
//     (min address, max address+quantity) and allocate one AreaLayout per FC.
//
// Status memory surfaces are derived from status slot configuration:
//   - For each (port, status_unit_id): size = (max(status_slot) + 1) * 30 holding registers.
//
// Assumes Validate() has already passed.
func BuildMemStore(cfg *Config) (*core.MemStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	store := core.NewMemStore()

	// --- Data memory surfaces ---

	type surfaceKey struct {
		port   uint16
		unitID uint16
	}
	type fcBounds struct {
		minStart uint16
		maxEnd   uint16 // exclusive
		set      bool
	}

	surfaces := make(map[surfaceKey]map[uint8]fcBounds)

	for _, u := range cfg.Replicator.Units {
		sk := surfaceKey{port: u.Target.Port, unitID: u.Target.UnitID}
		if surfaces[sk] == nil {
			surfaces[sk] = make(map[uint8]fcBounds)
		}
		for _, r := range u.Reads {
			b := surfaces[sk][r.FC]
			end := r.Address + r.Quantity
			if !b.set {
				b = fcBounds{minStart: r.Address, maxEnd: end, set: true}
			} else {
				if r.Address < b.minStart {
					b.minStart = r.Address
				}
				if end > b.maxEnd {
					b.maxEnd = end
				}
			}
			surfaces[sk][r.FC] = b
		}
	}

	for sk, fcMap := range surfaces {
		layouts := core.MemoryLayouts{}
		for fc, bounds := range fcMap {
			size := bounds.maxEnd - bounds.minStart
			layout := &core.AreaLayout{Start: bounds.minStart, Size: size}
			switch fc {
			case 1:
				layouts.Coils = layout
			case 2:
				layouts.DiscreteInputs = layout
			case 3:
				layouts.HoldingRegs = layout
			case 4:
				layouts.InputRegs = layout
			}
		}

		mem, err := core.NewMemory(layouts)
		if err != nil {
			return nil, fmt.Errorf(
				"data memory build failed (port=%d unit_id=%d): %w",
				sk.port, sk.unitID, err,
			)
		}
		id := core.MemoryID{Port: sk.port, UnitID: sk.unitID}
		if err := store.Add(id, mem); err != nil {
			return nil, fmt.Errorf(
				"data memory register failed (port=%d unit_id=%d): %w",
				sk.port, sk.unitID, err,
			)
		}
	}

	// --- Status memory surfaces ---
	// For each (port, status_unit_id): allocate (max_slot + 1) * 30 holding registers.

	type statusKey struct {
		port         uint16
		statusUnitID uint16
	}
	statusMax := make(map[statusKey]uint16) // tracks max slot per (port, status_unit_id)
	statusSeen := make(map[statusKey]bool)

	for _, u := range cfg.Replicator.Units {
		if u.Target.StatusSlot == nil || u.Target.StatusUnitID == nil {
			continue
		}
		sk := statusKey{port: u.Target.Port, statusUnitID: *u.Target.StatusUnitID}
		slot := *u.Target.StatusSlot
		if !statusSeen[sk] || slot > statusMax[sk] {
			statusMax[sk] = slot
			statusSeen[sk] = true
		}
	}

	for sk, maxSlot := range statusMax {
		size := (uint32(maxSlot) + 1) * 30
		if size > 0xFFFF {
			return nil, fmt.Errorf(
				"status memory overflow (port=%d unit_id=%d): max status_slot=%d requires %d registers, exceeds uint16 range",
				sk.port, sk.statusUnitID, maxSlot, size,
			)
		}
		mem, err := core.NewMemory(core.MemoryLayouts{
			HoldingRegs: &core.AreaLayout{Start: 0, Size: uint16(size)},
		})
		if err != nil {
			return nil, fmt.Errorf(
				"status memory build failed (port=%d unit_id=%d): %w",
				sk.port, sk.statusUnitID, err,
			)
		}
		id := core.MemoryID{Port: sk.port, UnitID: sk.statusUnitID}
		if err := store.Add(id, mem); err != nil {
			return nil, fmt.Errorf(
				"status memory register failed (port=%d unit_id=%d): %w",
				sk.port, sk.statusUnitID, err,
			)
		}
	}

	return store, nil
}
