// internal/adapter/response.go
package adapter

import "encoding/binary"

// BuildResponse wraps a PDU into a Modbus TCP response frame.
func BuildResponse(req *Request, pdu []byte) []byte {
	length := uint16(len(pdu) + 1)

	out := make([]byte, 7+len(pdu))
	binary.BigEndian.PutUint16(out[0:2], req.TransactionID)
	binary.BigEndian.PutUint16(out[2:4], req.ProtocolID)
	binary.BigEndian.PutUint16(out[4:6], length)
	out[6] = req.UnitID
	copy(out[7:], pdu)

	return out
}
