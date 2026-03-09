# Aegis Modbus Behavior Crosscheck Audit

**Audit scope:** MMA2, Modbus Replicator, Aegis  
**Date:** 2026-03-09  
**Status:** Complete — corrections applied (D-1 through D-3, D-8)

---

## 1. Repository Architecture Summary

### MMA2

MMA2 is a **deterministic Modbus memory appliance**.  It owns a fixed register
space whose layout is declared at startup and never changes at runtime.  Its
four layers are:

| Layer | Responsibility |
|---|---|
| Configuration | Declares ports, unit IDs, memory sizes.  Immutable at runtime. |
| Core Memory (`internal/memorycore`) | Raw Modbus storage with per-instance `sync.RWMutex`. |
| Transport Adapters | Modbus TCP, REST, Raw Ingest — translate network requests into memory ops. |
| Runtime | OS integration, lifecycle, listener management. |

MMA2 has **no polling logic**.  It is passive: adapters write into it; clients
read from it.

### Modbus Replicator

The Replicator is a **deterministic read → fan-out → write engine**.  It reads
Modbus field devices and writes the results into MMA2 via the Raw Ingest
protocol.

| Component | File |
|---|---|
| `Poller` | `internal/poller/poller.go` — reads one device via FC 1–4; single global interval |
| `Runner` | `internal/poller/runner.go` — ticker-based poll loop |
| `Writer` | `internal/writer/writer.go` — delivers poll results to MMA2 endpoint |
| `StatusWriter` | `internal/writer/status_writer.go` — differential status-block writes |
| Modbus client | `internal/poller/modbus/client.go` — TCP client, FC 1–4, TID validation |

The Replicator uses a **single global interval per polling unit**: every tick
executes every configured read block.  There is no per-block scheduling.

### Aegis

Aegis is a **combined in-process appliance**: it includes a polling engine, an
in-process memory store (equivalent to MMA2's core), and a Modbus TCP server
that serves data directly from that store.  No network hop exists between the
engine and the store.

| Component | File |
|---|---|
| `Poller` | `internal/engine/poller.go` — per-block independent intervals |
| `Runner` | `internal/engine/runner.go` — minInterval ticker |
| `StoreWriter` | `internal/engine/writer.go` — in-process writes to `core.Memory` |
| `Orchestrator` | `cmd/aegis/orchestrator.go` — consumes poll results, drives status |
| `BlockHealthStore` | `internal/engine/block_health.go` — per-block health tracking |
| Modbus client | `internal/engine/modbusclient/client.go` — identical protocol to Replicator |
| Core memory | `internal/core/memory.go` — same model as MMA2 `memorycore` |

---

## 2. Memory Model Comparison

| Property | MMA2 | Replicator | Aegis |
|---|---|---|---|
| Storage structure | `[]uint16` for registers, `[]byte` (packed LSB-first) for bits | N/A (writes via Raw Ingest) | Identical to MMA2 |
| Area separation | Coils / Discrete Inputs / Holding Regs / Input Regs | N/A | Identical to MMA2 |
| Address model | Zero-based logical offsets within a declared layout | N/A | Identical to MMA2 |
| Bounds protection | `layout.Contains(address, count)` — hard error on violation | N/A | Identical to MMA2 |
| Concurrency | Per-instance `sync.RWMutex` (RLock reads, Lock writes) | N/A | Identical to MMA2 |
| Store registry | `MemStore` map keyed by `(Port, UnitID)`, protected by RWMutex | N/A | Identical to MMA2 |

**Verdict:** Aegis's core memory model is a faithful port of MMA2 `memorycore`.
No differences.

---

## 3. Polling Engine Comparison

| Property | Replicator | Aegis |
|---|---|---|
| Interval model | Single global interval per unit — all reads fire on every tick | Per-block independent intervals; each block has its own `Interval` and `nextExec` |
| Ticker resolution | `cfg.Interval` | `minInterval()` — smallest interval across all read blocks |
| Due-block filter | None — all reads always execute | `nextExec[i]` checked against current time; only due blocks execute |
| Empty tick | Impossible — every tick executes all reads | Possible when all blocks have `nextExec > now` |
| Abort-on-first-failure | Yes — first failing block aborts the cycle | Yes — same behavior |
| nextExec advancement | N/A | Advanced for all due blocks **before** any I/O, regardless of success/failure |
| Goroutine structure | One goroutine per unit; ticker drives `PollOnce()` | Same — one goroutine per unit; ticker drives `pollAt(t)` |
| Poll-overlap protection | Blocking channel send (`out <- res`) ensures orchestrator must drain before next tick | Identical |

### Key architectural difference

The Replicator's model is simpler: one interval, all reads always fire.
Aegis's per-block interval model is a deliberate extension for deployments
where different registers have different freshness requirements.

The extension introduces a new concept — the **empty tick** — that does not
exist in the Replicator.  See Section 6 (Discrepancy List) for the bugs this
introduced.

---

## 4. Modbus Request Logic Comparison

| Property | Replicator (`internal/poller/modbus/client.go`) | Aegis (`internal/engine/modbusclient/client.go`) |
|---|---|---|
| Function codes | FC 1, 2, 3, 4 | FC 1, 2, 3, 4 |
| Request construction | MBAP header + PDU, randomised starting TID | Identical |
| TID validation | Yes — mismatch → error | Identical |
| Protocol ID check | Yes — non-zero → error | Identical |
| Unit ID check | Yes — mismatch → error | Identical |
| Exception handling | Returns `ModbusException{Function, Exception}` implementing `Code() uint16` | Identical |
| Bit response | `unpackBits()` → `[]bool` (LSB-first, same semantics as MMA2) | Identical |
| Register response | `unpackRegisters()` → `[]uint16` | Identical |
| Max quantity check | Relies on config validation (not enforced in client) | Identical |
| Connection reuse | Client held in `Poller.client`; nil on dead-connection errors | Identical |
| Lazy connect | Yes — factory called only when a poll is due | Identical |
| Dead-connection detection | `isDeadConnErr()` — EOF, broken pipe, reset, aborted, closed | Identical |
| Retry logic | None — one attempt per tick; next tick may reconnect | Identical |
| Timeout handling | `net.Error.Timeout()` → `TimeoutsTotal++` (separate from transport errors) | Identical |

**Verdict:** The Modbus client logic in Aegis is functionally identical to the
Replicator.  No differences in the request/response path.

---

## 5. Error Handling Comparison

| Property | Replicator | Aegis |
|---|---|---|
| Connection management | Client held in `Poller.client`; invalidated on dead-connection errors | Identical |
| Dead-connection detection | `isDeadConnErr()` inspects error string | Identical |
| Timeout classification | `net.Error.Timeout()` → `TimeoutsTotal++` | Identical |
| Transport error classification | All non-timeout errors → `TransportErrorsTotal++` | Identical |
| Retry | None in a single tick; next tick may create a new client | Identical |
| Consecutive failure tracking | `ConsecutiveFailCurr` incremented per failed tick; reset on success | Identical intent; see discrepancy D-1 |
| All-or-nothing data write | `writer.Write(res)` skips data writes if `res.Err != nil` | Identical |
| Stale data protection | Failed poll → data registers not updated | Identical |

---

## 6. Discrepancy List

### D-1 — CRITICAL: Counter inflation and ConsecutiveFailCurr reset on empty ticks

**Location:** `internal/engine/poller.go` — `pollAt()` function  
**Classification:** CRITICAL

**Description:**  
Before this audit, `pollAt()` incremented `RequestsTotal` and called
`recordSuccess()` (which increments `ResponsesValidTotal` and resets
`ConsecutiveFailCurr` to zero) even when no read blocks were due.

```
// BEFORE (incorrect):
func (p *Poller) pollAt(now time.Time) PollResult {
    p.counters.RequestsTotal++     // incremented before due-check
    ...
    if len(due) == 0 {
        p.recordSuccess()           // ResponsesValidTotal++, ConsecutiveFailCurr=0
        return res
    }
    ...
}
```

The Replicator has no concept of an empty tick: every `PollOnce()` call
executes all reads, so every `RequestsTotal++` corresponds to a real Modbus
exchange.  In Aegis, the per-block interval model can produce ticks where no
block is due.  On such ticks:

- `RequestsTotal` was incremented despite no Modbus request being issued.
- `ResponsesValidTotal` was incremented despite no valid response being
  received.
- `ConsecutiveFailCurr` was reset to zero, potentially masking an active
  failure streak if a timer-granularity edge caused a spurious empty tick
  while the device was still in error.

**Fix applied:**

```go
// AFTER (correct):
func (p *Poller) pollAt(now time.Time) PollResult {
    res := PollResult{UnitID: p.cfg.UnitID, At: now}
    due := ...
    if len(due) == 0 {
        // No Modbus exchange occurred — do NOT update any counters.
        return res
    }
    // Count the poll attempt only when actual Modbus reads will be performed.
    p.counters.RequestsTotal++
    ...
}
```

**Files changed:** `internal/engine/poller.go`  
**Tests added:** `internal/engine/poller_test.go`

---

### D-2 — CRITICAL: Spurious health-state reset on empty ticks

**Location:** `cmd/aegis/snapshot.go` — `applyPollResult()` function  
**Classification:** CRITICAL

**Description:**  
`applyPollResult()` treated any `PollResult` with `Err == nil` as a successful
poll, unconditionally transitioning health to `OK` and clearing
`SecondsInError` and `LastErrorCode`:

```
// BEFORE (incorrect):
if res.Err == nil {
    snap.Health = HealthOK
    snap.LastErrorCode = 0
    snap.SecondsInError = 0
}
```

In the Replicator, `res.Err == nil` reliably means every configured read block
completed successfully.  In Aegis, `res.Err == nil` can also mean no blocks
were due (empty tick) — no reads were attempted and no device communication
occurred.

If a timer-granularity edge caused an empty tick to be delivered while the
device was still in an error state, the health state would silently flip to
`OK` for one cycle before the next real poll corrected it.  This creates a
brief, spurious `OK` signal visible to any downstream Modbus client reading
the status block.

The Replicator never encounters this scenario because it has no empty ticks.

**Fix applied:**

```go
// AFTER (correct):
func applyPollResult(snap engine.StatusSnapshot, res engine.PollResult) (engine.StatusSnapshot, bool) {
    // Empty tick: no blocks were executed — do not alter health state.
    if res.Err == nil && len(res.BlockUpdates) == 0 {
        return snap, false
    }
    ...
}
```

The guard uses `len(res.BlockUpdates) == 0 && res.Err == nil` to detect an
empty tick precisely:
- Empty tick: `res.Err == nil`, `res.BlockUpdates` nil (no blocks executed).
- Real success: `res.Err == nil`, `len(res.BlockUpdates) >= 1`.
- Real failure: `res.Err != nil` (BlockUpdates may or may not be populated).
- Factory/connect failure: `res.Err != nil`, `res.BlockUpdates` nil — correctly
  triggers the error path, not the empty-tick guard.

**Files changed:** `cmd/aegis/snapshot.go`  
**Tests added:** `cmd/aegis/snapshot_test.go`

---

### D-3 — RISKY: Latency tracker skew on empty ticks

**Location:** `cmd/aegis/orchestrator.go` — `runOrchestrator()` function  
**Classification:** RISKY

**Description:**  
The latency tracker recorded `time.Since(res.At)` for every result received on
the poll channel, including empty ticks.  An empty tick has near-zero latency
(no I/O performed), which skews the running average latency downward,
misrepresenting the true device response time.

The Replicator has no empty ticks; its latency figures always reflect real
device I/O.

**Fix applied:**

```go
// Record latency only for ticks where actual Modbus reads were attempted.
if tracker != nil && !res.At.IsZero() && (res.Err != nil || len(res.BlockUpdates) > 0) {
    ms := uint32(time.Since(res.At).Milliseconds())
    tracker.Record(unitID, ms)
}
```

**Files changed:** `cmd/aegis/orchestrator.go`

---

### D-4 — SAFE DIFFERENCE: Status block format (Aegis v1 protocol)

**Location:** `internal/engine/status.go`, `docs/STATUS_PLANE.md`  
**Classification:** SAFE DIFFERENCE (intentional, locked v1 contract)

**Description:**  
The Aegis status block includes a 2-register magic header (`0x4147` / `0x53XX`)
in slots 0–1 before the operational data.  The Replicator's status block has no
header; `HealthCode` occupies slot 0.

| Slot | Replicator | Aegis |
|---|---|---|
| 0 | HealthCode | Header word 0 (`0x4147` = "AG") |
| 1 | LastErrorCode | Header word 1 (`0x53XX` = "S" + block index) |
| 2 | SecondsInError | HealthCode |
| 3 | DeviceName[0] | LastErrorCode |
| 4 | DeviceName[1] | SecondsInError |
| 20–29 | Transport counters | Transport counters (same positions) |

This is an **intentional Aegis design choice**, documented as the locked v1
Status Plane contract (`docs/STATUS_PLANE.md`).  The header enables:

- Sanity-checking by diagnostic tools (validate magic before parsing).
- Block identification by index without relying solely on address arithmetic.

Any downstream client reading Aegis status registers must use the Aegis v1
offsets.  Clients expecting the Replicator's slot-0 HealthCode will instead
read `0x4147`.  This is by design and requires client-side adaptation, not a
code fix.

**No code change applied.**

---

### D-5 — SAFE DIFFERENCE: Per-block interval scheduling

**Location:** `internal/engine/poller.go`, `internal/engine/types.go`  
**Classification:** SAFE DIFFERENCE (intentional architecture extension)

**Description:**  
The Replicator assigns a single global interval to each polling unit.  All
read blocks execute on every tick.  Aegis assigns an independent `Interval`
to each `ReadBlock`.  The ticker fires at `minInterval()` (the shortest across
all blocks in the unit); each block is only executed when its `nextExec` time
has been reached.

This allows a single Aegis unit to read high-frequency registers (e.g., 500 ms)
and low-frequency registers (e.g., 60 s) from the same device without spawning
separate connections.

The Replicator achieves the same result by configuring separate units per
cadence.

**No code change applied.**

---

### D-6 — SAFE DIFFERENCE: In-process write path (no Raw Ingest)

**Location:** `internal/engine/writer.go`  
**Classification:** SAFE DIFFERENCE (architectural consolidation)

**Description:**  
In the MMA2+Replicator system, the Replicator writes data to MMA2 via the Raw
Ingest TCP protocol — a network hop.  Aegis eliminates this hop: the engine
writes directly into the in-process `core.Memory` store via in-process function
calls.

This is a deliberate architectural choice that improves write latency and
removes an entire class of network-level failure modes.  The correctness of
the write operations (bounds checking, mutex discipline, area routing) is
identical to MMA2.

**No code change applied.**

---

### D-7 — SAFE DIFFERENCE: Status write strategy

**Location:** `internal/engine/writer.go`, `internal/writer/status_writer.go` (Replicator)  
**Classification:** SAFE DIFFERENCE

**Description:**  
The Replicator's `deviceStatusWriter` uses a **differential write** strategy:
it tracks the last-written snapshot and only writes registers that changed.
On startup or after a write failure, it performs a full-block re-assert.  This
minimises Raw Ingest network traffic.

Aegis's `StoreWriter.WriteStatus` always re-encodes and writes the full 30-
register status block.  Because writes are in-process function calls (no
network), the traffic-minimisation motivation does not apply.  Full-block
writes are simpler and carry no correctness risk.

**No code change applied.**

---

### D-8 — CRITICAL: nextExec not advanced on failure — tight retry loop in multi-interval configs

**Location:** `internal/engine/poller.go` — `pollAt()` function  
**Classification:** CRITICAL

**Description:**  
Before this fix, `pollAt()` only advanced `nextExec[i]` when **all** due blocks
succeeded.  On any failure (read error, factory error, or unsupported FC), all
due blocks' `nextExec` values were left unchanged — meaning every due block
remained immediately due again on the very next ticker tick.

Because the ticker fires at `minInterval()` (the smallest interval across all
configured read blocks), a block whose own interval is much larger than
`minInterval` would be retried at `minInterval` rate rather than at its
configured rate:

```
Example:
  Block A — FC 3, interval 100 ms  (fast sensor data)
  Block B — FC 3, interval 60 s    (slow status register)

  minInterval = 100 ms

  Scenario: Block B (60 s) fails.

  BEFORE fix:
    nextExec[B] is NOT advanced.
    On every subsequent 100 ms tick, Block B is still due.
    Block B is retried 10 times/second instead of once/minute.
    → 600× more aggressive than configured.
    → Weak device is hammered continuously during an error condition.

  AFTER fix:
    nextExec[B] is advanced to now + 60 s before any I/O.
    Block B is retried only after its full configured interval.
    → Device load unchanged from the pre-failure steady state.
```

The same pattern applied to factory failures: if the device is unreachable and
a new TCP connection cannot be established, Aegis would attempt to reconnect on
every `minInterval` tick rather than waiting for the block's own cadence.

The Replicator is immune to this class of bug because it has a single global
interval per unit — every block has the same cadence, so `minInterval` equals
the (only) block interval.  There is no faster ticker that can trigger early
retries.

**Fix applied:**

```go
// BEFORE (incorrect — nextExec advanced only on full success):
for _, idx := range due {
    // ... execute block
    if err != nil {
        return res  // nextExec unchanged → block immediately due on next tick
    }
}
// Only reached if all blocks succeeded:
for _, idx := range due {
    p.nextExec[idx] = now.Add(p.cfg.Reads[idx].Interval)
}

// AFTER (correct — nextExec advanced before any I/O):
// Advance next-execution times for all due blocks before attempting any I/O.
for _, idx := range due {
    p.nextExec[idx] = now.Add(p.cfg.Reads[idx].Interval)
}
// ... execute blocks; failure paths now return with nextExec already advanced
```

**Files changed:** `internal/engine/poller.go`  
**Tests added:** `internal/engine/poller_test.go`
- `TestPollAtFailureAdvancesNextExec` — verifies that on read failure, all due blocks' `nextExec` is advanced to `now + interval`
- `TestPollAtFactoryFailureAdvancesNextExec` — verifies that on factory/connect failure, due blocks' `nextExec` is advanced

---

## 7. Risk Assessment

| ID | Classification | Impact |
|---|---|---|
| D-1 | **CRITICAL** | Counters (`RequestsTotal`, `ResponsesValidTotal`) inflated by empty ticks; `ConsecutiveFailCurr` could be reset to 0 while device is in error state. **Fixed.** |
| D-2 | **CRITICAL** | Health state can briefly flip to `OK` during timer-granularity empty ticks while device is still in error. **Fixed.** |
| D-3 | **RISKY** | Latency average skewed downward by near-zero empty-tick measurements. **Fixed.** |
| D-4 | SAFE | Aegis status block slots offset by 2 vs. Replicator. Intentional; locked v1 contract. Clients must use Aegis offsets. |
| D-5 | SAFE | Per-block interval scheduling — Aegis architectural extension. |
| D-6 | SAFE | In-process write path — eliminates Raw Ingest network hop. |
| D-7 | SAFE | Full-block status writes vs. Replicator differential writes. |
| D-8 | **CRITICAL** | Failing blocks not advancing `nextExec` — retried at `minInterval` rate in multi-interval configs, potentially hundreds of times faster than configured. Causes continuous device hammering during error conditions. **Fixed.** |

---

## 8. Recommended Corrections

### Applied in this audit

| Correction | File | Description |
|---|---|---|
| Fix D-1 | `internal/engine/poller.go` | Move `RequestsTotal++` after the `due` check; remove `recordSuccess()` on empty ticks |
| Fix D-2 | `cmd/aegis/snapshot.go` | Guard `applyPollResult` against empty-tick results; use `len(res.BlockUpdates) == 0 && res.Err == nil` to detect empty ticks |
| Fix D-3 | `cmd/aegis/orchestrator.go` | Skip latency recording for empty-tick results |
| Fix D-8 | `internal/engine/poller.go` | Advance `nextExec` for all due blocks before I/O so failed blocks wait their configured interval before retry |
| Tests | `internal/engine/poller_test.go` | Validate counter invariants for empty, success, and failure ticks; validate `nextExec` advancement on failure and factory failure |
| Tests | `cmd/aegis/snapshot_test.go` | Validate health-state transitions for all tick types |

### Client guidance (not a code change)

Any Modbus client or dashboard that was designed against the Replicator status
block layout (HealthCode at register 0) must be updated to use the Aegis v1
offsets (HealthCode at register 2) when reading from an Aegis instance.  The
two layouts are not interchangeable.

---

## 9. Reliability Patterns: MMA2 + Replicator vs. Aegis

| Pattern | MMA2 + Replicator | Aegis |
|---|---|---|
| Strict memory locking | `sync.RWMutex` per Memory instance | Identical |
| Deterministic poll loops | Single global interval; all reads always fire | Per-block intervals with `minInterval` ticker; same determinism within a block's cadence |
| Request serialisation | Sequential reads within a `PollOnce()` call | Identical |
| All-or-nothing data write | `res.Err != nil` → no data writes | Identical |
| Stale data preservation | Failed poll → data registers unchanged | Identical |
| No hidden retries | One attempt per tick; next tick may reconnect | Identical |
| Dead-connection detection | `isDeadConnErr()` classifier | Identical |
| Counter-as-data | Transport counters written to status block | Identical |
| Status block determinism | Fixed 30-register layout, always written | Identical (different slot offsets by design) |
| Poll-overlap prevention | Blocking channel send | Identical |

The **patterns violated by Aegis** (before the fixes in this audit) were:

1. **Counter-as-data determinism** (D-1, D-2, D-3): counters were incremented
   and health state updated on empty ticks that carried no real Modbus exchange,
   producing values inconsistent with the Replicator's definition of "one
   increment = one device interaction."

2. **Failure pacing** (D-8): failing blocks in multi-interval configurations were
   not advancing `nextExec`, causing them to be retried at `minInterval` rate
   instead of their own configured cadence.  The Replicator is immune to this
   because it uses a single global interval per unit.

---

## 10. Root Cause Hypothesis

> "MMA2 + Replicator is reliable but Aegis shows discrepancy."

The most likely causes, in order of probability:

1. **Status block register offset** (D-4): Any client reading HealthCode at
   register 0 (Replicator convention) sees `0x4147` from Aegis instead.
   This is not a bug in Aegis but a breaking change relative to the Replicator
   format.  It is the most immediately visible difference to any tool that
   reads device health.

2. **Counter inflation on empty ticks** (D-1, now fixed): `RequestsTotal` and
   `ResponsesValidTotal` grew faster than expected in multi-block units,
   causing counter-based health dashboards to report misleading rates.

3. **Spurious health `OK` on empty ticks** (D-2, now fixed): In rare
   timer-granularity races, health could briefly reset to `OK` between a real
   poll failure and the next poll attempt.  Any monitoring system sampling
   health at that moment would see a false recovery.
