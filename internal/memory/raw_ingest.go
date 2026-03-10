// internal/memory/raw_ingest.go
// Defines result types produced by the polling engine, device status encoding/decoding,
// per-read-block health tracking, and the StoreWriter that commits poll results into
// the in-process memory store.
package memory

import (
	"encoding/binary"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

// ---- Poll result types ----

// BlockResult is the raw result of a single read.
type BlockResult struct {
	FC       uint8
	Address  uint16
	Quantity uint16

	// Exactly one of these is populated depending on FC.
	Bits      []bool   // FC 1, 2
	Registers []uint16 // FC 3, 4
}

// BlockUpdate carries the per-block health outcome for one poll cycle.
// The poller emits one BlockUpdate per due block, whether it succeeded or failed.
type BlockUpdate struct {
	BlockIdx      int  // index in the unit's reads list
	Success       bool
	Timeout       bool
	ExceptionCode byte // non-zero only when Success==false and Timeout==false
}

// PollResult is a snapshot produced by one poll cycle.
// All-or-nothing: if Err is non-nil, Blocks is empty.
// BlockUpdates carries per-block health info regardless of overall success/failure.
type PollResult struct {
	UnitID string
	At     time.Time

	Blocks       []BlockResult
	Err          error
	BlockUpdates []BlockUpdate // per-block health outcomes for due blocks
}

// ---- Status block types and constants ----

// StatusSlotsPerDevice is the fixed number of holding register slots per device status block.
const StatusSlotsPerDevice uint16 = 30

// statusMagic is the fixed 3-byte magic constant written into the header of every
// status block. It is protocol-locked and must not be configurable.
const (
	statusMagicByte0 byte = 0x41 // 'A'
	statusMagicByte1 byte = 0x47 // 'G'
	statusMagicByte2 byte = 0x53 // 'S'
)

// Slot offsets within each device status block.
const (
	slotHeader0 = 0
	slotHeader1 = 1

	slotHealthCode    = 2
	slotLastErrorCode = 3
	slotSecondsInErr  = 4

	slotDeviceNameStart = 5
	slotDeviceNameSlots = 8

	slotRequestsTotalLow         = 20
	slotRequestsTotalHigh        = 21
	slotResponsesValidLow        = 22
	slotResponsesValidHigh       = 23
	slotTimeoutsTotalLow         = 24
	slotTimeoutsTotalHigh        = 25
	slotTransportErrorsTotalLow  = 26
	slotTransportErrorsTotalHigh = 27
	slotConsecutiveFailCurr      = 28
	slotConsecutiveFailMax       = 29
)

// Health codes
const (
	HealthUnknown  uint16 = 0
	HealthOK       uint16 = 1
	HealthError    uint16 = 2
	HealthStale    uint16 = 3
	HealthDisabled uint16 = 4
)

// StatusSnapshot is the current device status state.
// It is constructed by the orchestrator and delivered to the StoreWriter.
type StatusSnapshot struct {
	Health         uint16
	LastErrorCode  uint16
	SecondsInError uint16

	RequestsTotal        uint32
	ResponsesValidTotal  uint32
	TimeoutsTotal        uint32
	TransportErrorsTotal uint32

	ConsecutiveFailCurr uint16
	ConsecutiveFailMax  uint16
}

// encodeStatusBlock encodes a StatusSnapshot into a full device status register block.
// Layout is protocol-locked (30 registers).
func encodeStatusBlock(s StatusSnapshot, deviceName string, blockIndex uint8) []uint16 {
	regs := make([]uint16, StatusSlotsPerDevice)

	regs[slotHeader0] = uint16(statusMagicByte0)<<8 | uint16(statusMagicByte1)
	regs[slotHeader1] = uint16(statusMagicByte2)<<8 | uint16(blockIndex)

	regs[slotHealthCode] = s.Health
	regs[slotLastErrorCode] = s.LastErrorCode
	regs[slotSecondsInErr] = s.SecondsInError

	nameBytes := []byte(deviceName)
	const maxNameChars = 16
	if len(nameBytes) > maxNameChars {
		nameBytes = nameBytes[:maxNameChars]
	}
	for i := range nameBytes {
		if nameBytes[i] < 0x20 || nameBytes[i] > 0x7E {
			nameBytes[i] = '?'
		}
	}
	for i := 0; i < maxNameChars; i += 2 {
		var hi, lo byte
		if i < len(nameBytes) {
			hi = nameBytes[i]
		}
		if i+1 < len(nameBytes) {
			lo = nameBytes[i+1]
		}
		regs[slotDeviceNameStart+i/2] = uint16(hi)<<8 | uint16(lo)
	}

	putU32(regs, slotRequestsTotalLow, s.RequestsTotal)
	putU32(regs, slotResponsesValidLow, s.ResponsesValidTotal)
	putU32(regs, slotTimeoutsTotalLow, s.TimeoutsTotal)
	putU32(regs, slotTransportErrorsTotalLow, s.TransportErrorsTotal)

	regs[slotConsecutiveFailCurr] = s.ConsecutiveFailCurr
	regs[slotConsecutiveFailMax] = s.ConsecutiveFailMax

	return regs
}

func putU32(regs []uint16, start int, v uint32) {
	regs[start] = uint16(v & 0xFFFF)
	regs[start+1] = uint16((v >> 16) & 0xFFFF)
}

func getU32(regs []uint16, start int) uint32 {
	return uint32(regs[start]) | uint32(regs[start+1])<<16
}

// DecodeStatusBlock decodes a raw register slice into a StatusSnapshot.
// regs must contain at least StatusSlotsPerDevice elements.
func DecodeStatusBlock(regs []uint16) StatusSnapshot {
	if len(regs) < int(StatusSlotsPerDevice) {
		return StatusSnapshot{}
	}
	return StatusSnapshot{
		Health:               regs[slotHealthCode],
		LastErrorCode:        regs[slotLastErrorCode],
		SecondsInError:       regs[slotSecondsInErr],
		RequestsTotal:        getU32(regs, slotRequestsTotalLow),
		ResponsesValidTotal:  getU32(regs, slotResponsesValidLow),
		TimeoutsTotal:        getU32(regs, slotTimeoutsTotalLow),
		TransportErrorsTotal: getU32(regs, slotTransportErrorsTotalLow),
		ConsecutiveFailCurr:  regs[slotConsecutiveFailCurr],
		ConsecutiveFailMax:   regs[slotConsecutiveFailMax],
	}
}

// ErrorCode extracts a uint16 error code from an error value.
// Supports errors that implement Code(), ErrorCode(), or ModbusCode().
// Falls back to 1 for unknown errors.
func ErrorCode(err error) uint16 {
	if err == nil {
		return 0
	}

	type coderA interface{ Code() uint16 }
	type coderB interface{ ErrorCode() uint16 }
	type coderC interface{ ModbusCode() uint16 }

	var a coderA
	if asErr(err, &a) {
		return a.Code()
	}
	var b coderB
	if asErr(err, &b) {
		return b.ErrorCode()
	}
	var c coderC
	if asErr(err, &c) {
		return c.ModbusCode()
	}

	return 1
}

func asErr[T any](err error, target *T) bool {
	if err == nil {
		return false
	}
	v, ok := err.(T)
	if ok {
		*target = v
		return true
	}
	return false
}

// ---- Block health types ----

// ReadBlockHealth holds the per-read-block health state produced by the engine.
type ReadBlockHealth struct {
	Timeout           bool
	ConsecutiveErrors int
	LastExceptionCode byte
	LastSuccess       time.Time
	LastError         time.Time
}

// BlockHealthKey identifies one read block in the health store.
type BlockHealthKey struct {
	UnitID   string
	BlockIdx int
}

// BlockHealthReader is the interface through which the adapter queries per-block health.
// The concrete implementation is BlockHealthStore.
// Returns (timeout, consecutiveErrors, exceptionCode, found).
type BlockHealthReader interface {
	GetBlockHealth(unitID string, blockIdx int) (timeout bool, consecutiveErrors int, exceptionCode byte, found bool)
}

// BlockHealthStore is a thread-safe store for per-read-block health state.
type BlockHealthStore struct {
	mu      sync.RWMutex
	entries map[BlockHealthKey]ReadBlockHealth
}

// NewBlockHealthStore creates an empty BlockHealthStore.
func NewBlockHealthStore() *BlockHealthStore {
	return &BlockHealthStore{
		entries: make(map[BlockHealthKey]ReadBlockHealth),
	}
}

// Set writes the health state for one read block.
func (s *BlockHealthStore) Set(key BlockHealthKey, h ReadBlockHealth) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.entries[key] = h
}

// Get reads the health state for one read block.
func (s *BlockHealthStore) Get(key BlockHealthKey) (ReadBlockHealth, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	h, ok := s.entries[key]
	return h, ok
}

// SetBlockHealth sets the health state for one read block using primitive key components.
func (s *BlockHealthStore) SetBlockHealth(unitID string, blockIdx int, h ReadBlockHealth) {
	s.Set(BlockHealthKey{UnitID: unitID, BlockIdx: blockIdx}, h)
}

// GetBlockHealth returns the health state for one read block as primitive values.
// Returns (timeout, consecutiveErrors, exceptionCode, found).
func (s *BlockHealthStore) GetBlockHealth(unitID string, blockIdx int) (timeout bool, consecutiveErrors int, exceptionCode byte, found bool) {
	h, ok := s.Get(BlockHealthKey{UnitID: unitID, BlockIdx: blockIdx})
	return h.Timeout, h.ConsecutiveErrors, h.LastExceptionCode, ok
}

// GetLastSuccess returns the time of the last successful poll for one read block.
func (s *BlockHealthStore) GetLastSuccess(unitID string, blockIdx int) (time.Time, bool) {
	h, ok := s.Get(BlockHealthKey{UnitID: unitID, BlockIdx: blockIdx})
	if !ok {
		return time.Time{}, false
	}
	return h.LastSuccess, true
}

// ---- Write plan and StoreWriter ----

// WritePlan is the fully-built write plan for one polling unit.
type WritePlan struct {
	UnitID  string
	Targets []TargetMemory

	// Optional device status target (nil = status disabled)
	Status *StatusTarget
}

// TargetMemory is one in-process store target.
type TargetMemory struct {
	MemoryID MemoryID       // (Port, UnitID) key in the store
	Offsets  map[int]uint16 // per-FC address delta; missing FC defaults to 0
}

// StatusTarget describes where to write device status in the store.
type StatusTarget struct {
	MemoryID   MemoryID
	BaseSlot   uint16
	DeviceName string
}

// StoreWriter writes PollResult snapshots directly into the shared in-process Store.
//
// Architectural rule (LOCKED):
//   - There is NO TCP or network connection between the engine and the store.
//   - Writes are in-process function calls to Memory.
type StoreWriter struct {
	plan  WritePlan
	store Store
}

// NewStoreWriter creates a StoreWriter for the given plan and store.
func NewStoreWriter(plan WritePlan, store Store) *StoreWriter {
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
				if err := mem.WriteBits(AreaCoils, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: coils write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}

			case 2:
				packed := packBits(b.Bits)
				if err := mem.WriteBits(AreaDiscreteInputs, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: discrete_inputs write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}

			case 3:
				packed := packRegisters(b.Registers)
				if err := mem.WriteRegs(AreaHoldingRegs, dstAddr, b.Quantity, packed); err != nil {
					errs = append(errs, fmt.Sprintf(
						"writer: holding_regs write (unit_id=%d addr=%d qty=%d): %v",
						tgt.MemoryID.UnitID, dstAddr, b.Quantity, err,
					))
				}

			case 4:
				packed := packRegisters(b.Registers)
				if err := mem.WriteRegs(AreaInputRegs, dstAddr, b.Quantity, packed); err != nil {
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
	regs := encodeStatusBlock(snap, w.plan.Status.DeviceName, uint8(w.plan.Status.BaseSlot))

	src := make([]byte, len(regs)*2)
	for i, v := range regs {
		binary.BigEndian.PutUint16(src[i*2:i*2+2], v)
	}

	if err := mem.WriteRegs(AreaHoldingRegs, baseAddr, uint16(len(regs)), src); err != nil {
		return fmt.Errorf("status writer: write failed: %w", err)
	}

	return nil
}

// RawIngest is a convenience function that creates a temporary StoreWriter
// and writes the given blocks (as a successful poll result) into the store.
func RawIngest(store Store, plan WritePlan, blocks []BlockResult) error {
	res := PollResult{Blocks: blocks}
	return NewStoreWriter(plan, store).Write(res)
}

// ---- Write helpers ----

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
