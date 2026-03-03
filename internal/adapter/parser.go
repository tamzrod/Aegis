// internal/adapter/parser.go
package adapter

import (
	"encoding/binary"
	"fmt"
	"io"
)

// ReadRequest reads exactly one Modbus TCP request from r.
// port is the local listening TCP port, injected into the returned request.
func ReadRequest(r io.Reader, port uint16) (*Request, error) {
	mbap := make([]byte, 7)
	if _, err := io.ReadFull(r, mbap); err != nil {
		return nil, err
	}

	txID := binary.BigEndian.Uint16(mbap[0:2])
	protoID := binary.BigEndian.Uint16(mbap[2:4])
	length := binary.BigEndian.Uint16(mbap[4:6])
	unitID := mbap[6]

	if length == 0 {
		return nil, fmt.Errorf("invalid MBAP length")
	}

	pduLen := int(length) - 1
	if pduLen <= 0 {
		return nil, fmt.Errorf("invalid PDU length")
	}

	pdu := make([]byte, pduLen)
	if _, err := io.ReadFull(r, pdu); err != nil {
		return nil, err
	}

	return &Request{
		Port:          port,
		TransactionID: txID,
		ProtocolID:    protoID,
		Length:        length,
		UnitID:        unitID,
		FunctionCode:  pdu[0],
		Payload:       pdu[1:],
	}, nil
}
