// internal/adapter/pdu_types.go
// Re-exports PDU types from internal/memory.
package adapter

import "github.com/tamzrod/Aegis/internal/memory"

type ReadRequestPDU = memory.ReadRequestPDU
type WriteSinglePDU = memory.WriteSinglePDU
type WriteMultiplePDU = memory.WriteMultiplePDU
type WriteMultipleBitsPDU = memory.WriteMultipleBitsPDU
