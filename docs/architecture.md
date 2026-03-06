# Aegis — Architecture Reference

## High-Level System Overview

Aegis is a single Go binary that supports two operational modes:

- **Normal mode** — full replication engine + Modbus TCP server, active when a valid configuration file is present.
- **WebUI-only mode** — configuration interface only, active when the configuration file is missing or invalid.

The WebUI is the primary entry point and always starts (when enabled or when no config is found). It allows the operator to configure the system and apply changes at runtime. The replication engine and Modbus TCP server only start after a valid configuration is loaded.

In normal mode Aegis fuses two roles:

1. **Modbus TCP server** — accepts inbound client connections and serves register and coil data from an in-process memory store.
2. **Replication engine** — polls one or more upstream Modbus devices on a configured interval and writes the read values directly into the same in-process memory store.

The critical constraint is that the engine and the server share memory **in-process**. There is no internal TCP loopback, no channel serialization between engine and server, and no secondary Modbus client-to-server connection inside the binary. The engine calls `Memory.WriteRegs` / `Memory.WriteBits` as ordinary function calls; the server calls `Memory.ReadRegs` / `Memory.ReadBits` against the same `*Memory` pointer. An `sync.RWMutex` embedded in each `Memory` instance provides concurrency safety between the two.

```
                              ┌─────────────────────────────────────────┐
                              │  WebUI (always available on port 8080)  │
                              │  /healthz  /status  /config             │
                              └──────────────┬──────────────────────────┘
                                             │ (config present & valid)
                                             ▼
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

## Startup Modes

Aegis supports three startup scenarios, determined at launch time:

| Invocation | Config file present? | Behavior |
|---|---|---|
| `aegis <config.yaml>` | Yes | Load specified file; start engine + WebUI (if `webui.enabled: true`). |
| `aegis` | Yes (`config.yaml` in working dir) | Load `config.yaml`; start engine + WebUI (if `webui.enabled: true`). |
| `aegis` | No `config.yaml` found | Create a minimal `config.yaml` (empty units list); start WebUI only on `:8080`; runtime (replicator + adapters) remains disabled until a valid config is applied. |

In all cases the binary does **not** terminate on a missing config. The missing-config state is treated as a first-boot condition, not a fatal error.

---

## First Boot Workflow

On a fresh device with no configuration file:

1. Start Aegis (no arguments): `./aegis`
2. Aegis automatically creates a minimal `config.yaml` (with an empty units list) in the working directory.
3. Open the WebUI at **http://localhost:8080**. Log in with the default credentials (`admin` / `admin`). You will be prompted to change the password on first login.
4. Use the UI to add devices (units) and define their read blocks.
5. Click **"Apply Config"** to save the configuration and write `config.yaml` to disk.
6. The runtime (replicator + Modbus TCP adapters) starts automatically; the gateway begins serving Modbus data.

No manual file editing or process restart is required for initial setup.

---

## Degraded Mode

Aegis enters degraded mode when the configuration file is missing or contains invalid YAML / fails validation:

- **WebUI remains running** — accessible at the configured listen address (default `:8080`).
- **Runtime is disabled** — no replicator units are started, no Modbus TCP server is listening.
- **Error information is surfaced** — the WebUI `/status` endpoint reports the error so the operator can diagnose and correct it.

Degraded mode is recoverable: once a valid configuration is applied via the WebUI, the runtime starts without requiring a process restart.

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

The binary entry point. Coordinates boot orchestration and hosts the `RuntimeManager`, which wires all subsystems together and exposes a unified interface to the WebUI.

| File | Responsibility |
|---|---|
| `main.go` | Boot sequence, config load, goroutine launch, OS signal handling, shutdown. |
| `runtime.go` | `RuntimeManager` struct definition, construction (`NewRuntimeManager`), and the internal `rebuild` engine that atomically stops/starts the running components. |
| `runtime_lifecycle.go` | External lifecycle operations: `StopRuntime`, `StartRuntime`, `Stop` (graceful shutdown), `RuntimeStatus`, `ListenerStatuses`. |
| `runtime_status.go` | Observability: `DeviceStatuses`, `ReadDeviceStatus`, `ReadViewerRegisters`, and supporting helpers (`healthCodeToString`, `deriveDeviceStatus`, `isDevicePolling`). |
| `runtime_config.go` | Config management: `ApplyConfig`, `ReloadFromDisk`, `UpdatePasswordHash`, `GetActiveConfigYAML`, and the shared `atomicWriteConfig` helper. |
| `orchestrator.go` | Per-unit poll-result consumption loop. Drives the `secTicker` and enforces the write-change policy (status written only when snapshot differs). |
| `health.go` | Per-read-block health state mutation: `updateBlockHealth`. |
| `snapshot.go` | Pure snapshot transforms: `applyPollResult` and `applyCounters`. Value-in / value-out, no side effects. |
| `latency.go` | `PollLatencyTracker` — records per-unit last/avg/max poll latency. Passive-only; does not influence control flow. |
| `views.go` | `runtimeView` and `configView` adapters that satisfy the `view.RuntimeView` and `view.ConfigView` interfaces. |
| `doc.go` | Package-level godoc: file organisation and dependency direction. |

---

## Dependency Direction

```
cmd/aegis
  ├── internal/config   (Load, Validate, BuildMemStore, Build)
  ├── internal/engine   (Build, Unit, Poller, StoreWriter, PollResult, StatusSnapshot)
  ├── internal/adapter  (NewServerWithListener, BuildAuthorityRegistry)
  ├── internal/runtime  (RuntimeManager, RuntimeState, DeviceStatus, ListenerStatus, StatusBlockSnapshot)
  └── internal/core     (Store, MemoryID, Area constants — for ReadDeviceStatus / ReadViewerRegisters)

internal/config
  └── internal/core     (MemStore, Memory, MemoryID, AreaLayout, etc.)

internal/engine
  ├── internal/core     (Store, Memory, MemoryID, Area constants)
  ├── internal/config   (Config, UnitConfig, ParseListenPort — builder only)
  └── internal/engine/modbusclient  (Client impl)

internal/adapter
  └── internal/core     (Store, Memory, MemoryID, Area constants)

internal/runtime
  (no internal dependencies)

internal/core
  (no internal dependencies)
```

`internal/adapter` and `internal/engine` both import `internal/core` but **do not import each other**. The `core.Store` interface is the only coupling point between them.

---

## Boot Sequence Flow

```
main()
  │
  ├─1─ os.Args[1] (optional) — config file path; defaults to "config.yaml"
  │
  ├─2─ os.Stat(cfgPath)
  │      file not found → log "config.yaml not found, creating new configuration"
  │                        config.CreateMinimal(cfgPath) — writes minimal config.yaml to disk
  │                          (content: "replicator:\n  units: []\n" — empty units list)
  │                        rt.activeConfigYAML = MinimalConfigYAML
  │                        startWebUI = true  [→ skip to step 6]
  │      file found     → continue to step 3
  │
  ├─3─ config.Load(path)
  │      os.ReadFile → yaml.Unmarshal → *Config
  │      failure → rt.SetError(err); startWebUI = true  [→ skip to step 6]
  │
  ├─4─ config.Validate(cfg)
  │      structural and constraint checks
  │      failure → rt.SetError(err); startWebUI = true  [→ skip to step 6]
  │
  ├─5─ rt.Start(cfg, rawYAML)  [only reached when config is valid]
  │      config.BuildMemStore(cfg)
  │        for each (port, unit_id) derived from replicator reads:
  │          compute bounding range per FC → core.NewMemory(layouts)
  │          store.Add(MemoryID{port, unitID}, mem)
  │        for each (port, status_unit_id) with status_slot:
  │          allocate (max_slot+1)*30 holding registers
  │          store.Add(MemoryID{port, statusUnitID}, mem)
  │      engine.Build(cfg, store)
  │        for each replicator unit:
  │          builds Poller (lazy connect, no initial dial)
  │          builds WritePlan → StoreWriter
  │      for each unique target.Port across all replicator units:
  │        net.Listen("tcp", ":PORT")  ← pre-bind synchronously; error returned immediately
  │        adapter.NewServerWithListener(":PORT", ln, store, authority, debug)
  │        go srv.Serve()
  │      for each engine.Unit:
  │        out := make(chan PollResult, 8)
  │        go orchestrator(out)
  │        go poller.Run(ctx, out)
  │      startWebUI = cfg.WebUI.Enabled
  │
  ├─6─ WebUI startup  [conditional on startWebUI == true]
  │      webui.NewServer(webuiListen, rt, authCfg)
  │      go srv.ListenAndServe()
  │      log "webui adapter starting on <addr>"
  │
  └─7─ signal.Notify(quit, SIGINT, SIGTERM)
         <-quit → cancel() → all goroutines exit via ctx.Done()
```

Steps 3–4 run synchronously before any goroutine or network socket is created. However, failures at these steps are **non-fatal**: the process continues into WebUI-only mode. The runtime (steps 5+) is skipped whenever config loading or validation fails. The WebUI HTTP server (step 6) starts independently and is not gated on the runtime being healthy.

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

Config loading is a two-step synchronous process. It runs before the runtime is started but **after** the WebUI server is started (or scheduled to start). Missing or invalid config causes the process to enter WebUI-only mode rather than terminating.

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
| Config file not found | `os.Stat` returns `ErrNotExist` | **Recoverable** — WebUI starts on the configured listen address (default `:8080`); runtime disabled. |
| File not readable | `os.ReadFile` returns error | **Recoverable** — `rt.SetError(err)`; WebUI starts; runtime disabled. |
| YAML parse error | `yaml.Unmarshal` returns error | **Recoverable** — `rt.SetError(err)`; WebUI shows error; runtime disabled. |
| Validation failure | `config.Validate` returns error | **Recoverable** — `rt.SetError(err)`; WebUI shows error; runtime disabled. |
| Memory store build failure | `config.BuildMemStore` returns error | **Recoverable** — `rt.SetError(err)`; WebUI shows error; runtime disabled. |
| Engine build failure | `engine.Build` returns error | **Recoverable** — `rt.SetError(err)`; WebUI shows error; runtime disabled. |
| Adapter bind failure | `net.Listen` returns an error in `rebuild()` | **Recoverable** — `rt.SetError(err)`; the error is surfaced to the WebUI error bar; the WebUI remains accessible; no process exit. |

Failures at the config-loading and validation stages are **non-fatal**. The process continues in WebUI-only (degraded) mode, giving the operator the opportunity to correct the configuration through the browser interface without restarting the device. Only failures that prevent the runtime network adapters from binding their ports result in a fatal log and process exit.

Validation errors include the field path (e.g. `replicator.units[0] (plc1): target.port must be > 0`).

---

## External Dependencies

| Module | Version | Use |
|---|---|---|
| `github.com/tamzrod/modbus` | v0.1.1 | Modbus TCP client used by `internal/engine/modbusclient`. |
| `gopkg.in/yaml.v3` | v3.0.1 | YAML config file parsing in `internal/config/loader.go`. |
