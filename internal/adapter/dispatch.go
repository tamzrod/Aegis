// internal/adapter/dispatch.go
package adapter

import (
	"encoding/binary"

	"github.com/tamzrod/Aegis/internal/core"
)

// DispatchMemory routes a Modbus request to the in-process Store.
//
// Supported function codes:
//
//	FC 1  - Read Coils
//	FC 2  - Read Discrete Inputs
//	FC 3  - Read Holding Registers
//	FC 4  - Read Input Registers
//	FC 5  - Write Single Coil
//	FC 6  - Write Single Register (holding registers only)
//	FC 15 - Write Multiple Coils
//	FC 16 - Write Multiple Registers (holding registers only)
func DispatchMemory(store core.Store, req *Request) []byte {
	switch req.FunctionCode {
	case 1:
		return handleReadBits(store, req, core.AreaCoils)
	case 2:
		return handleReadBits(store, req, core.AreaDiscreteInputs)
	case 3:
		return handleReadRegs(store, req, core.AreaHoldingRegs)
	case 4:
		return handleReadRegs(store, req, core.AreaInputRegs)
	case 5:
		return handleWriteSingleCoil(store, req)
	case 6:
		return handleWriteSingleReg(store, req)
	case 15:
		return handleWriteMultipleCoils(store, req)
	case 16:
		return handleWriteMultipleRegs(store, req)
	default:
		return BuildExceptionPDU(req.FunctionCode, 0x01) // Illegal Function
	}
}

func resolveMemory(store core.Store, req *Request) (*core.Memory, bool) {
	memID := core.MemoryID{
		Port:   req.Port,
		UnitID: uint16(req.UnitID),
	}
	mem, err := store.MustGet(memID)
	if err != nil {
		return nil, false
	}
	return mem, true
}

func bitsForBitsLocal(n uint16) int {
	if n == 0 {
		return 0
	}
	return int((n + 7) / 8)
}

func handleReadBits(store core.Store, req *Request, area core.Area) []byte {
	decoded, err := DecodeReadRequest(req.Payload)
	if err != nil || decoded.Quantity == 0 {
		return BuildExceptionPDU(req.FunctionCode, 0x03) // Illegal Data Value
	}

	mem, ok := resolveMemory(store, req)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02) // Illegal Data Address
	}

	buf := make([]byte, bitsForBitsLocal(decoded.Quantity))
	if err := mem.ReadBits(area, decoded.Address, decoded.Quantity, buf); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildReadResponsePDU(req.FunctionCode, buf)
}

func handleWriteSingleCoil(store core.Store, req *Request) []byte {
	decoded, err := DecodeWriteSingle(req.Payload)
	if err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	var src byte
	switch decoded.Value {
	case 0xFF00:
		src = 0x01
	case 0x0000:
		src = 0x00
	default:
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	if err := mem.WriteBits(core.AreaCoils, decoded.Address, 1, []byte{src}); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteSingleResponsePDU(req.FunctionCode, decoded.Address, decoded.Value)
}

func handleWriteMultipleCoils(store core.Store, req *Request) []byte {
	decoded, err := DecodeWriteMultipleBits(req.Payload)
	if err != nil || decoded.Quantity == 0 {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	if err := mem.WriteBits(core.AreaCoils, decoded.Address, decoded.Quantity, decoded.Data); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteMultipleResponsePDU(req.FunctionCode, decoded.Address, decoded.Quantity)
}

func handleReadRegs(store core.Store, req *Request, area core.Area) []byte {
	decoded, err := DecodeReadRequest(req.Payload)
	if err != nil || decoded.Quantity == 0 {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	buf := make([]byte, int(decoded.Quantity)*2)
	if err := mem.ReadRegs(area, decoded.Address, decoded.Quantity, buf); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildReadResponsePDU(req.FunctionCode, buf)
}

func handleWriteSingleReg(store core.Store, req *Request) []byte {
	decoded, err := DecodeWriteSingle(req.Payload)
	if err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	src := make([]byte, 2)
	binary.BigEndian.PutUint16(src, decoded.Value)

	if err := mem.WriteRegs(core.AreaHoldingRegs, decoded.Address, 1, src); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteSingleResponsePDU(req.FunctionCode, decoded.Address, decoded.Value)
}

func handleWriteMultipleRegs(store core.Store, req *Request) []byte {
	decoded, err := DecodeWriteMultiple(req.Payload)
	if err != nil || decoded.Quantity == 0 || int(decoded.Quantity) != len(decoded.Values) {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	src := make([]byte, len(decoded.Values)*2)
	for i, v := range decoded.Values {
		binary.BigEndian.PutUint16(src[i*2:i*2+2], v)
	}

	if err := mem.WriteRegs(core.AreaHoldingRegs, decoded.Address, decoded.Quantity, src); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteMultipleResponsePDU(req.FunctionCode, decoded.Address, decoded.Quantity)
}
