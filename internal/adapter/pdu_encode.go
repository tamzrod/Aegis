// internal/adapter/pdu_encode.go
// Re-exports PDU encode functions from internal/memory.
package adapter

import "github.com/tamzrod/Aegis/internal/memory"

var BuildExceptionPDU = memory.BuildExceptionPDU
var BuildReadResponsePDU = memory.BuildReadResponsePDU
var BuildWriteSingleResponsePDU = memory.BuildWriteSingleResponsePDU
var BuildWriteMultipleResponsePDU = memory.BuildWriteMultipleResponsePDU
