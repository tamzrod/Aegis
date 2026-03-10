// internal/engine/status.go
// Re-exports status types and constants from internal/memory.
package engine

import "github.com/tamzrod/Aegis/internal/memory"

const StatusSlotsPerDevice = memory.StatusSlotsPerDevice
const HealthUnknown = memory.HealthUnknown
const HealthOK = memory.HealthOK
const HealthError = memory.HealthError
const HealthStale = memory.HealthStale
const HealthDisabled = memory.HealthDisabled

type StatusSnapshot = memory.StatusSnapshot

var DecodeStatusBlock = memory.DecodeStatusBlock
var ErrorCode = memory.ErrorCode
