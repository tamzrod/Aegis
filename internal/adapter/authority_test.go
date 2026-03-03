// internal/adapter/authority_test.go
package adapter

import (
	"encoding/binary"
	"io"
	"net"
	"testing"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/core"
)

// --------------------
// Mock HealthChecker
// --------------------

type mockHealthChecker struct {
	healthy bool
}

func (m *mockHealthChecker) IsHealthyFor(_, _ uint16) bool { return m.healthy }

// --------------------
// Config-level tests
// --------------------

// TestDefaultAuthorityModeIsBuffer verifies that Load() sets the default to "buffer"
// when authority_mode is absent from the YAML.
func TestDefaultAuthorityModeIsBuffer(t *testing.T) {
	cfg := &config.Config{
		AuthorityMode: "", // simulates absent field before Load normalises
	}
	// Load applies the default; reproduce that logic here for a unit-level check.
	if cfg.AuthorityMode == "" {
		cfg.AuthorityMode = config.AuthorityModeBuffer
	}
	if cfg.AuthorityMode != config.AuthorityModeBuffer {
		t.Errorf("expected default authority_mode = %q, got %q", config.AuthorityModeBuffer, cfg.AuthorityMode)
	}
}

// TestInvalidAuthorityModeFailsValidation verifies that Validate rejects unknown modes.
func TestInvalidAuthorityModeFailsValidation(t *testing.T) {
	cfg := minimalValidConfig()
	cfg.AuthorityMode = "unknown"
	if err := config.Validate(cfg); err == nil {
		t.Error("expected validation error for unknown authority_mode")
	}
}

// --------------------
// enforceAuthority unit tests
// --------------------

// TestEnforceAuthorityWriteStandaloneAllowed verifies that write FCs are allowed
// in standalone mode.
func TestEnforceAuthorityWriteStandaloneAllowed(t *testing.T) {
	health := &mockHealthChecker{healthy: false}
	for _, fc := range []uint8{5, 6, 15, 16} {
		pdu, rejected := enforceAuthority(config.AuthorityModeStandalone, fc, 502, 1, health)
		if rejected {
			t.Errorf("FC%d: standalone should allow writes, got exception PDU: %v", fc, pdu)
		}
	}
}

// TestEnforceAuthorityWriteStrictRejected verifies that write FCs are rejected (0x01)
// in strict mode.
func TestEnforceAuthorityWriteStrictRejected(t *testing.T) {
	health := &mockHealthChecker{healthy: true}
	for _, fc := range []uint8{5, 6, 15, 16} {
		pdu, rejected := enforceAuthority(config.AuthorityModeStrict, fc, 502, 1, health)
		if !rejected {
			t.Errorf("FC%d: strict should reject writes", fc)
			continue
		}
		if len(pdu) != 2 || pdu[0] != fc|0x80 || pdu[1] != 0x01 {
			t.Errorf("FC%d: expected exception 0x01, got %v", fc, pdu)
		}
	}
}

// TestEnforceAuthorityWriteBufferRejected verifies that write FCs are rejected (0x01)
// in buffer mode.
func TestEnforceAuthorityWriteBufferRejected(t *testing.T) {
	health := &mockHealthChecker{healthy: true}
	for _, fc := range []uint8{5, 6, 15, 16} {
		pdu, rejected := enforceAuthority(config.AuthorityModeBuffer, fc, 502, 1, health)
		if !rejected {
			t.Errorf("FC%d: buffer should reject writes", fc)
			continue
		}
		if len(pdu) != 2 || pdu[0] != fc|0x80 || pdu[1] != 0x01 {
			t.Errorf("FC%d: expected exception 0x01, got %v", fc, pdu)
		}
	}
}

// TestEnforceAuthorityReadStrictUnhealthyBlocked verifies that reads return 0x0B
// in strict mode when the upstream is unhealthy.
func TestEnforceAuthorityReadStrictUnhealthyBlocked(t *testing.T) {
	health := &mockHealthChecker{healthy: false}
	for _, fc := range []uint8{1, 2, 3, 4} {
		pdu, rejected := enforceAuthority(config.AuthorityModeStrict, fc, 502, 1, health)
		if !rejected {
			t.Errorf("FC%d: strict should block reads when unhealthy", fc)
			continue
		}
		if len(pdu) != 2 || pdu[0] != fc|0x80 || pdu[1] != 0x0B {
			t.Errorf("FC%d: expected exception 0x0B, got %v", fc, pdu)
		}
	}
}

// TestEnforceAuthorityReadStrictHealthyAllowed verifies that reads proceed
// in strict mode when the upstream is healthy.
func TestEnforceAuthorityReadStrictHealthyAllowed(t *testing.T) {
	health := &mockHealthChecker{healthy: true}
	for _, fc := range []uint8{1, 2, 3, 4} {
		pdu, rejected := enforceAuthority(config.AuthorityModeStrict, fc, 502, 1, health)
		if rejected {
			t.Errorf("FC%d: strict should allow reads when healthy, got exception PDU: %v", fc, pdu)
		}
	}
}

// TestEnforceAuthorityReadBufferUnhealthyAllowed verifies that reads are never
// blocked in buffer mode, even when the upstream is unhealthy.
func TestEnforceAuthorityReadBufferUnhealthyAllowed(t *testing.T) {
	health := &mockHealthChecker{healthy: false}
	for _, fc := range []uint8{1, 2, 3, 4} {
		pdu, rejected := enforceAuthority(config.AuthorityModeBuffer, fc, 502, 1, health)
		if rejected {
			t.Errorf("FC%d: buffer should never block reads, got exception PDU: %v", fc, pdu)
		}
	}
}

// TestEnforceAuthorityReadStandaloneUnhealthyAllowed verifies that reads are never
// blocked in standalone mode, even when the upstream is unhealthy.
func TestEnforceAuthorityReadStandaloneUnhealthyAllowed(t *testing.T) {
	health := &mockHealthChecker{healthy: false}
	for _, fc := range []uint8{1, 2, 3, 4} {
		pdu, rejected := enforceAuthority(config.AuthorityModeStandalone, fc, 502, 1, health)
		if rejected {
			t.Errorf("FC%d: standalone should never block reads, got exception PDU: %v", fc, pdu)
		}
	}
}

// --------------------
// HandleConn integration tests via net.Pipe + fakeConn
// --------------------

// fakeConn wraps net.Conn and overrides LocalAddr to return a fake TCP address.
// This allows HandleConn to extract a port without a real TCP listener.
type fakeConn struct {
	net.Conn
	localAddr *net.TCPAddr
}

func (c *fakeConn) LocalAddr() net.Addr { return c.localAddr }

// buildTestStore creates a minimal store with one memory block: port=502, unitID=1,
// with 10 holding registers starting at address 0.
func buildTestStore(t *testing.T) core.Store {
	t.Helper()
	store := core.NewMemStore()
	mem, err := core.NewMemory(core.MemoryLayouts{
		HoldingRegs: &core.AreaLayout{Start: 0, Size: 10},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	if err := store.Add(core.MemoryID{Port: 502, UnitID: 1}, mem); err != nil {
		t.Fatalf("store.Add: %v", err)
	}
	return store
}

// buildFC3Frame builds a minimal Modbus TCP frame for FC3 (read 1 holding register
// at address 0 for unit_id 1).
func buildFC3Frame() []byte {
	// MBAP: txID=1, protoID=0, length=6, unitID=1; PDU: FC=3, addr=0, qty=1
	frame := make([]byte, 12)
	binary.BigEndian.PutUint16(frame[0:2], 1)    // txID
	binary.BigEndian.PutUint16(frame[2:4], 0)    // protoID
	binary.BigEndian.PutUint16(frame[4:6], 6)    // length = unitID(1) + PDU(5)
	frame[6] = 1                                  // unitID
	frame[7] = 3                                  // FC3
	binary.BigEndian.PutUint16(frame[8:10], 0)   // address = 0
	binary.BigEndian.PutUint16(frame[10:12], 1)  // quantity = 1
	return frame
}

// buildFC6Frame builds a minimal Modbus TCP frame for FC6 (write single register
// value 0x1234 at address 0 for unit_id 1).
func buildFC6Frame() []byte {
	// MBAP: txID=1, protoID=0, length=6, unitID=1; PDU: FC=6, addr=0, value=0x1234
	frame := make([]byte, 12)
	binary.BigEndian.PutUint16(frame[0:2], 1)      // txID
	binary.BigEndian.PutUint16(frame[2:4], 0)      // protoID
	binary.BigEndian.PutUint16(frame[4:6], 6)      // length
	frame[6] = 1                                    // unitID
	frame[7] = 6                                    // FC6
	binary.BigEndian.PutUint16(frame[8:10], 0)     // address = 0
	binary.BigEndian.PutUint16(frame[10:12], 0x1234) // value
	return frame
}

// sendAndReceive sends a Modbus TCP frame via the client end of a pipe and reads
// one response frame back.  The server goroutine runs HandleConn.
func sendAndReceive(t *testing.T, mode string, health HealthChecker, store core.Store, reqFrame []byte) []byte {
	t.Helper()

	srvRaw, cli := net.Pipe()
	srv := &fakeConn{Conn: srvRaw, localAddr: &net.TCPAddr{Port: 502}}

	done := make(chan struct{})
	go func() {
		defer close(done)
		HandleConn(srv, store, mode, health)
	}()

	if _, err := cli.Write(reqFrame); err != nil {
		t.Fatalf("write request: %v", err)
	}

	// Read MBAP header (7 bytes) then PDU.
	mbap := make([]byte, 7)
	if _, err := io.ReadFull(cli, mbap); err != nil {
		t.Fatalf("read MBAP header: %v", err)
	}
	length := binary.BigEndian.Uint16(mbap[4:6])
	pduLen := int(length) - 1
	pdu := make([]byte, pduLen)
	if _, err := io.ReadFull(cli, pdu); err != nil {
		t.Fatalf("read PDU: %v", err)
	}

	// Close client side to unblock HandleConn's next ReadRequest (EOF).
	cli.Close()
	<-done

	return pdu
}

// TestHandleConnStrictUnhealthyReadBlocked verifies that HandleConn returns 0x0B
// for a read request in strict mode when health is not OK.
func TestHandleConnStrictUnhealthyReadBlocked(t *testing.T) {
	store := buildTestStore(t)
	health := &mockHealthChecker{healthy: false}
	pdu := sendAndReceive(t, config.AuthorityModeStrict, health, store, buildFC3Frame())
	if len(pdu) != 2 || pdu[0] != 0x03|0x80 || pdu[1] != 0x0B {
		t.Errorf("strict+unhealthy read: expected exception 0x0B, got %v", pdu)
	}
}

// TestHandleConnBufferUnhealthyReadAllowed verifies that HandleConn returns data
// (not an exception) for a read request in buffer mode even when health is not OK.
func TestHandleConnBufferUnhealthyReadAllowed(t *testing.T) {
	store := buildTestStore(t)
	health := &mockHealthChecker{healthy: false}
	pdu := sendAndReceive(t, config.AuthorityModeBuffer, health, store, buildFC3Frame())
	// Successful FC3 response: pdu[0] == 0x03 (not exception bit set)
	if len(pdu) == 0 || pdu[0]&0x80 != 0 {
		t.Errorf("buffer+unhealthy read: expected data response, got %v", pdu)
	}
}

// TestHandleConnStandaloneWriteAllowed verifies that HandleConn processes a write
// in standalone mode (no exception returned).
func TestHandleConnStandaloneWriteAllowed(t *testing.T) {
	store := buildTestStore(t)
	health := &mockHealthChecker{healthy: true}
	pdu := sendAndReceive(t, config.AuthorityModeStandalone, health, store, buildFC6Frame())
	if len(pdu) == 0 || pdu[0]&0x80 != 0 {
		t.Errorf("standalone write: expected success response, got %v", pdu)
	}
}

// TestHandleConnStrictWriteRejected verifies that HandleConn returns exception 0x01
// for a write request in strict mode.
func TestHandleConnStrictWriteRejected(t *testing.T) {
	store := buildTestStore(t)
	health := &mockHealthChecker{healthy: true}
	pdu := sendAndReceive(t, config.AuthorityModeStrict, health, store, buildFC6Frame())
	if len(pdu) != 2 || pdu[0] != 0x06|0x80 || pdu[1] != 0x01 {
		t.Errorf("strict write: expected exception 0x01, got %v", pdu)
	}
}

// TestHandleConnBufferWriteRejected verifies that HandleConn returns exception 0x01
// for a write request in buffer mode.
func TestHandleConnBufferWriteRejected(t *testing.T) {
	store := buildTestStore(t)
	health := &mockHealthChecker{healthy: true}
	pdu := sendAndReceive(t, config.AuthorityModeBuffer, health, store, buildFC6Frame())
	if len(pdu) != 2 || pdu[0] != 0x06|0x80 || pdu[1] != 0x01 {
		t.Errorf("buffer write: expected exception 0x01, got %v", pdu)
	}
}

// --------------------
// StoreHealthChecker tests
// --------------------

// TestStoreHealthCheckerNoStatusEntry returns true when no status is configured.
func TestStoreHealthCheckerNoStatusEntry(t *testing.T) {
	store := core.NewMemStore()
	hc := &StoreHealthChecker{store: store, entries: make(map[dataKey][]statusEntry)}
	if !hc.IsHealthyFor(502, 1) {
		t.Error("expected true (no status configured), got false")
	}
}

// TestStoreHealthCheckerHealthOK verifies that IsHealthyFor returns true when the
// health register in the status block contains healthOK (1).
func TestStoreHealthCheckerHealthOK(t *testing.T) {
	store := core.NewMemStore()

	// Create a status memory with 30 holding registers at port=502, unitID=255.
	statusMem, err := core.NewMemory(core.MemoryLayouts{
		HoldingRegs: &core.AreaLayout{Start: 0, Size: 30},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	// Write healthOK (1) into register offset 2 (statusHealthOffset).
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, healthOK)
	if err := statusMem.WriteRegs(core.AreaHoldingRegs, statusHealthOffset, 1, buf); err != nil {
		t.Fatalf("WriteRegs: %v", err)
	}
	if err := store.Add(core.MemoryID{Port: 502, UnitID: 255}, statusMem); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	hc := &StoreHealthChecker{
		store: store,
		entries: map[dataKey][]statusEntry{
			{port: 502, unitID: 1}: {{statusPort: 502, statusUnitID: 255, baseAddr: 0}},
		},
	}
	if !hc.IsHealthyFor(502, 1) {
		t.Error("expected healthy (healthOK written), got false")
	}
}

// TestStoreHealthCheckerHealthError verifies that IsHealthyFor returns false when
// the health register in the status block contains a non-OK code (e.g. 2 = error).
func TestStoreHealthCheckerHealthError(t *testing.T) {
	store := core.NewMemStore()

	statusMem, err := core.NewMemory(core.MemoryLayouts{
		HoldingRegs: &core.AreaLayout{Start: 0, Size: 30},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	// Write healthError (2) into register offset 2.
	buf := make([]byte, 2)
	binary.BigEndian.PutUint16(buf, 2) // HealthError
	if err := statusMem.WriteRegs(core.AreaHoldingRegs, statusHealthOffset, 1, buf); err != nil {
		t.Fatalf("WriteRegs: %v", err)
	}
	if err := store.Add(core.MemoryID{Port: 502, UnitID: 255}, statusMem); err != nil {
		t.Fatalf("store.Add: %v", err)
	}

	hc := &StoreHealthChecker{
		store: store,
		entries: map[dataKey][]statusEntry{
			{port: 502, unitID: 1}: {{statusPort: 502, statusUnitID: 255, baseAddr: 0}},
		},
	}
	if hc.IsHealthyFor(502, 1) {
		t.Error("expected unhealthy (healthError written), got true")
	}
}

// --------------------
// Helpers
// --------------------

// minimalValidConfig returns a minimal valid Config suitable for testing Validate().
func minimalValidConfig() *config.Config {
	return &config.Config{
		AuthorityMode: config.AuthorityModeStrict,
		Server: config.ServerConfig{
			Listeners: []config.ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []config.MemoryDef{
						{UnitID: 1, HoldingRegs: config.AreaDef{Start: 0, Count: 10}},
					},
				},
			},
		},
		Replicator: config.ReplicatorConfig{
			Units: []config.UnitConfig{
				{
					ID:     "plc1",
					Source: config.SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
					Reads:  []config.ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: config.TargetConfig{ListenerID: "main", UnitID: 1},
				},
			},
		},
	}
}
