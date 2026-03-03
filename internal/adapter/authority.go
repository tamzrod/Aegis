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
type targetReadBlock struct {
	blockIdx int
	fc       uint8
	address  uint16
	quantity uint16
}

// targetEntry holds the authority configuration for one (port, unitID) pair.
type targetEntry struct {
	mode         string            // "A", "B", or "C"
	replicatorID string            // replicator unit string ID (for health lookups)
	blocks       []targetReadBlock // configured read blocks for this target
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
func BuildAuthorityRegistry(cfg *config.Config, health BlockHealthReader) *AuthorityRegistry {
	listenerPort := make(map[string]uint16)
	for _, l := range cfg.Server.Listeners {
		port, err := config.ParseListenPort(l.Listen)
		if err != nil {
			continue
		}
		listenerPort[l.ID] = port
	}

	targets := make(map[targetKey]targetEntry)

	for _, u := range cfg.Replicator.Units {
		port, ok := listenerPort[u.Target.ListenerID]
		if !ok {
			continue
		}
		key := targetKey{port: port, unitID: u.Target.UnitID}

		blocks := make([]targetReadBlock, 0, len(u.Reads))
		for i, r := range u.Reads {
			blocks = append(blocks, targetReadBlock{
				blockIdx: i,
				fc:       r.FC,
				address:  r.Address,
				quantity: r.Quantity,
			})
		}

		targets[key] = targetEntry{
			mode:         u.Target.Mode,
			replicatorID: u.ID,
			blocks:       blocks,
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
//   - Reads always served; block health is not consulted.
//
// Mode B (Strict):
//   - Writes rejected (0x01).
//   - Reads: if covering block has Timeout → 0x0B.
//           if covering block has LastExceptionCode != 0 → forward exception.
//           otherwise serve normally.
//
// Mode C (Buffered):
//   - Writes rejected (0x01).
//   - Reads always served; block health is not consulted.
//
// For read requests: if no read block fully covers the request range, 0x02 is returned.
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
		covering := findCoveringBlocks(entry.blocks, fc, address, quantity)
		if covering == nil {
			return BuildExceptionPDU(fc, 0x02), true
		}

		switch entry.mode {
		case config.TargetModeA, config.TargetModeC:
			// Always serve reads; health is not consulted.
			return nil, false

		case config.TargetModeB:
			for _, blk := range covering {
				timeout, _, excCode, found := r.health.GetBlockHealth(entry.replicatorID, blk.blockIdx)
				if !found {
					continue // no health data yet — allow
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
