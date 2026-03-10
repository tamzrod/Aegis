// internal/adapter/pdu_decode.go
// Re-exports PDU decode functions from internal/memory.
package adapter

import "github.com/tamzrod/Aegis/internal/memory"

var DecodeReadRequest = memory.DecodeReadRequest
var DecodeWriteSingle = memory.DecodeWriteSingle
var DecodeWriteMultiple = memory.DecodeWriteMultiple
var DecodeWriteMultipleBits = memory.DecodeWriteMultipleBits
