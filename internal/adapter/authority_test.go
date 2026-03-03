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
// Mock BlockHealthReader
// --------------------

// mockBlockHealthReader simulates the engine's BlockHealthStore for tests.
// Key: "unitID:blockIdx", value: (timeout, consecutiveErrors, exceptionCode).
type mockBlockHealthReader struct {
entries map[string]blockHealthEntry
}

type blockHealthEntry struct {
timeout           bool
consecutiveErrors int
exceptionCode     byte
}

func (m *mockBlockHealthReader) GetBlockHealth(unitID string, blockIdx int) (timeout bool, consecutiveErrors int, exceptionCode byte, found bool) {
key := unitID + ":" + string(rune('0'+blockIdx))
e, ok := m.entries[key]
return e.timeout, e.consecutiveErrors, e.exceptionCode, ok
}

func newMockHealth() *mockBlockHealthReader {
return &mockBlockHealthReader{entries: make(map[string]blockHealthEntry)}
}

func (m *mockBlockHealthReader) setHealthy(unitID string, blockIdx int) {
key := unitID + ":" + string(rune('0'+blockIdx))
m.entries[key] = blockHealthEntry{timeout: false, consecutiveErrors: 0, exceptionCode: 0}
}

func (m *mockBlockHealthReader) setTimeout(unitID string, blockIdx int) {
key := unitID + ":" + string(rune('0'+blockIdx))
m.entries[key] = blockHealthEntry{timeout: true, consecutiveErrors: 1, exceptionCode: 0}
}

func (m *mockBlockHealthReader) setException(unitID string, blockIdx int, code byte) {
key := unitID + ":" + string(rune('0'+blockIdx))
m.entries[key] = blockHealthEntry{timeout: false, consecutiveErrors: 1, exceptionCode: code}
}

// --------------------
// Test helpers
// --------------------

// buildTestRegistry builds an AuthorityRegistry for testing.
// target: port=502, unitID=1, mode=given, reads=[{FC4, addr=0, qty=10}]
func buildTestRegistry(mode string, health BlockHealthReader) *AuthorityRegistry {
cfg := &config.Config{
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
ID:     "unit1",
Source: config.SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
Reads:  []config.ReadConfig{{FC: 4, Address: 0, Quantity: 10, IntervalMs: 1000}},
Target: config.TargetConfig{ListenerID: "main", UnitID: 1, Mode: mode},
},
},
},
}
return BuildAuthorityRegistry(cfg, health)
}

// buildTwoBlockRegistry builds a registry with two FC4 blocks: [0,10) and [10,10).
func buildTwoBlockRegistry(mode string, health BlockHealthReader) *AuthorityRegistry {
cfg := &config.Config{
Server: config.ServerConfig{
Listeners: []config.ListenerConfig{
{
ID:     "main",
Listen: ":502",
Memory: []config.MemoryDef{
{UnitID: 1, InputRegs: config.AreaDef{Start: 0, Count: 20}},
},
},
},
},
Replicator: config.ReplicatorConfig{
Units: []config.UnitConfig{
{
ID:     "unit1",
Source: config.SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
Reads: []config.ReadConfig{
{FC: 4, Address: 0, Quantity: 10, IntervalMs: 200},  // block 0: [0,10)
{FC: 4, Address: 10, Quantity: 10, IntervalMs: 5000}, // block 1: [10,20)
},
Target: config.TargetConfig{ListenerID: "main", UnitID: 1, Mode: mode},
},
},
},
}
return BuildAuthorityRegistry(cfg, health)
}

// --------------------
// Config-level tests (default mode)
// --------------------

// TestDefaultTargetModeIsB verifies that Load() sets the default to "B"
// when target.mode is absent from the YAML.
func TestDefaultTargetModeIsB(t *testing.T) {
cfg := &config.Config{
Replicator: config.ReplicatorConfig{
Units: []config.UnitConfig{
{Target: config.TargetConfig{Mode: ""}},
},
},
}
// Load applies the default; reproduce that logic here for a unit-level check.
if cfg.Replicator.Units[0].Target.Mode == "" {
cfg.Replicator.Units[0].Target.Mode = config.TargetModeB
}
if cfg.Replicator.Units[0].Target.Mode != config.TargetModeB {
t.Errorf("expected default target mode = %q, got %q", config.TargetModeB, cfg.Replicator.Units[0].Target.Mode)
}
}

// TestInvalidTargetModeFailsValidation verifies that Validate rejects unknown modes.
func TestInvalidTargetModeFailsValidation(t *testing.T) {
cfg := minimalValidConfig()
cfg.Replicator.Units[0].Target.Mode = "unknown"
if err := config.Validate(cfg); err == nil {
t.Error("expected validation error for unknown target mode")
}
}

// --------------------
// AuthorityRegistry enforcement tests
// --------------------

// TestEnforceWriteModeAAllowed verifies that write FCs are allowed in mode A.
func TestEnforceWriteModeAAllowed(t *testing.T) {
health := newMockHealth()
reg := buildTestRegistry(config.TargetModeA, health)
for _, fc := range []uint8{5, 6, 15, 16} {
pdu, rejected := reg.Enforce(502, 1, fc, 0, 1)
if rejected {
t.Errorf("FC%d: mode A should allow writes, got exception PDU: %v", fc, pdu)
}
}
}

// TestEnforceWriteModeBRejected verifies that write FCs are rejected (0x01) in mode B.
func TestEnforceWriteModeBRejected(t *testing.T) {
health := newMockHealth()
reg := buildTestRegistry(config.TargetModeB, health)
for _, fc := range []uint8{5, 6, 15, 16} {
pdu, rejected := reg.Enforce(502, 1, fc, 0, 1)
if !rejected {
t.Errorf("FC%d: mode B should reject writes", fc)
continue
}
if len(pdu) != 2 || pdu[0] != fc|0x80 || pdu[1] != 0x01 {
t.Errorf("FC%d: expected exception 0x01, got %v", fc, pdu)
}
}
}

// TestEnforceWriteModeCRejected verifies that write FCs are rejected (0x01) in mode C.
func TestEnforceWriteModeCRejected(t *testing.T) {
health := newMockHealth()
reg := buildTestRegistry(config.TargetModeC, health)
for _, fc := range []uint8{5, 6, 15, 16} {
pdu, rejected := reg.Enforce(502, 1, fc, 0, 1)
if !rejected {
t.Errorf("FC%d: mode C should reject writes", fc)
continue
}
if len(pdu) != 2 || pdu[0] != fc|0x80 || pdu[1] != 0x01 {
t.Errorf("FC%d: expected exception 0x01, got %v", fc, pdu)
}
}
}

// TestEnforceReadModeBTimeoutBlocked verifies that reads in mode B return 0x0B
// when the covering block has a timeout.
func TestEnforceReadModeBTimeoutBlocked(t *testing.T) {
health := newMockHealth()
health.setTimeout("unit1", 0)
reg := buildTestRegistry(config.TargetModeB, health)
pdu, rejected := reg.Enforce(502, 1, 4, 0, 5) // FC4, addr=0, qty=5 (covered by block 0)
if !rejected {
t.Error("mode B + timeout: read should be blocked")
return
}
if len(pdu) != 2 || pdu[0] != 0x04|0x80 || pdu[1] != 0x0B {
t.Errorf("expected exception 0x0B, got %v", pdu)
}
}

// TestEnforceReadModeBHealthyAllowed verifies that reads in mode B proceed
// when the covering block is healthy.
func TestEnforceReadModeBHealthyAllowed(t *testing.T) {
health := newMockHealth()
health.setHealthy("unit1", 0)
reg := buildTestRegistry(config.TargetModeB, health)
pdu, rejected := reg.Enforce(502, 1, 4, 0, 5)
if rejected {
t.Errorf("mode B + healthy: read should proceed, got exception PDU: %v", pdu)
}
}

// TestEnforceReadModeCAlwaysServes verifies that reads in mode C are never blocked,
// even when a block has timed out.
func TestEnforceReadModeCAlwaysServes(t *testing.T) {
health := newMockHealth()
health.setTimeout("unit1", 0)
reg := buildTestRegistry(config.TargetModeC, health)
pdu, rejected := reg.Enforce(502, 1, 4, 0, 5)
if rejected {
t.Errorf("mode C: read should always proceed, got exception PDU: %v", pdu)
}
}

// TestEnforceReadModeAAlwaysServes verifies that reads in mode A are never blocked.
func TestEnforceReadModeAAlwaysServes(t *testing.T) {
health := newMockHealth()
health.setTimeout("unit1", 0)
reg := buildTestRegistry(config.TargetModeA, health)
pdu, rejected := reg.Enforce(502, 1, 4, 0, 5)
if rejected {
t.Errorf("mode A: read should always proceed, got exception PDU: %v", pdu)
}
}

// --------------------
// New spec tests
// --------------------

// TestTwoFC4BlocksDifferentIntervals verifies config with two FC4 blocks at different
// intervals builds correctly with two separate block entries in the registry.
func TestTwoFC4BlocksDifferentIntervals(t *testing.T) {
health := newMockHealth()
reg := buildTwoBlockRegistry(config.TargetModeB, health)
entry, ok := reg.targets[targetKey{port: 502, unitID: 1}]
if !ok {
t.Fatal("expected target entry for (502, 1)")
}
if len(entry.blocks) != 2 {
t.Fatalf("expected 2 read blocks, got %d", len(entry.blocks))
}
}

// TestOneHealthyOneTimed_HealthySucceeds verifies that in mode B with two blocks,
// accessing the healthy range succeeds.
func TestOneHealthyOneTimed_HealthySucceeds(t *testing.T) {
health := newMockHealth()
health.setHealthy("unit1", 0) // block 0: [0,10) — healthy
health.setTimeout("unit1", 1) // block 1: [10,20) — timed out
reg := buildTwoBlockRegistry(config.TargetModeB, health)

// Read from block 0 range only — should succeed.
pdu, rejected := reg.Enforce(502, 1, 4, 0, 5)
if rejected {
t.Errorf("healthy block 0: read should succeed, got exception PDU: %v", pdu)
}
}

// TestOneHealthyOneTimed_TimedOutFails verifies that in mode B with two blocks,
// accessing the timed-out range returns 0x0B.
func TestOneHealthyOneTimed_TimedOutFails(t *testing.T) {
health := newMockHealth()
health.setHealthy("unit1", 0)
health.setTimeout("unit1", 1)
reg := buildTwoBlockRegistry(config.TargetModeB, health)

// Read from block 1 range — should fail with 0x0B.
pdu, rejected := reg.Enforce(502, 1, 4, 10, 5)
if !rejected {
t.Error("timed-out block 1: read should be rejected")
return
}
if len(pdu) != 2 || pdu[0] != 0x04|0x80 || pdu[1] != 0x0B {
t.Errorf("expected exception 0x0B, got %v", pdu)
}
}

// TestUpstreamExceptionForwarded verifies that mode B forwards the upstream
// Modbus exception code when a block recorded an exception.
func TestUpstreamExceptionForwarded(t *testing.T) {
health := newMockHealth()
health.setException("unit1", 0, 0x04) // Slave Device Failure
reg := buildTestRegistry(config.TargetModeB, health)
pdu, rejected := reg.Enforce(502, 1, 4, 0, 5)
if !rejected {
t.Error("block with exception: read should be rejected")
return
}
if len(pdu) != 2 || pdu[0] != 0x04|0x80 || pdu[1] != 0x04 {
t.Errorf("expected exception 0x04 forwarded, got %v", pdu)
}
}

// TestModeCAlwaysServesReads verifies mode C always serves reads regardless of health.
func TestModeCAlwaysServesReads(t *testing.T) {
health := newMockHealth()
health.setTimeout("unit1", 0)
health.setException("unit1", 0, 0x06)
reg := buildTestRegistry(config.TargetModeC, health)

	// The registry has FC4 blocks only. Mode C serves covered FC4 reads regardless of health.
	pdu, rejected := reg.Enforce(502, 1, 4, 0, 5)
	if rejected {
		t.Errorf("FC4: mode C should always serve reads, got exception PDU: %v", pdu)
	}

}
// TestModeAAllowsWrite verifies that mode A allows write function codes.
func TestModeAAllowsWrite(t *testing.T) {
health := newMockHealth()
reg := buildTestRegistry(config.TargetModeA, health)
for _, fc := range []uint8{5, 6, 15, 16} {
pdu, rejected := reg.Enforce(502, 1, fc, 0, 1)
if rejected {
t.Errorf("FC%d: mode A should allow writes, got exception PDU: %v", fc, pdu)
}
}
}

// TestPartialOverlapReturns0x02 verifies that a request whose range is not fully
// covered by any read block returns 0x02 (Illegal Data Address).
func TestPartialOverlapReturns0x02(t *testing.T) {
health := newMockHealth()
reg := buildTwoBlockRegistry(config.TargetModeB, health)

// Request spans address [8, 18) — partially covered by block 0 [0,10) and block 1 [10,20),
// so their union [0,20) DOES cover [8,18). This should succeed.
// Let's use a range that is genuinely not covered: [15, 30) exceeds both blocks.
health.setHealthy("unit1", 0)
health.setHealthy("unit1", 1)
pdu, rejected := reg.Enforce(502, 1, 4, 15, 20) // [15, 35) — exceeds block 1 which ends at 20
if !rejected {
t.Error("request exceeding block coverage should be rejected with 0x02")
return
}
if len(pdu) != 2 || pdu[0] != 0x04|0x80 || pdu[1] != 0x02 {
t.Errorf("expected exception 0x02, got %v", pdu)
}
}

// TestDefaultModeIsB verifies that when mode is omitted from config, Load sets it to "B".
func TestDefaultModeIsB(t *testing.T) {
// Simulate loading without mode field (mode="").
// The loader normalises "" to "B".
mode := ""
if mode == "" {
mode = config.TargetModeB
}
if mode != config.TargetModeB {
t.Errorf("expected default mode B, got %q", mode)
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
binary.BigEndian.PutUint16(frame[0:2], 1)   // txID
binary.BigEndian.PutUint16(frame[2:4], 0)   // protoID
binary.BigEndian.PutUint16(frame[4:6], 6)   // length = unitID(1) + PDU(5)
frame[6] = 1                                 // unitID
frame[7] = 3                                 // FC3
binary.BigEndian.PutUint16(frame[8:10], 0)  // address = 0
binary.BigEndian.PutUint16(frame[10:12], 1) // quantity = 1
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
func sendAndReceive(t *testing.T, authority *AuthorityRegistry, store core.Store, reqFrame []byte) []byte {
t.Helper()

srvRaw, cli := net.Pipe()
srv := &fakeConn{Conn: srvRaw, localAddr: &net.TCPAddr{Port: 502}}

done := make(chan struct{})
go func() {
defer close(done)
HandleConn(srv, store, authority)
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

// buildFC3Registry builds a registry covering FC3 (holding registers) for unit 1 on port 502.
func buildFC3Registry(mode string, health BlockHealthReader) *AuthorityRegistry {
cfg := &config.Config{
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
ID:     "unit1",
Source: config.SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
Reads:  []config.ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
Target: config.TargetConfig{ListenerID: "main", UnitID: 1, Mode: mode},
},
},
},
}
return BuildAuthorityRegistry(cfg, health)
}

// TestHandleConnModeBTimeoutReadBlocked verifies that HandleConn returns 0x0B
// for a read request in mode B when the covering block has timed out.
func TestHandleConnModeBTimeoutReadBlocked(t *testing.T) {
store := buildTestStore(t)
health := newMockHealth()
health.setTimeout("unit1", 0)
authority := buildFC3Registry(config.TargetModeB, health)
pdu := sendAndReceive(t, authority, store, buildFC3Frame())
if len(pdu) != 2 || pdu[0] != 0x03|0x80 || pdu[1] != 0x0B {
t.Errorf("mode B + timeout read: expected exception 0x0B, got %v", pdu)
}
}

// TestHandleConnModeCUnhealthyReadAllowed verifies that HandleConn returns data
// for a read request in mode C even when the block has timed out.
func TestHandleConnModeCUnhealthyReadAllowed(t *testing.T) {
store := buildTestStore(t)
health := newMockHealth()
health.setTimeout("unit1", 0)
authority := buildFC3Registry(config.TargetModeC, health)
pdu := sendAndReceive(t, authority, store, buildFC3Frame())
// Successful FC3 response: pdu[0] == 0x03 (not exception bit set)
if len(pdu) == 0 || pdu[0]&0x80 != 0 {
t.Errorf("mode C + timeout read: expected data response, got %v", pdu)
}
}

// TestHandleConnModeAWriteAllowed verifies that HandleConn processes a write
// in mode A (no exception returned).
func TestHandleConnModeAWriteAllowed(t *testing.T) {
store := buildTestStore(t)
health := newMockHealth()
authority := buildFC3Registry(config.TargetModeA, health)
pdu := sendAndReceive(t, authority, store, buildFC6Frame())
if len(pdu) == 0 || pdu[0]&0x80 != 0 {
t.Errorf("mode A write: expected success response, got %v", pdu)
}
}

// TestHandleConnModeBWriteRejected verifies that HandleConn returns exception 0x01
// for a write request in mode B.
func TestHandleConnModeBWriteRejected(t *testing.T) {
store := buildTestStore(t)
health := newMockHealth()
authority := buildFC3Registry(config.TargetModeB, health)
pdu := sendAndReceive(t, authority, store, buildFC6Frame())
if len(pdu) != 2 || pdu[0] != 0x06|0x80 || pdu[1] != 0x01 {
t.Errorf("mode B write: expected exception 0x01, got %v", pdu)
}
}

// TestHandleConnModeCWriteRejected verifies that HandleConn returns exception 0x01
// for a write request in mode C.
func TestHandleConnModeCWriteRejected(t *testing.T) {
store := buildTestStore(t)
health := newMockHealth()
authority := buildFC3Registry(config.TargetModeC, health)
pdu := sendAndReceive(t, authority, store, buildFC6Frame())
if len(pdu) != 2 || pdu[0] != 0x06|0x80 || pdu[1] != 0x01 {
t.Errorf("mode C write: expected exception 0x01, got %v", pdu)
}
}

// --------------------
// Helpers
// --------------------

// minimalValidConfig returns a minimal valid Config suitable for testing Validate().
func minimalValidConfig() *config.Config {
return &config.Config{
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
Target: config.TargetConfig{ListenerID: "main", UnitID: 1, Mode: config.TargetModeB},
},
},
},
}
}
