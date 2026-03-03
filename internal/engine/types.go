// internal/engine/types.go
package engine

import "time"

// ReadBlock describes one Modbus read geometry.
// Geometry only: no semantics.
type ReadBlock struct {
	FC       uint8
	Address  uint16
	Quantity uint16
}

// BlockResult is the raw result of a single read.
type BlockResult struct {
	FC       uint8
	Address  uint16
	Quantity uint16

	// Exactly one of these is populated depending on FC.
	Bits      []bool   // FC 1, 2
	Registers []uint16 // FC 3, 4
}

// PollResult is a snapshot produced by one poll cycle.
// All-or-nothing: if Err is non-nil, Blocks is empty.
type PollResult struct {
	UnitID string
	At     time.Time

	Blocks []BlockResult
	Err    error
}

// TransportCounters holds lifetime transport instrumentation for a single polling unit.
// These counters are:
//   - Monotonic
//   - Integer-only
//   - Passive observability only (do not influence control flow)
type TransportCounters struct {
	RequestsTotal        uint32
	ResponsesValidTotal  uint32
	TimeoutsTotal        uint32
	TransportErrorsTotal uint32

	ConsecutiveFailCurr uint16
	ConsecutiveFailMax  uint16
}
