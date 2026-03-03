// internal/adapter/handler.go
package adapter

import (
	"io"
	"log"
	"net"

	"github.com/tamzrod/Aegis/internal/core"
)

// HandleConn handles a single Modbus TCP client connection.
// It reads requests in a loop, dispatches each to the in-process Store,
// and writes responses back to the client.
//
// State sealing is enforced here: if a memory block has a sealing flag coil
// and its value is 0 (sealed), the server returns Device Busy (0x06) for all requests.
func HandleConn(conn net.Conn, store core.Store) {
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
