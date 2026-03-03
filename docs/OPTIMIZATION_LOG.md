# Optimization Log — Intent Isolation Refactor

---

## 1. Overview

This was a structural refactor only.

- No runtime behavior change.
- No protocol change.
- No configuration contract change.

The objective was to reduce intent density per file by extracting each distinct responsibility into its own file, and to eliminate concrete-type coupling between original files and the files extracted from them.

---

## 2. Refactor Scope

### Files Modified

| File | Change |
|---|---|
| `internal/engine/block_health.go` | Added `SetBlockHealth` and `GetBlockHealth` primitive-arg methods to prevent internal key-layout leakage into callers. |
| `internal/adapter/handler.go` | Introduced `Enforcer` interface; changed `HandleConn` parameter from `*AuthorityRegistry` to `Enforcer`. |
| `internal/adapter/server.go` | Changed `Server.authority` field and `NewServer` parameter from `*AuthorityRegistry` to `Enforcer`. |

### New Files Created

| File | Intent Domain |
|---|---|
| `cmd/aegis/orchestrator.go` | Scheduling + policy enforcement |
| `cmd/aegis/health.go` | Per-block health state mutation |
| `cmd/aegis/snapshot.go` | Status snapshot data transformation |
| `cmd/aegis/doc.go` | Package-level dependency documentation |
| `internal/adapter/authority.go` | Per-target authority enforcement |

### Files Removed

None.

---

## 3. Intent Domains Extracted

| Original File | Extracted File | Intent Domain | Behavior Change |
|---|---|---|---|
| `cmd/aegis/main.go` | `cmd/aegis/orchestrator.go` | Scheduling + policy enforcement (per-unit poll-result consumption loop, write-change policy, secTicker) | None (refactor only) |
| `cmd/aegis/main.go` | `cmd/aegis/health.go` | Per-read-block health state mutation (`updateBlockHealth`) | None (refactor only) |
| `cmd/aegis/main.go` | `cmd/aegis/snapshot.go` | Status snapshot data transformation (`applyPollResult`, `applyCounters`) | None (refactor only) |
| `internal/adapter/handler.go` | `internal/adapter/authority.go` | Per-target authority enforcement (`AuthorityRegistry`, `BlockHealthReader`, `Enforce`) | None (refactor only) |

---

## 4. Architectural Goal

**Reduce intent density per file.**
Each file now owns exactly one responsibility. `main.go` owns boot sequence and IO only. `orchestrator.go` owns scheduling and policy only. `handler.go` owns TCP connection loop only. `authority.go` owns authority logic only.

**Isolate authority enforcement.**
Authority enforcement (mode-based write rejection, block-health-gated reads, coverage checking) is now contained entirely within `internal/adapter/authority.go`. It is not distributed across the connection handler or the server struct.

**Isolate scheduling logic.**
The per-unit `select` loop, `secTicker`, and write-change policy are contained entirely within `cmd/aegis/orchestrator.go`. They are no longer inlined inside `main()`.

**Isolate state tracking.**
Per-block health state mutation (`updateBlockHealth`) is contained in `cmd/aegis/health.go`. Status snapshot derivation (`applyPollResult`, `applyCounters`) is contained in `cmd/aegis/snapshot.go`. Neither file has side effects; both are pure function files.

**Preserve separation of concerns.**
`internal/adapter` does not import `internal/engine`. `internal/engine` does not import `internal/adapter`. The seam between them is `core.Store` (for data) and the `BlockHealthReader` interface in the adapter (for health state). The engine satisfies `BlockHealthReader` via `GetBlockHealth` using only primitive types — no engine-internal types cross the package boundary.

**Improve debuggability.**
A failure in authority enforcement is isolated to `authority.go`. A failure in scheduling is isolated to `orchestrator.go`. Stack traces identify the responsible file without needing to trace through a monolithic handler or main function.

**Improve static analysis clarity.**
Each file has a narrow, declared dependency surface. Interfaces (`Enforcer`, `BlockHealthReader`, `counterSource`, `pollWriter`, `blockHealthWriter`) make the coupling explicit and statically verifiable. Unused or excessive imports are eliminated.

---

## 5. Dependency Direction (Before vs After)

### `cmd/aegis` — Before

The orchestration loop (scheduling, health mutation, snapshot derivation) was inlined in `main.go`. The orchestrator referenced concrete engine types directly:

```
cmd/aegis/main.go
  ├── *engine.Poller           (concrete — called p.Counters())
  ├── *engine.StoreWriter      (concrete — called w.Write(), w.WriteStatus())
  └── *engine.BlockHealthStore (concrete — called s.Set(BlockHealthKey{UnitID, BlockIdx}, h))
```

`BlockHealthKey` (an engine-internal struct) was constructed by name in the orchestration code, leaking the internal key layout across the package boundary.

### `cmd/aegis` — After

The orchestration loop is in `orchestrator.go`. It depends only on narrow local interfaces:

```
cmd/aegis/main.go
  └── cmd/aegis/orchestrator.go
        ├── counterSource     (interface) ← satisfied by *engine.Poller
        ├── pollWriter        (interface) ← satisfied by *engine.StoreWriter
        ├── blockHealthWriter (interface) ← satisfied by *engine.BlockHealthStore
        ├── cmd/aegis/health.go    (state mutation — no external deps beyond engine types)
        └── cmd/aegis/snapshot.go  (data transformation — no external deps beyond engine types)
```

`main.go` passes concrete engine types; `orchestrator.go` binds them via structural typing. `BlockHealthKey` is never constructed outside `internal/engine`.

---

### `internal/adapter` — Before

`handler.go` and `server.go` referenced `*AuthorityRegistry` directly. A change to the concrete registry type required modifications in both the connection handler and the server struct:

```
internal/adapter/handler.go
  └── *AuthorityRegistry   (concrete — HandleConn parameter)

internal/adapter/server.go
  └── *AuthorityRegistry   (concrete — Server.authority field, NewServer parameter)
```

Authority enforcement logic was not separated from connection-handling logic. The authority registry was a peer of the handler rather than an abstracted dependency.

### `internal/adapter` — After

Authority enforcement is extracted to `authority.go`. `handler.go` and `server.go` depend on the `Enforcer` interface only:

```
internal/adapter/handler.go
  └── Enforcer   (interface) ← satisfied by *AuthorityRegistry

internal/adapter/server.go
  └── Enforcer   (interface) ← satisfied by *AuthorityRegistry

internal/adapter/authority.go
  ├── BlockHealthReader (interface) ← satisfied by *engine.BlockHealthStore
  └── internal/config   (read-only config access during registry construction)
```

`*AuthorityRegistry` satisfies `Enforcer` via Go structural typing. No changes were required to `authority.go` or `main.go`.

---

### Full Package Dependency Graph — After

```
cmd/aegis
  ├── internal/config     (Load, Validate, BuildMemStore)
  ├── internal/engine     (Build, NewBlockHealthStore, PollResult, StatusSnapshot,
  │                        TransportCounters, ReadBlockHealth, BlockUpdate,
  │                        HealthOK, HealthError, HealthUnknown, ErrorCode)
  └── internal/adapter    (BuildAuthorityRegistry, NewServer)

internal/config
  └── internal/core       (MemStore, Memory, MemoryID, AreaLayout, StateSealingDef)

internal/engine
  ├── internal/core       (Store, Memory, MemoryID, Area constants)
  └── internal/config     (Config, UnitConfig, ParseListenPort — builder only)

internal/adapter
  ├── internal/core       (Store, Memory, MemoryID, Area constants)
  └── internal/config     (Config, TargetModeA/B/C — authority.go only)

internal/core
  (no internal dependencies)
```

`internal/adapter` and `internal/engine` do not import each other.
The only runtime coupling between them is through `core.Store` (data plane)
and the `BlockHealthReader` interface (health plane), both satisfied via primitive
types or standard interfaces with no cross-package struct construction.
