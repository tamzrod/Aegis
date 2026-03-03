// internal/engine/writer.go
package engine

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"

	"github.com/tamzrod/Aegis/internal/core"
)

// WritePlan is the fully-built write plan for one polling unit.
// It contains the in-process store targets — no network endpoints.
type WritePlan struct {
	UnitID  string
	Targets []TargetMemory

	// Optional device status target (nil = status disabled)
	Status *StatusTarget
}

// TargetMemory is one in-process store target.
type TargetMemory struct {
	MemoryID core.MemoryID  // (Port, UnitID) key in the store
	Offsets  map[int]uint16 // per-FC address delta; missing FC defaults to 0
}

// StatusTarget describes where to write device status in the store.
type StatusTarget struct {
	MemoryID   core.MemoryID
	BaseSlot   uint16
	DeviceName string
}

// StoreWriter writes PollResult snapshots directly into the shared in-process Store.
//
// Architectural rule (LOCKED):
//   - There is NO TCP or network connection between the engine and the store.
//   - Writes are in-process function calls to core.Memory.
type StoreWriter struct {
	plan  WritePlan
	store core.Store
}

// NewStoreWriter creates a StoreWriter for the given plan and store.
func NewStoreWriter(plan WritePlan, store core.Store) *StoreWriter {
	return &StoreWriter{plan: plan, store: store}
}

// Write commits a successful PollResult into the store.
// If res.Err is non-nil, data writes are skipped (poll failed — do not write stale data).
func (w *StoreWriter) Write(res PollResult) error {
	if res.Err != nil {
		return nil
	}

	var errs []string

	for _, tgt := range w.plan.Targets {
		mem, err := w.store.MustGet(tgt.MemoryID)
		if err != nil {
			errs = append(errs, fmt.Sprintf(
				"writer: memory not found (port=%d unit_id=%d): %v",
				tgt.MemoryID.Port, tgt.MemoryID.UnitID, err,
			))
			continue
		}

		for _, b := range res.Blocks {
			dstAddr := offsetForFC(tgt.Offsets, b.FC) + b.Address

			switch b.FC {
			case 1:
				packed := packBits(b.Bits)
				if err := mem.WriteBits(core.AreaCoils, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: coils write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}

			case 2:
				packed := packBits(b.Bits)
				if err := mem.WriteBits(core.AreaDiscreteInputs, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: discrete_inputs write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}

			case 3:
				packed := packRegisters(b.Registers)
				if err := mem.WriteRegs(core.AreaHoldingRegs, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: holding_regs write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}

			case 4:
				packed := packRegisters(b.Registers)
				if err := mem.WriteRegs(core.AreaInputRegs, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: input_regs write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}
			}
		}
	}

	if len(errs) > 0 {
		return errors.New(strings.Join(errs, " | "))
	}

	return nil
}

// WriteStatus writes the device status block directly into the store.
// baseAddr = baseSlot * StatusSlotsPerDevice
func (w *StoreWriter) WriteStatus(snap StatusSnapshot) error {
	if w.plan.Status == nil {
		return nil
	}

	mem, err := w.store.MustGet(w.plan.Status.MemoryID)
	if err != nil {
		return fmt.Errorf(
			"status writer: memory not found (port=%d unit_id=%d): %w",
			w.plan.Status.MemoryID.Port, w.plan.Status.MemoryID.UnitID, err,
		)
	}

	baseAddr := w.plan.Status.BaseSlot * StatusSlotsPerDevice
	regs := encodeStatusBlock(snap, w.plan.Status.DeviceName)

	src := make([]byte, len(regs)*2)
	for i, v := range regs {
		binary.BigEndian.PutUint16(src[i*2:i*2+2], v)
	}

	if err := mem.WriteRegs(core.AreaHoldingRegs, baseAddr, uint16(len(regs)), src); err != nil {
		return fmt.Errorf("status writer: write failed: %w", err)
	}

	return nil
}

// --------------------
// Helpers
// --------------------

func offsetForFC(offsets map[int]uint16, fc uint8) uint16 {
	if v, ok := offsets[int(fc)]; ok {
		return v
	}
	return 0
}

// packBits converts a []bool into LSB-first packed bytes (Modbus bit layout).
func packBits(bits []bool) []byte {
	n := (len(bits) + 7) / 8
	out := make([]byte, n)
	for i, v := range bits {
		if v {
			out[i/8] |= 1 << uint(i%8)
		}
	}
	return out
}

// packRegisters converts []uint16 into big-endian byte pairs.
func packRegisters(regs []uint16) []byte {
	out := make([]byte, len(regs)*2)
	for i, r := range regs {
		binary.BigEndian.PutUint16(out[i*2:i*2+2], r)
	}
	return out
}
