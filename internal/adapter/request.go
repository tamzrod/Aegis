// internal/adapter/request.go
package adapter

// Request is a fully parsed Modbus TCP request.
type Request struct {
	// TCP context
	Port uint16

	// MBAP fields
	TransactionID uint16
	ProtocolID    uint16
	Length        uint16

	// PDU fields
	UnitID       uint8
	FunctionCode uint8
	Payload      []byte
}
