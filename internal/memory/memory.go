// internal/memory/memory.go
// Package memory provides the MMA2 memory engine: the in-process Modbus address space,
// device status encoding, per-block health tracking, store writing, and the Modbus TCP server
// adapter. It is the canonical home for all types shared between the polling engine and the
// adapter layer.
//
// All memory types (Store, Memory, MemStore, etc.) are re-exported from internal/core so
// that consumers can use either package interchangeably.
package memory

import "github.com/tamzrod/Aegis/internal/core"

// ---- Re-export core types ----

type Store = core.Store
type Memory = core.Memory
type MemStore = core.MemStore
type MemoryID = core.MemoryID
type Area = core.Area
type AreaLayout = core.AreaLayout
type MemoryLayouts = core.MemoryLayouts
type StateSealingDef = core.StateSealingDef

const (
AreaInvalid        = core.AreaInvalid
AreaCoils          = core.AreaCoils
AreaDiscreteInputs = core.AreaDiscreteInputs
AreaHoldingRegs    = core.AreaHoldingRegs
AreaInputRegs      = core.AreaInputRegs
)

var NewMemStore = core.NewMemStore
var NewMemory = core.NewMemory

var (
ErrUnknownMemoryID = core.ErrUnknownMemoryID
ErrAreaNotDefined  = core.ErrAreaNotDefined
ErrInvalidArea     = core.ErrInvalidArea
ErrCountZero       = core.ErrCountZero
ErrDstTooSmall     = core.ErrDstTooSmall
ErrSrcTooSmall     = core.ErrSrcTooSmall
ErrOutOfBounds     = core.ErrOutOfBounds
ErrStartOverflow   = core.ErrStartOverflow
ErrSizeZero        = core.ErrSizeZero
ErrNilMemory       = core.ErrNilMemory
ErrEmptyPort       = core.ErrEmptyPort
ErrUnitIDZero      = core.ErrUnitIDZero
ErrNilStore        = core.ErrNilStore
)
