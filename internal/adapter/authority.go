// internal/adapter/authority.go
package adapter

import (
	"sort"

	"github.com/tamzrod/Aegis/internal/config"
)

// BlockHealthReader is the interface through which the adapter queries per-block health.
// The concrete implementation lives in the engine package (engine.BlockHealthStore).
// Using an interface avoids a circular import between adapter and engine.
// Returns (timeout, consecutiveErrors, exceptionCode, found).
type BlockHealthReader interface {
	GetBlockHealth(unitID string, blockIdx int) (timeout bool, consecutiveErrors int, exceptionCode byte, found bool)
}

// targetReadBlock describes one configured read block for a replicator target.
// Block indices are scoped to the single replicator unit that owns the surface.
type targetReadBlock struct {
	blockIdx int    // index within the unit's reads list
	fc       uint8
	address  uint16
	quantity uint16
}

// boundingRange is the inclusive address range [start, end) covering all read blocks
// for one (port, unitID, FC) combination.
type boundingRange struct {
	start uint16
	end   uint16 // exclusive
}

// targetEntry holds the authority configuration for one (port, unitID) pair.
// Each surface corresponds to exactly one replicator unit.
type targetEntry struct {
	mode           string                  // "A", "B", or "C"
	replicatorID   string                  // the single replicator unit that owns this surface
	blocks         []targetReadBlock       // read blocks for this surface
	boundingRanges map[uint8]boundingRange // fc → bounding range derived from read blocks
}

type targetKey struct {
	port   uint16
	unitID uint16
}

// AuthorityRegistry maps (port, unitID) pairs to their authority configuration.
// It is built once at startup from config and is read-only at runtime.
// It is not global: each entry is per-target (per replicator unit target).
type AuthorityRegistry struct {
	targets map[targetKey]targetEntry
	health  BlockHealthReader
}

// BuildAuthorityRegistry constructs an AuthorityRegistry from the validated config.
// Assumes config.Validate() has already passed.
//
// Each (port, unit_id) surface maps to exactly one replicator unit (validated upstream).
func BuildAuthorityRegistry(cfg *config.Config, health BlockHealthReader) *AuthorityRegistry {
	targets := make(map[targetKey]targetEntry)

	for _, u := range cfg.Replicator.Units {
		key := targetKey{port: u.Target.Port, unitID: u.Target.UnitID}

		blocks := make([]targetReadBlock, 0, len(u.Reads))
		for i, r := range u.Reads {
			blocks = append(blocks, targetReadBlock{
				blockIdx: i,
				fc:       r.FC,
				address:  r.Address,
				quantity: r.Quantity,
			})
		}

		brs := make(map[uint8]boundingRange)
		for _, r := range u.Reads {
			end := r.Address + r.Quantity
			br, exists := brs[r.FC]
			if !exists {
				brs[r.FC] = boundingRange{start: r.Address, end: end}
			} else {
				if r.Address < br.start {
					br.start = r.Address
				}
				if end > br.end {
					br.end = end
				}
				brs[r.FC] = br
			}
		}

		targets[key] = targetEntry{
			mode:           u.Target.Mode,
			replicatorID:   u.ID,
			blocks:         blocks,
			boundingRanges: brs,
		}
	}

	return &AuthorityRegistry{
		targets: targets,
		health:  health,
	}
}

// Enforce checks authority for an incoming Modbus request.
// Returns (exception PDU, true) if the request must be rejected, or (nil, false)
// if it may proceed.
//
// Authority is per-target: if no target is registered for (port, unitID), the
// request is allowed through (e.g. for status-only memory units).
//
// Mode A (Standalone):
//   - Writes allowed.
//   - Reads always served within bounding range; block health is not consulted.
//
// Mode B (Strict):
//   - Writes rejected (0x01).
//   - Reads outside bounding range → 0x02.
//   - Reads in a hole (within bounding range, not in a segment) → 0x02.
//   - Reads in a covered segment: if unprimed or timeout → 0x0B;
//     if exception recorded → forward exception; otherwise serve normally.
//
// Mode C (Buffered):
//   - Writes rejected (0x01).
//   - Reads outside bounding range → 0x02.
//   - Reads in a hole or covered segment → always serve (health not consulted).
func (r *AuthorityRegistry) Enforce(port, unitID uint16, fc uint8, address, quantity uint16) ([]byte, bool) {
	entry, ok := r.targets[targetKey{port: port, unitID: unitID}]
	if !ok {
		// No registered target — allow through without authority enforcement.
		return nil, false
	}

	if isWriteFC(fc) {
		if entry.mode != config.TargetModeA {
			return BuildExceptionPDU(fc, 0x01), true
		}
		return nil, false
	}

	if isReadFC(fc) {
		// Check whether the request falls within the bounding range for this FC.
		br, hasBR := entry.boundingRanges[fc]
		reqEnd := uint32(address) + uint32(quantity)
		if !hasBR || uint32(address) < uint32(br.start) || reqEnd > uint32(br.end) {
			// Outside bounding range: always 0x02 regardless of mode.
			return BuildExceptionPDU(fc, 0x02), true
		}

		// Within bounding range: check segment coverage.
		covering := findCoveringBlocks(entry.blocks, fc, address, quantity)
		if covering == nil {
			// Hole: within bounding range but not covered by any segment.
			switch entry.mode {
			case config.TargetModeA, config.TargetModeC:
				// Serve zero-filled response from memory.
				return nil, false
			default: // TargetModeB
				return BuildExceptionPDU(fc, 0x02), true
			}
		}

		// Fully covered by segments.
		switch entry.mode {
		case config.TargetModeA, config.TargetModeC:
			// Always serve reads; health is not consulted.
			return nil, false

		case config.TargetModeB:
			for _, blk := range covering {
				timeout, _, excCode, found := r.health.GetBlockHealth(entry.replicatorID, blk.blockIdx)
				if !found {
					// Unprimed block (no successful poll yet): mode B must not serve
					// potentially stale or zero memory as if it were valid device data.
					// 0x0B (Gateway Target Device Failed to Respond) signals to clients
					// that the data source is unavailable, consistent with timeout behavior.
					return BuildExceptionPDU(fc, 0x0B), true
				}
				if timeout {
					return BuildExceptionPDU(fc, 0x0B), true
				}
				if excCode != 0 {
					return BuildExceptionPDU(fc, excCode), true
				}
			}
			return nil, false
		}
	}

	return nil, false
}

// findCoveringBlocks returns the read blocks that together fully cover the
// [address, address+quantity) range for the given FC.
// Returns nil if coverage is incomplete (gap or no matching blocks).
// Partial overlap without full coverage returns nil.
func findCoveringBlocks(blocks []targetReadBlock, fc uint8, address, quantity uint16) []targetReadBlock {
	reqStart := uint32(address)
	reqEnd := uint32(address) + uint32(quantity)

	// Collect overlapping blocks for the same FC.
	var matching []targetReadBlock
	for _, b := range blocks {
		if b.fc != fc {
			continue
		}
		bStart := uint32(b.address)
		bEnd := bStart + uint32(b.quantity)
		// Include block if it overlaps with the request range.
		if bStart < reqEnd && bEnd > reqStart {
			matching = append(matching, b)
		}
	}

	if len(matching) == 0 {
		return nil
	}

	// Sort by start address to check contiguous coverage.
	sort.Slice(matching, func(i, j int) bool {
		return matching[i].address < matching[j].address
	})

	// Verify the union of matching blocks covers the full request range.
	coveredUntil := reqStart
	for _, b := range matching {
		bStart := uint32(b.address)
		bEnd := bStart + uint32(b.quantity)
		if bStart > coveredUntil {
			return nil // gap in coverage
		}
		if bEnd > coveredUntil {
			coveredUntil = bEnd
		}
	}

	if coveredUntil < reqEnd {
		return nil // request extends beyond coverage
	}

	return matching
}

// isWriteFC returns true for write function codes (FC 5, 6, 15, 16).
func isWriteFC(fc uint8) bool {
	return fc == 5 || fc == 6 || fc == 15 || fc == 16
}

// isReadFC returns true for read function codes (FC 1, 2, 3, 4).
func isReadFC(fc uint8) bool {
	return fc >= 1 && fc <= 4
}
