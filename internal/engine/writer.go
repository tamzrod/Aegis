// internal/engine/writer.go
// Re-exports writer types from internal/memory.
package engine

import "github.com/tamzrod/Aegis/internal/memory"

type WritePlan = memory.WritePlan
type TargetMemory = memory.TargetMemory
type StatusTarget = memory.StatusTarget
type StoreWriter = memory.StoreWriter

var NewStoreWriter = memory.NewStoreWriter
