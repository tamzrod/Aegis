// internal/adapter/pdu_decode.go
package adapter

import (
	"encoding/binary"
	"fmt"
)

// DecodeReadRequest decodes FC 1, 2, 3, 4 payload.
func DecodeReadRequest(pdu []byte) (*ReadRequestPDU, error) {
	if len(pdu) != 4 {
		return nil, fmt.Errorf("invalid read request length")
	}
	return &ReadRequestPDU{
		Address:  binary.BigEndian.Uint16(pdu[0:2]),
		Quantity: binary.BigEndian.Uint16(pdu[2:4]),
	}, nil
}

// DecodeWriteSingle decodes FC 5, 6 payload.
func DecodeWriteSingle(pdu []byte) (*WriteSinglePDU, error) {
	if len(pdu) != 4 {
		return nil, fmt.Errorf("invalid write single length")
	}
	return &WriteSinglePDU{
		Address: binary.BigEndian.Uint16(pdu[0:2]),
		Value:   binary.BigEndian.Uint16(pdu[2:4]),
	}, nil
}

// DecodeWriteMultiple decodes FC 16 (write multiple registers) payload.
func DecodeWriteMultiple(pdu []byte) (*WriteMultiplePDU, error) {
	if len(pdu) < 5 {
		return nil, fmt.Errorf("invalid write multiple length")
	}

	addr := binary.BigEndian.Uint16(pdu[0:2])
	qty := binary.BigEndian.Uint16(pdu[2:4])
	byteCount := int(pdu[4])

	if len(pdu[5:]) != byteCount {
		return nil, fmt.Errorf("byte count mismatch")
	}
	if byteCount%2 != 0 {
		return nil, fmt.Errorf("invalid register byte count")
	}

	values := make([]uint16, 0, qty)
	for i := 0; i < byteCount; i += 2 {
		values = append(values, binary.BigEndian.Uint16(pdu[5+i:5+i+2]))
	}

	return &WriteMultiplePDU{
		Address:  addr,
		Quantity: qty,
		Values:   values,
	}, nil
}

// DecodeWriteMultipleBits decodes FC 15 (write multiple coils) payload.
// Bits are packed LSB-first per Modbus spec.
func DecodeWriteMultipleBits(pdu []byte) (*WriteMultipleBitsPDU, error) {
	if len(pdu) < 5 {
		return nil, fmt.Errorf("invalid write multiple bits length")
	}

	addr := binary.BigEndian.Uint16(pdu[0:2])
	qty := binary.BigEndian.Uint16(pdu[2:4])
	byteCount := int(pdu[4])

	if len(pdu[5:]) != byteCount {
		return nil, fmt.Errorf("byte count mismatch")
	}

	expected := 0
	if qty != 0 {
		expected = int((qty + 7) / 8)
	}
	if byteCount != expected {
		return nil, fmt.Errorf("invalid coil byte count")
	}

	data := make([]byte, byteCount)
	copy(data, pdu[5:])

	return &WriteMultipleBitsPDU{
		Address:  addr,
		Quantity: qty,
		Data:     data,
	}, nil
}
