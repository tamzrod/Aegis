// internal/engine/block_health.go
// Re-exports block health types from internal/memory.
package engine

import "github.com/tamzrod/Aegis/internal/memory"

type ReadBlockHealth = memory.ReadBlockHealth
type BlockHealthKey = memory.BlockHealthKey
type BlockHealthStore = memory.BlockHealthStore
type BlockHealthReader = memory.BlockHealthReader

var NewBlockHealthStore = memory.NewBlockHealthStore
