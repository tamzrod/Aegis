# Aegis — Architecture Reference

## High-Level System Overview

Aegis is a single Go binary that fuses two roles:

1. **Modbus TCP server** — accepts inbound client connections and serves register and coil data from an in-process memory store.
2. **Replication engine** — polls one or more upstream Modbus devices on a configured interval and writes the read values directly into the same in-process memory store.

The critical constraint is that the engine and the server share memory **in-process**. There is no internal TCP loopback, no channel serialization between engine and server, and no secondary Modbus client-to-server connection inside the binary. The engine calls `Memory.WriteRegs` / `Memory.WriteBits` as ordinary function calls; the server calls `Memory.ReadRegs` / `Memory.ReadBits` against the same `*Memory` pointer. An `sync.RWMutex` embedded in each `Memory` instance provides concurrency safety between the two.

```
Upstream device ──Modbus TCP──► Poller (engine)
                                     │ store.MustGet → mem.WriteRegs/WriteBits
                                     ▼
                              core.MemStore (in-process)
                                     ▲
                         store.MustGet → mem.ReadRegs/ReadBits
                                     │
Modbus TCP client ◄──Modbus TCP──── Server adapter (adapter)
```

---

## Component Breakdown

### `internal/core`

The authoritative in-process memory layer. It has no knowledge of configuration, network protocols, or the replication engine.

| File | Responsibility |
|---|---|
| `store.go` | `Store` interface (`Get`, `MustGet`) and `MemStore` implementation (map of `MemoryID → *Memory`, `sync.RWMutex`). |
| `memory_id.go` | `MemoryID` struct (`Port uint16`, `UnitID uint16`) — the composite key used to look up a `Memory` in the store. |
| `memory.go` | `Memory` struct — holds four independent address-space slices (coils `[]byte`, discrete inputs `[]byte`, holding regs `[]uint16`, input regs `[]uint16`). Exposes `ReadBits`, `WriteBits`, `ReadRegs`, `WriteRegs`. Every method acquires `m.mu` (read lock for reads, write lock for writes). |
| `area.go` | `Area` type — typed constants `AreaCoils (1)`, `AreaDiscreteInputs (2)`, `AreaHoldingRegs (3)`, `AreaInputRegs (4)`. |
| `layout.go` | `AreaLayout` struct (`Start`, `Size`). Provides `Contains(address, count) bool` and `Offset(address) uint16` used by `memory.go` to perform bounds checking and to translate a Modbus zero-based address into a slice index. |
| `bits.go` | `copyBits` / `writeBits` helpers for LSB-first Modbus bit packing. |
| `errors.go` | Sentinel errors (`ErrNilStore`, `ErrNilMemory`, `ErrOutOfBounds`, etc.). |
| `state_sealing.go` | `StateSealingDef` struct (Area + Address) and `SetStateSealing` / `StateSealing` accessors. Metadata only — no enforcement here. |

**`Store` interface** (the architectural seam):

```go
type Store interface {
    Get(id MemoryID) (*Memory, bool)
    MustGet(id MemoryID) (*Memory, error)
}
```

Both the server adapter and the replication engine depend on this interface. Neither depends on the other.

---

### `internal/config`

Loads, validates, and materialises the YAML configuration file. It also contains the factory (`BuildMemStore`) that converts the validated config into a live `core.MemStore`.

| File | Responsibility |
|---|---|
| `config.go` | Go struct tree that mirrors the YAML schema: `Config`, `ReplicatorConfig`, `UnitConfig`, `SourceConfig`, `ReadConfig`, `TargetConfig`. The `ServerConfig`/`ListenerConfig`/`MemoryDef` hierarchy has been removed; listeners and memory are now derived at runtime. |
| `loader.go` | `Load(path string) (*Config, error)` — reads the file with `os.ReadFile` and unmarshals with `gopkg.in/yaml.v3`. No validation. |
| `validate.go` | `Validate(cfg *Config) error` — structural and constraint checks. Does not mutate the config. |
| `build_store.go` | `BuildMemStore(cfg *Config) (*core.MemStore, error)` — derives data memory surfaces from read bounding ranges per `(port, unit_id, FC)` and status memory surfaces from `(port, status_unit_id)` with size `(max_slot+1)*30` registers. |
| `helpers.go` | `ParseListenPort(listen string) (uint16, error)` — shared helper. |

---

### `internal/engine`

The replication engine. Polls upstream Modbus devices and writes data directly into the shared `core.Store`.

| File | Responsibility |
|---|---|
| `types.go` | `ReadBlock`, `BlockResult`, `PollResult`, `TransportCounters`. |
| `poller.go` | `Poller` struct and `PollOnce()`. Manages a `Client` interface (lazy connect via factory on first tick or after dead-connection invalidation). |
| `runner.go` | `Poller.Run(ctx, out chan<- PollResult)` — drives the ticker loop, sends each `PollResult` to the `out` channel. |
| `writer.go` | `StoreWriter` and `WritePlan`. `Write(PollResult)` calls `store.MustGet` then `mem.WriteRegs`/`mem.WriteBits` directly. `WriteStatus(StatusSnapshot)` writes a 30-register status block. |
| `status.go` | `StatusSnapshot`, health code constants, `encodeStatusBlock`. Fixed 30-register layout per device. |
| `builder.go` | `Build(cfg, store) ([]Unit, error)` — constructs all `Poller` + `StoreWriter` pairs from config. |
| `modbusclient/` | Thin wrapper around `github.com/tamzrod/modbus` that satisfies the `Client` interface. |

**`Client` interface** (the upstream transport seam):

```go
type Client interface {
    ReadCoils(addr, qty uint16) ([]bool, error)
    ReadDiscreteInputs(addr, qty uint16) ([]bool, error)
    ReadHoldingRegisters(addr, qty uint16) ([]uint16, error)
    ReadInputRegisters(addr, qty uint16) ([]uint16, error)
}
```

---

### `internal/adapter`

Modbus TCP server. Pure transport adapter — no logic, no state beyond the connection loop.

| File | Responsibility |
|---|---|
| `server.go` | `Server` struct. `ListenAndServe()` — `net.Listen("tcp", ...)`, accept loop, `go HandleConn(conn, store)` per connection. |
| `handler.go` | `HandleConn(conn, store)` — per-connection read loop. Derives `Port` from `conn.LocalAddr()`. Calls `DispatchMemory`. |
| `authority.go` | `AuthorityRegistry` and `BuildAuthorityRegistry`. Enforces per-target mode (A/B/C), bounding-range checks, hole logic (within bounding range but not covered by a segment: B→0x02, A/C→serve zeros), unprimed block gating (B→0x0B), and upstream exception forwarding. |
| `parser.go` | `ReadRequest(conn, port) (*Request, error)` — reads and decodes the 6-byte MBAP header, then the PDU. |
| `request.go` | `Request` struct (`TransactionID`, `UnitID`, `Port`, `FunctionCode`, `Payload`). |
| `response.go` | `BuildResponse(req, pdu) []byte` — assembles the MBAP response frame. |
| `dispatch.go` | `DispatchMemory(store, req) []byte` — routes FC 1/2/3/4/5/6/15/16 to the appropriate `core.Memory` method. |
| `pdu_decode.go` | `DecodeReadRequest`, `DecodeWriteSingle`, `DecodeWriteMultipleBits`, `DecodeWriteMultiple` — PDU payload decoders. |
| `pdu_encode.go` | `BuildReadResponsePDU`, `BuildWriteSingleResponsePDU`, `BuildWriteMultipleResponsePDU`, `BuildExceptionPDU`. |
| `pdu_types.go` | Decoded payload structs. |

---

### `cmd/aegis`

The binary entry point. Contains only boot orchestration — no business logic.

| File | Responsibility |
|---|---|
| `main.go` | Boot sequence, goroutine launch, signal handling, shutdown. |

---

## Dependency Direction

```
cmd/aegis
  ├── internal/config   (Load, Validate, BuildMemStore, Build)
  ├── internal/engine   (Build, Unit, Poller, StoreWriter, PollResult, StatusSnapshot)
  └── internal/adapter  (NewServer)

internal/config
  └── internal/core     (MemStore, Memory, MemoryID, AreaLayout, etc.)

internal/engine
  ├── internal/core     (Store, Memory, MemoryID, Area constants)
  ├── internal/config   (Config, UnitConfig, ParseListenPort — builder only)
  └── internal/engine/modbusclient  (Client impl)

internal/adapter
  └── internal/core     (Store, Memory, MemoryID, Area constants)

internal/core
  (no internal dependencies)
```

`internal/adapter` and `internal/engine` both import `internal/core` but **do not import each other**. The `core.Store` interface is the only coupling point between them.

---

## Boot Sequence Flow

```
main()
  │
  ├─1─ os.Args[1] — config file path required; missing → log.Fatal
  │
  ├─2─ config.Load(path)
  │      os.ReadFile → yaml.Unmarshal → *Config
  │      failure → log.Fatalf("config load failed: %v", err)
  │
  ├─3─ config.Validate(cfg)
  │      structural and constraint checks
  │      failure → log.Fatalf("config validation failed: %v", err)
  │
  ├─4─ config.BuildMemStore(cfg)
  │      for each (port, unit_id) derived from replicator reads:
  │        compute bounding range per FC → core.NewMemory(layouts)
  │        store.Add(MemoryID{port, unitID}, mem)
  │      for each (port, status_unit_id) with status_slot:
  │        allocate (max_slot+1)*30 holding registers
  │        store.Add(MemoryID{port, statusUnitID}, mem)
  │      failure → log.Fatalf("memory store build failed: %v", err)
  │
  ├─5─ engine.Build(cfg, store)
  │      for each replicator unit:
  │        builds Poller (lazy connect, no initial dial)
  │        builds WritePlan → StoreWriter (uses target.Port directly)
  │      failure → log.Fatalf("engine build failed: %v", err)
  │
  ├─6─ for each unique target.Port across all replicator units:
  │      adapter.NewServer(":PORT", store)
  │      go srv.ListenAndServe()   ← blocks inside on net.Listen + accept loop
  │      failure inside goroutine → log.Fatalf
  │
  ├─7─ for each engine.Unit:
  │      out := make(chan PollResult, 8)
  │      go orchestrator(out)      ← consumes results, calls writer.Write + writer.WriteStatus
  │      go poller.Run(ctx, out)   ← ticker loop, calls PollOnce every interval
  │
  └─8─ signal.Notify(quit, SIGINT, SIGTERM)
         <-quit → cancel() → all goroutines exit via ctx.Done()
```

Steps 2–5 all run synchronously on the main goroutine before any network or goroutine is created. Any failure in steps 2–5 calls `log.Fatal` / `log.Fatalf` and terminates the process before listeners or pollers start.

---

## How Replication Writes to Memory

Each `engine.Unit` is driven by two goroutines:

**Poller goroutine** (`Poller.Run`):
```
time.NewTicker(interval)
  │  tick fires
  └─► PollOnce()
        if client == nil: factory() → modbusclient.New (TCP dial)
        for each ReadBlock:
          client.ReadCoils / ReadDiscreteInputs / ReadHoldingRegisters / ReadInputRegisters
          on dead-connection error: client.Close(); client = nil
        returns PollResult{Blocks, Err, At}
        sends PollResult → out channel (buffered, cap 8)
```

**Orchestrator goroutine** (in `main`):
```
select {
  case res := <-out:
    writer.Write(res)          // data write
    writer.WriteStatus(snap)   // status write (if changed)

  case <-secTicker.C:
    if snap.Health != HealthOK: snap.SecondsInError++
    writer.WriteStatus(snap)
}
```

**`StoreWriter.Write(res PollResult)`**:
- If `res.Err != nil`: no-op (stale data is never written on a failed poll).
- For each `TargetMemory` in the `WritePlan`:
  - `store.MustGet(tgt.MemoryID)` — retrieves the `*Memory` by `(port, unit_id)`.
  - For each `BlockResult` in `res.Blocks`:
    - FC 1: `packBits(b.Bits)` → `mem.WriteBits(AreaCoils, dstAddr, qty, packed)`
    - FC 2: `packBits(b.Bits)` → `mem.WriteBits(AreaDiscreteInputs, dstAddr, qty, packed)`
    - FC 3: `packRegisters(b.Registers)` → `mem.WriteRegs(AreaHoldingRegs, dstAddr, qty, packed)`
    - FC 4: `packRegisters(b.Registers)` → `mem.WriteRegs(AreaInputRegs, dstAddr, qty, packed)`
  - `dstAddr = offsetForFC(tgt.Offsets, b.FC) + b.Address` (per-FC offset delta from config, defaults to 0).

**`StoreWriter.WriteStatus(snap StatusSnapshot)`**:
- Looks up the status `*Memory` by `(port, statusUnitID)`.
- Calls `encodeStatusBlock(snap, deviceName)` → `[]uint16` (30 registers, fixed layout).
- Converts to big-endian bytes.
- Calls `mem.WriteRegs(AreaHoldingRegs, baseSlot*30, 30, src)`.

All writes ultimately resolve to:
```go
m.mu.Lock()
for i := 0; i < count; i++ {
    backing[off+i] = binary.BigEndian.Uint16(src[i*2 : i*2+2])
}
m.mu.Unlock()
```

---

## How the Modbus Server Reads from Memory

Each inbound TCP connection is handled by `HandleConn` in its own goroutine:

```
net.Listen("tcp", listen)
  └─► conn := ln.Accept()
        go HandleConn(conn, store)
```

**`HandleConn(conn, store)`**:
1. Derives `port` from `conn.LocalAddr().(*net.TCPAddr).Port`.
2. Loop:
   a. `ReadRequest(conn, port)` — reads 6-byte MBAP header (transaction ID, protocol ID, length, unit ID) then the PDU payload. Returns `*Request` with `Port` set.
   b. Constructs `core.MemoryID{Port: port, UnitID: uint16(req.UnitID)}`.
   c. **State sealing check**: if `store.Get(mid)` succeeds and `mem.StateSealing() != nil`:
      - `mem.ReadBits(seal.Area, seal.Address, 1, buf)` — reads the sealing flag coil.
      - If the low bit is 0 (sealed): sends exception PDU `0x06` (Device Busy) and continues to next request.
   d. `DispatchMemory(store, req)` → PDU bytes.
   e. `BuildResponse(req, pdu)` → MBAP frame.
   f. `conn.Write(frame)`.

**`DispatchMemory(store, req)`** for read FCs (1, 2, 3, 4):
```
resolveMemory(store, req)     // store.MustGet(MemoryID{Port, UnitID})
DecodeReadRequest(req.Payload) // address + quantity from PDU
buf := make([]byte, ...)
mem.ReadBits / mem.ReadRegs(area, address, quantity, buf)
BuildReadResponsePDU(fc, buf)
```

All reads ultimately resolve to:
```go
m.mu.RLock()
for i := 0; i < count; i++ {
    binary.BigEndian.PutUint16(dst[i*2:i*2+2], backing[off+i])
}
m.mu.RUnlock()
```

The `RWMutex` allows concurrent reads from multiple TCP clients while serialising against engine writes.

---

## Configuration Loading Behavior

Config loading is a two-step synchronous process that runs before any goroutine or network socket is created:

**Step 1 — `config.Load(path string) (*Config, error)`**:
- `os.ReadFile(path)` — reads the entire file into memory.
- `yaml.Unmarshal(data, &cfg)` — decodes YAML into the Go struct tree.
- Returns the populated `*Config` or an error. Does not validate field values.

**Step 2 — `config.Validate(cfg *Config) error`**:
- Requires at least one replicator unit.
- `validateReplicator`: checks unique unit IDs; `target.port` > 0; `target.unit_id` in [1, 255]; if `target.status_slot` is set then `target.status_unit_id` must also be set and differ from all data `unit_id` values on the same port; no duplicate `status_slot` on the same `(port, status_unit_id)`; no overlapping read ranges for the same `(port, unit_id, FC)` (write conflict check); positive `reads[*].interval_ms`.
- Does not mutate the config.

**`config.BuildMemStore(cfg *Config) (*core.MemStore, error)`**:
- **Data memory**: for each `(port, unit_id)` pair across all replicator units, collects all read blocks and computes a bounding range per FC (min start address, max end address). Allocates one `AreaLayout` per FC with the bounding range. Holes within the bounding range are not allocated separately but are served as zeros (mode A/C) or rejected with 0x02 (mode B) by the adapter authority.
- **Status memory**: for each `(port, status_unit_id)` pair, allocates `(max(status_slot) + 1) * 30` holding registers starting at address 0. Each slot occupies 30 consecutive registers at `slot * 30`.

The YAML file is loaded exactly once at startup. There is no hot-reload mechanism.

---

## Failure Behavior on Invalid Config

| Stage | Failure Condition | Process Behavior |
|---|---|---|
| No config path argument | `len(os.Args) < 2` | `log.Fatal("usage: aegis <config.yaml>")` → exit code 1 |
| File not readable | `os.ReadFile` returns error | `log.Fatalf("config load failed: read config file: %v", err)` → exit code 1 |
| YAML parse error | `yaml.Unmarshal` returns error | `log.Fatalf("config load failed: parse config yaml: %v", err)` → exit code 1 |
| Validation failure | `config.Validate` returns error | `log.Fatalf("config validation failed: %v", err)` → exit code 1 |
| Memory store build failure | `config.BuildMemStore` returns error | `log.Fatalf("aegis: memory store build failed: %v", err)` → exit code 1 |
| Engine build failure | `engine.Build` returns error | `log.Fatalf("aegis: engine build failed: %v", err)` → exit code 1 |
| Adapter listen failure | `net.Listen` fails inside goroutine | `log.Fatalf("aegis: adapter (%s) failed: %v", ...)` → exit code 1 |

In all cases the process terminates before serving any request. No goroutines, listeners, or pollers are started prior to completion of steps 2–5 in the boot sequence. Validation errors include the field path (e.g. `replicator.units[0] (plc1): target.port must be > 0`).

---

## External Dependencies

| Module | Version | Use |
|---|---|---|
| `github.com/tamzrod/modbus` | v0.1.1 | Modbus TCP client used by `internal/engine/modbusclient`. |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML config file parsing in `internal/config/loader.go`. |
