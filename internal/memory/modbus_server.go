// internal/memory/modbus_server.go
// Modbus TCP server adapter: server lifecycle, per-connection handling, request dispatch,
// authority enforcement, PDU parsing and encoding.
// All types previously in internal/adapter/ (excluding webui) are merged here.
package memory

import (
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"sort"
	"sync"

	"github.com/tamzrod/Aegis/internal/config"
)

// ---- PDU types ----

// ReadRequestPDU represents FC 1, 2, 3, 4
type ReadRequestPDU struct {
	Address  uint16
	Quantity uint16
}

// WriteSinglePDU represents FC 5, 6
type WriteSinglePDU struct {
	Address uint16
	Value   uint16
}

// WriteMultiplePDU represents FC 16 (write multiple registers)
type WriteMultiplePDU struct {
	Address  uint16
	Quantity uint16
	Values   []uint16
}

// WriteMultipleBitsPDU represents FC 15 (write multiple coils)
type WriteMultipleBitsPDU struct {
	Address  uint16
	Quantity uint16
	Data     []byte
}

// ---- PDU decode ----

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

// ---- PDU encode ----

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

// ---- Request ----

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

// ---- Response ----

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

// ---- Authority registry ----

// targetReadBlock describes one configured read block for a replicator target.
type targetReadBlock struct {
	blockIdx int
	fc       uint8
	address  uint16
	quantity uint16
}

// boundingRange is the inclusive address range [start, end) covering all read blocks.
type boundingRange struct {
	start uint16
	end   uint16 // exclusive
}

// targetEntry holds the authority configuration for one (port, unitID) pair.
type targetEntry struct {
	mode           string
	replicatorID   string
	blocks         []targetReadBlock
	boundingRanges map[uint8]boundingRange
}

type targetKey struct {
	port   uint16
	unitID uint16
}

// AuthorityRegistry maps (port, unitID) pairs to their authority configuration.
type AuthorityRegistry struct {
	targets        map[targetKey]targetEntry
	health         BlockHealthReader
	adapterRouting bool
}

// BuildAuthorityRegistry constructs an AuthorityRegistry from the validated config.
func BuildAuthorityRegistry(cfg *config.Config, health BlockHealthReader) *AuthorityRegistry {
	targets := make(map[targetKey]targetEntry)

	for _, u := range cfg.Replicator.Units {
		key := targetKey{port: u.Target.Port, unitID: u.Target.UnitID}

		blocks := make([]targetReadBlock, 0, len(u.Reads))
		for i, r := range u.Reads {
			blocks = append(blocks, targetReadBlock{
				blockIdx: i,
				fc:       r.FC,
				address:  r.Address,
				quantity: r.Quantity,
			})
		}

		brs := make(map[uint8]boundingRange)
		for _, r := range u.Reads {
			end := r.Address + r.Quantity
			br, exists := brs[r.FC]
			if !exists {
				brs[r.FC] = boundingRange{start: r.Address, end: end}
			} else {
				if r.Address < br.start {
					br.start = r.Address
				}
				if end > br.end {
					br.end = end
				}
				brs[r.FC] = br
			}
		}

		targets[key] = targetEntry{
			mode:           u.Target.Mode,
			replicatorID:   u.ID,
			blocks:         blocks,
			boundingRanges: brs,
		}

		log.Printf("adapter: authority registered %d:%d → %s", key.port, key.unitID, u.ID)
	}

	return &AuthorityRegistry{
		targets:        targets,
		health:         health,
		adapterRouting: cfg.Debug.AdapterRouting,
	}
}

// Enforce checks authority for an incoming Modbus request.
// Returns (exception PDU, true) if the request must be rejected, or (nil, false)
// if it may proceed.
func (r *AuthorityRegistry) Enforce(port, unitID uint16, fc uint8, address, quantity uint16) ([]byte, bool) {
	entry, ok := r.targets[targetKey{port: port, unitID: unitID}]
	if !ok {
		if r.adapterRouting {
			log.Printf("adapter: ROUTING REQUEST port=%d unit=%d → no authority entry (pass-through)", port, unitID)
		}
		return nil, false
	}

	if r.adapterRouting {
		log.Printf("adapter: ROUTING REQUEST port=%d unit=%d → matched %s", port, unitID, entry.replicatorID)
	}

	if isWriteFC(fc) {
		if entry.mode != config.TargetModeA {
			return BuildExceptionPDU(fc, 0x01), true
		}
		return nil, false
	}

	if isReadFC(fc) {
		br, hasBR := entry.boundingRanges[fc]
		reqEnd := uint32(address) + uint32(quantity)
		if !hasBR || uint32(address) < uint32(br.start) || reqEnd > uint32(br.end) {
			return BuildExceptionPDU(fc, 0x02), true
		}

		covering := findCoveringBlocks(entry.blocks, fc, address, quantity)
		if covering == nil {
			switch entry.mode {
			case config.TargetModeA, config.TargetModeC:
				return nil, false
			default: // TargetModeB
				return BuildExceptionPDU(fc, 0x02), true
			}
		}

		switch entry.mode {
		case config.TargetModeA, config.TargetModeC:
			return nil, false

		case config.TargetModeB:
			for _, blk := range covering {
				timeout, _, excCode, found := r.health.GetBlockHealth(entry.replicatorID, blk.blockIdx)
				if !found {
					return BuildExceptionPDU(fc, 0x0B), true
				}
				if timeout {
					return BuildExceptionPDU(fc, 0x0B), true
				}
				if excCode != 0 {
					return BuildExceptionPDU(fc, excCode), true
				}
			}
			return nil, false
		}
	}

	return nil, false
}

// findCoveringBlocks returns the read blocks that fully cover [address, address+quantity).
// Returns nil if coverage is incomplete.
func findCoveringBlocks(blocks []targetReadBlock, fc uint8, address, quantity uint16) []targetReadBlock {
	reqStart := uint32(address)
	reqEnd := uint32(address) + uint32(quantity)

	var matching []targetReadBlock
	for _, b := range blocks {
		if b.fc != fc {
			continue
		}
		bStart := uint32(b.address)
		bEnd := bStart + uint32(b.quantity)
		if bStart < reqEnd && bEnd > reqStart {
			matching = append(matching, b)
		}
	}

	if len(matching) == 0 {
		return nil
	}

	sort.Slice(matching, func(i, j int) bool {
		return matching[i].address < matching[j].address
	})

	coveredUntil := reqStart
	for _, b := range matching {
		bStart := uint32(b.address)
		bEnd := bStart + uint32(b.quantity)
		if bStart > coveredUntil {
			return nil
		}
		if bEnd > coveredUntil {
			coveredUntil = bEnd
		}
	}

	if coveredUntil < reqEnd {
		return nil
	}

	return matching
}

func isWriteFC(fc uint8) bool {
	return fc == 5 || fc == 6 || fc == 15 || fc == 16
}

func isReadFC(fc uint8) bool {
	return fc >= 1 && fc <= 4
}

// ---- Enforcer interface ----

// Enforcer is the narrow interface used by HandleConn for per-request authority enforcement.
type Enforcer interface {
	Enforce(port, unitID uint16, fc uint8, address, quantity uint16) ([]byte, bool)
}

// ---- Dispatch ----

// DispatchMemory routes a Modbus request to the in-process Store.
func DispatchMemory(store Store, req *Request, debug bool) []byte {
	switch req.FunctionCode {
	case 1:
		return handleReadBits(store, req, AreaCoils, debug)
	case 2:
		return handleReadBits(store, req, AreaDiscreteInputs, debug)
	case 3:
		return handleReadRegs(store, req, AreaHoldingRegs, debug)
	case 4:
		return handleReadRegs(store, req, AreaInputRegs, debug)
	case 5:
		return handleWriteSingleCoil(store, req, debug)
	case 6:
		return handleWriteSingleReg(store, req, debug)
	case 15:
		return handleWriteMultipleCoils(store, req, debug)
	case 16:
		return handleWriteMultipleRegs(store, req, debug)
	default:
		return BuildExceptionPDU(req.FunctionCode, 0x01)
	}
}

func resolveMemory(store Store, req *Request, debug bool) (*Memory, bool) {
	memID := MemoryID{
		Port:   req.Port,
		UnitID: uint16(req.UnitID),
	}
	mem, err := store.MustGet(memID)
	if err != nil {
		if debug {
			log.Printf("adapter: memory surface port=%d unit=%d not found → Illegal Data Address", req.Port, req.UnitID)
		}
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

func handleReadBits(store Store, req *Request, area Area, debug bool) []byte {
	decoded, err := DecodeReadRequest(req.Payload)
	if err != nil || decoded.Quantity == 0 {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req, debug)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	buf := make([]byte, bitsForBitsLocal(decoded.Quantity))
	if err := mem.ReadBits(area, decoded.Address, decoded.Quantity, buf); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildReadResponsePDU(req.FunctionCode, buf)
}

func handleWriteSingleCoil(store Store, req *Request, debug bool) []byte {
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

	mem, ok := resolveMemory(store, req, debug)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	if err := mem.WriteBits(AreaCoils, decoded.Address, 1, []byte{src}); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteSingleResponsePDU(req.FunctionCode, decoded.Address, decoded.Value)
}

func handleWriteMultipleCoils(store Store, req *Request, debug bool) []byte {
	decoded, err := DecodeWriteMultipleBits(req.Payload)
	if err != nil || decoded.Quantity == 0 {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req, debug)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	if err := mem.WriteBits(AreaCoils, decoded.Address, decoded.Quantity, decoded.Data); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteMultipleResponsePDU(req.FunctionCode, decoded.Address, decoded.Quantity)
}

func handleReadRegs(store Store, req *Request, area Area, debug bool) []byte {
	decoded, err := DecodeReadRequest(req.Payload)
	if err != nil || decoded.Quantity == 0 {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req, debug)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	buf := make([]byte, int(decoded.Quantity)*2)
	if err := mem.ReadRegs(area, decoded.Address, decoded.Quantity, buf); err != nil {
		if debug {
			log.Printf("adapter: request outside surface → Illegal Data Address (port=%d unit=%d fc=%d addr=%d qty=%d)",
				req.Port, req.UnitID, req.FunctionCode, decoded.Address, decoded.Quantity)
		}
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	if debug {
		log.Printf("adapter: request covered → serving data (port=%d unit=%d fc=%d addr=%d qty=%d)",
			req.Port, req.UnitID, req.FunctionCode, decoded.Address, decoded.Quantity)
	}
	return BuildReadResponsePDU(req.FunctionCode, buf)
}

func handleWriteSingleReg(store Store, req *Request, debug bool) []byte {
	decoded, err := DecodeWriteSingle(req.Payload)
	if err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req, debug)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	src := make([]byte, 2)
	binary.BigEndian.PutUint16(src, decoded.Value)

	if err := mem.WriteRegs(AreaHoldingRegs, decoded.Address, 1, src); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteSingleResponsePDU(req.FunctionCode, decoded.Address, decoded.Value)
}

func handleWriteMultipleRegs(store Store, req *Request, debug bool) []byte {
	decoded, err := DecodeWriteMultiple(req.Payload)
	if err != nil || decoded.Quantity == 0 || int(decoded.Quantity) != len(decoded.Values) {
		return BuildExceptionPDU(req.FunctionCode, 0x03)
	}

	mem, ok := resolveMemory(store, req, debug)
	if !ok {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	src := make([]byte, len(decoded.Values)*2)
	for i, v := range decoded.Values {
		binary.BigEndian.PutUint16(src[i*2:i*2+2], v)
	}

	if err := mem.WriteRegs(AreaHoldingRegs, decoded.Address, decoded.Quantity, src); err != nil {
		return BuildExceptionPDU(req.FunctionCode, 0x02)
	}

	return BuildWriteMultipleResponsePDU(req.FunctionCode, decoded.Address, decoded.Quantity)
}

// ---- Handler ----

// HandleConn handles a single Modbus TCP client connection.
func HandleConn(conn net.Conn, store Store, authority Enforcer, debug bool) {
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

		addr, qty := extractAddressQuantity(req)
		if debug {
			log.Printf("adapter: ROUTING REQUEST port=%d unit=%d fc=%d address=%d quantity=%d",
				port, req.UnitID, req.FunctionCode, addr, qty)
		}
		if authority != nil {
			if pdu, rejected := authority.Enforce(port, uint16(req.UnitID), req.FunctionCode, addr, qty); rejected {
				_, _ = conn.Write(BuildResponse(req, pdu))
				continue
			}
		}

		mid := MemoryID{
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
				if (buf[0] & 0x01) == 0 {
					pdu := BuildExceptionPDU(req.FunctionCode, 0x06)
					_, _ = conn.Write(BuildResponse(req, pdu))
					continue
				}
			}
		}

		pdu := DispatchMemory(store, req, debug)
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

// ---- Server ----

// Server is a Modbus TCP server adapter.
type Server struct {
	listen    string
	store     Store
	authority Enforcer
	debug     bool

	mu   sync.Mutex
	ln   net.Listener
	done chan struct{}
}

// NewServer creates a Server for the given listen address, store, and authority enforcer.
func NewServer(listen string, store Store, authority Enforcer, debug bool) *Server {
	return &Server{
		listen:    listen,
		store:     store,
		authority: authority,
		debug:     debug,
		done:      make(chan struct{}),
	}
}

// NewServerWithListener creates a Server with a pre-bound net.Listener.
func NewServerWithListener(listen string, ln net.Listener, store Store, authority Enforcer, debug bool) *Server {
	return &Server{
		listen:    listen,
		store:     store,
		authority: authority,
		debug:     debug,
		ln:        ln,
		done:      make(chan struct{}),
	}
}

// Addr returns the listen address string for this server.
func (s *Server) Addr() string {
	return s.listen
}

// ListenAndServe starts accepting Modbus TCP connections.
func (s *Server) ListenAndServe() error {
	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		return err
	}

	s.mu.Lock()
	s.ln = ln
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.ln = nil
		s.mu.Unlock()
		close(s.done)
	}()

	log.Printf("adapter: modbus tcp listening on %s", s.listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go HandleConn(conn, s.store, s.authority, s.debug)
	}
}

// Shutdown closes the listener and waits for ListenAndServe to return.
func (s *Server) Shutdown() {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()
	if ln != nil {
		ln.Close()
		<-s.done
	}
}

// Serve starts accepting connections on the pre-bound listener.
func (s *Server) Serve() error {
	s.mu.Lock()
	ln := s.ln
	s.mu.Unlock()

	defer func() {
		s.mu.Lock()
		s.ln = nil
		s.mu.Unlock()
		close(s.done)
	}()

	log.Printf("adapter: modbus tcp listening on %s", s.listen)

	for {
		conn, err := ln.Accept()
		if err != nil {
			return err
		}
		go HandleConn(conn, s.store, s.authority, s.debug)
	}
}
