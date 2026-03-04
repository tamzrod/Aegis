// internal/adapter/handler.go
package adapter

import (
	"encoding/binary"
	"io"
	"log"
	"net"

	"github.com/tamzrod/Aegis/internal/core"
)

// Enforcer is the narrow interface used by HandleConn for per-request authority
// enforcement.  The concrete implementation lives in authority.go
// (AuthorityRegistry); using an interface here keeps handler.go decoupled from
// that implementation.
type Enforcer interface {
	// Enforce checks authority for an incoming Modbus request.
	// Returns (exception PDU, true) if the request must be rejected, or
	// (nil, false) if it may proceed.
	Enforce(port, unitID uint16, fc uint8, address, quantity uint16) ([]byte, bool)
}

// HandleConn handles a single Modbus TCP client connection.
// It reads requests in a loop, dispatches each to the in-process Store,
// and writes responses back to the client.
//
// Authority is enforced before dispatch using the Enforcer:
//   - Write FCs (5, 6, 15, 16) are rejected with 0x01 unless mode == A.
//   - Read FCs (1, 2, 3, 4) are checked against per-block health in mode B.
//   - Reads not covered by any read block return 0x02.
//
// State sealing is enforced here: if a memory block has a sealing flag coil
// and its value is 0 (sealed), the server returns Device Busy (0x06) for all requests.
func HandleConn(conn net.Conn, store core.Store, authority Enforcer) {
	defer conn.Close()

	localAddr, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		log.Printf("adapter: failed to get local TCP address")
		return
	}
	port := uint16(localAddr.Port)

	for {
		req, err := ReadRequest(conn, port)
		if err != nil {
			if err != io.EOF {
				log.Printf("adapter: read error: %v", err)
			}
			return
		}

		// Per-target authority enforcement: check before state sealing and dispatch.
		addr, qty := extractAddressQuantity(req)
		log.Printf("adapter: ROUTING REQUEST port=%d unit=%d fc=%d address=%d quantity=%d",
			port, req.UnitID, req.FunctionCode, addr, qty)
		if authority != nil {
			if pdu, rejected := authority.Enforce(port, uint16(req.UnitID), req.FunctionCode, addr, qty); rejected {
				_, _ = conn.Write(BuildResponse(req, pdu))
				continue
			}
		}

		mid := core.MemoryID{
			Port:   req.Port,
			UnitID: uint16(req.UnitID),
		}

		// State sealing: if configured and flag == 0 → Device Busy
		if mem, ok := store.Get(mid); ok {
			if seal := mem.StateSealing(); seal != nil {
				buf := []byte{0}
				if err := mem.ReadBits(seal.Area, seal.Address, 1, buf); err != nil {
					pdu := BuildExceptionPDU(req.FunctionCode, 0x06)
					_, _ = conn.Write(BuildResponse(req, pdu))
					continue
				}
				// 0 = sealed, 1 = unsealed
				if (buf[0] & 0x01) == 0 {
					pdu := BuildExceptionPDU(req.FunctionCode, 0x06)
					_, _ = conn.Write(BuildResponse(req, pdu))
					continue
				}
			}
		}

		pdu := DispatchMemory(store, req)
		if pdu == nil {
			return
		}

		frame := BuildResponse(req, pdu)
		if _, err := conn.Write(frame); err != nil {
			log.Printf("adapter: write error: %v", err)
			return
		}
	}
}

// extractAddressQuantity extracts the start address and quantity from a request payload.
// For FC 1-4: payload is [addr_hi, addr_lo, qty_hi, qty_lo].
// For FC 5, 6: payload is [addr_hi, addr_lo, value_hi, value_lo] — quantity = 1.
// For FC 15, 16: payload is [addr_hi, addr_lo, qty_hi, qty_lo, byte_count, ...].
// Returns (0, 0) if the payload is too short to decode.
func extractAddressQuantity(req *Request) (address, quantity uint16) {
	p := req.Payload
	if len(p) < 4 {
		return 0, 0
	}
	address = binary.BigEndian.Uint16(p[0:2])
	fc := req.FunctionCode
	switch {
	case fc >= 1 && fc <= 4:
		quantity = binary.BigEndian.Uint16(p[2:4])
	case fc == 5 || fc == 6:
		quantity = 1
	case fc == 15 || fc == 16:
		quantity = binary.BigEndian.Uint16(p[2:4])
	}
	return address, quantity
}
