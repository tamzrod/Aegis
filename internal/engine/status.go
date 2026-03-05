// internal/engine/status.go
package engine

// Device status block layout constants.
// These values define the protocol and MUST NOT be configurable.

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
	// Header (registers 0–1): 3-byte magic constant + 1-byte block index.
	slotHeader0 = 0 // uint16: magic[0] << 8 | magic[1]
	slotHeader1 = 1 // uint16: magic[2] << 8 | block_index

	slotHealthCode    = 2
	slotLastErrorCode = 3
	slotSecondsInErr  = 4

	slotDeviceNameStart = 5
	slotDeviceNameSlots = 8 // registers 5–12

	// registers 13–19 are reserved (always zero)

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
// blockIndex is the sequential block index (0–255) written into the header.
func encodeStatusBlock(s StatusSnapshot, deviceName string, blockIndex uint8) []uint16 {
	regs := make([]uint16, StatusSlotsPerDevice)

	// Header: fixed magic constant + block index.
	regs[slotHeader0] = uint16(statusMagicByte0)<<8 | uint16(statusMagicByte1)
	regs[slotHeader1] = uint16(statusMagicByte2)<<8 | uint16(blockIndex)

	regs[slotHealthCode] = s.Health
	regs[slotLastErrorCode] = s.LastErrorCode
	regs[slotSecondsInErr] = s.SecondsInError

	// Device name: pack ASCII pairs into registers (2 chars per register)
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

	// Transport counters (uint32 → two uint16, low word first)
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

// DecodeStatusBlock decodes a raw register slice (starting at the block base address)
// into a StatusSnapshot. regs must contain at least StatusSlotsPerDevice elements.
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

// asErr is a generic helper for errors.As without reflection juggling.
func asErr[T any](err error, target *T) bool {
	// Use a simple type assertion via interface — errors.As handles unwrapping.
	// We delegate to a closure to avoid importing "errors" here (already imported in poller.go).
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
