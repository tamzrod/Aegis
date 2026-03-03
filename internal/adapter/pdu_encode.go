// internal/adapter/pdu_encode.go
package adapter

import "encoding/binary"

// BuildReadResponsePDU builds FC 1, 2, 3, 4 response PDU.
func BuildReadResponsePDU(fc uint8, data []byte) []byte {
	out := make([]byte, 2+len(data))
	out[0] = fc
	out[1] = uint8(len(data))
	copy(out[2:], data)
	return out
}

// BuildWriteSingleResponsePDU builds FC 5, 6 response PDU.
func BuildWriteSingleResponsePDU(fc uint8, addr uint16, value uint16) []byte {
	out := make([]byte, 5)
	out[0] = fc
	binary.BigEndian.PutUint16(out[1:3], addr)
	binary.BigEndian.PutUint16(out[3:5], value)
	return out
}

// BuildWriteMultipleResponsePDU builds FC 15, 16 response PDU.
func BuildWriteMultipleResponsePDU(fc uint8, addr uint16, qty uint16) []byte {
	out := make([]byte, 5)
	out[0] = fc
	binary.BigEndian.PutUint16(out[1:3], addr)
	binary.BigEndian.PutUint16(out[3:5], qty)
	return out
}

// BuildExceptionPDU builds a Modbus exception response PDU.
func BuildExceptionPDU(fc uint8, code uint8) []byte {
	return []byte{fc | 0x80, code}
}
