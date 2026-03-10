// internal/engine/types.go
// Re-exports types from internal/puller and internal/memory.
package engine

import (
	"github.com/tamzrod/Aegis/internal/memory"
	"github.com/tamzrod/Aegis/internal/puller"
)

type ReadBlock = puller.ReadBlock
type BlockResult = memory.BlockResult
type PollResult = memory.PollResult
type BlockUpdate = memory.BlockUpdate
type TransportCounters = puller.TransportCounters
