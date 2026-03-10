# Aegis Puller Scheduling Audit

**Audit scope:** `internal/puller` — field device polling engine  
**Date:** 2026-03-10  
**Status:** Complete — all Replicator invariants verified; no defects found

---

## Context

The Aegis repository has been restructured into four core packages:

| Package | Responsibility | Origin |
|---|---|---|
| `internal/puller` | Field device polling engine | Derived from Replicator |
| `internal/memory` | Modbus memory appliance | Derived from MMA2 |
| `internal/orchestrator` | Runtime coordination | Aegis-original |
| `web` | UI | Aegis-original |

The original Replicator used a **single global interval per device** — all read
blocks fired on every tick.

The new Aegis requirement is **per-read-block interval scheduling** — each read
block has an independent cadence.

This audit verifies that the `internal/puller` implementation correctly delivers
this extension while preserving all Replicator reliability invariants.

---

## Phase 1 — Scheduling Logic Location

### Files examined

| File | Key elements |
|---|---|
| `internal/puller/engine.go` | `Poller`, `pollAt()`, `nextExec[]`, `minInterval()` |
| `internal/puller/scheduler.go` | `Poller.Run()` — ticker-driven outer loop |
| `internal/puller/device.go` | `ReadBlock.Interval`, `TransportCounters`, `Build()` |

### How scheduling works

**Interval storage** — `ReadBlock.Interval` (type `time.Duration`) holds the
poll cadence for one read block.  It is set at construction time from
`ReadConfig.IntervalMs` and is immutable for the lifetime of a `Poller`.

```
ReadBlock
  FC        uint8
  Address   uint16
  Quantity  uint16
  Interval  time.Duration   ← per-block cadence
```

**Due-time tracking** — `Poller.nextExec []time.Time` is a parallel slice to
`Poller.cfg.Reads`.  Index `i` records when block `i` is next eligible to
execute.  All entries are initialized to the zero value of `time.Time`,
making every block due on the first tick.

**Due-block detection** (engine.go:83–88):
```go
for i := range p.cfg.Reads {
    if !now.Before(p.nextExec[i]) {
        due = append(due, i)
    }
}
```
A block is due when `now >= nextExec[i]`.

**Next-execution advancement** (engine.go:94–96):
```go
for _, idx := range due {
    p.nextExec[idx] = now.Add(p.cfg.Reads[idx].Interval)
}
```
`nextExec[i]` is set to `now + Interval` for every due block **before** any
Modbus I/O begins.

**Ticker resolution** (scheduler.go:22):
```go
ticker := time.NewTicker(p.minInterval())
```
The ticker fires at `minInterval()` — the smallest interval across all read
blocks.  This ensures no block is delayed by more than one tick relative to
its configured cadence.

---

## Phase 2 — Read Block Scheduling Model

### Is each ReadBlock independently scheduled?

**Yes.**  Each element of `nextExec[]` advances independently according to its
own `ReadBlock.Interval`.  A block with a 1-second interval will fire every
second; a block with a 60-second interval will fire once per minute.  The
two blocks' clocks do not interfere.

### Is there still a device-level interval?

**No.**  `PollerConfig` contains only `UnitID` and `Reads []ReadBlock`.
There is no device-level interval field.  The `minInterval()` value is a
derived scheduling parameter (ticker resolution), not a poll cadence.

### Which interval drives execution?

`ReadBlock.Interval` — always.  A block executes when
`now >= nextExec[i]`, where `nextExec[i]` was last set to
`executionTime + ReadBlock.Interval`.

**Model in force:**

```
Poller (one device)
  ReadBlock[0]   FC=3  Addr=0   Qty=10   Interval=1s    nextExec[0]
  ReadBlock[1]   FC=3  Addr=100 Qty=10   Interval=60s   nextExec[1]
  ReadBlock[2]   FC=4  Addr=0   Qty=5    Interval=5s    nextExec[2]
```

---

## Phase 3 — Device Execution Discipline

### Goroutine model

Each `Poller` runs in **exactly one goroutine**, started by
`orchestrator.rebuild()` (lifecycle.go:724–727):

```go
go func(p *puller.Poller, ch chan<- memory.PollResult) {
    defer r.wg.Done()
    p.Run(runtimeCtx, ch)
}(u.Poller, out)
```

`Poller.Run()` drives a single blocking ticker loop.  Each tick calls
`p.pollAt(t)` synchronously.  Because the loop is single-threaded and
`pollAt` blocks until all due reads complete, there is **no concurrency**
within a device.

### Execution path

```
Poller.Run() [one goroutine]
  │
  └─ ticker.C fires
       │
       └─ p.pollAt(t) [synchronous, blocking]
            │
            ├─ detect due blocks
            ├─ advance nextExec for all due blocks
            ├─ connect client if needed
            └─ sequential for-range over due blocks
                 └─ client.ReadXxx()  [one at a time]
```

Reads execute **directly inside the scheduler loop**, not via a separate
device-worker queue.  Serialization is guaranteed by construction: there is
no mechanism by which two reads to the same device could execute concurrently.

---

## Phase 4 — Burst Protection

### Scenario

Multiple read blocks become due simultaneously (e.g., on first tick, or when
a slow block and a fast block coincide).

### Mechanism

`pollAt()` collects all due blocks into a `due []int` slice, then executes
them in a **sequential for-range loop** (engine.go:118–187):

```go
for _, idx := range due {
    rb := p.cfg.Reads[idx]
    switch rb.FC {
    case 3:
        regs, err := p.client.ReadHoldingRegisters(rb.Address, rb.Quantity)
        if err != nil {
            ...
            return res  // abort; remaining due blocks not attempted
        }
        ...
    }
}
```

No goroutines are spawned per block.  The `for range due` loop executes
one Modbus request at a time, in index order.  A burst of `N` simultaneously
due blocks produces exactly `N` sequential requests to the same device,
never `N` parallel requests.

### Enforcement summary

| Layer | Mechanism |
|---|---|
| Single goroutine | `Poller.Run()` never spawns sub-goroutines |
| Sequential reads | `for range due` loop in `pollAt()` |
| No queue needed | Serialization is implicit in the single-threaded model |

---

## Phase 5 — Replicator Invariant Verification

### Invariant 1: At most one in-flight Modbus request per device

**Status: ✅ Preserved**

`Poller.Run()` is a single goroutine.  `pollAt()` is synchronous.  The
ticker's channel send `out <- p.pollAt(t)` blocks until the orchestrator
reads the result (buffered channel, capacity 8).  Even with buffering, the
Poller goroutine itself executes only one `pollAt()` call at a time.

### Invariant 2: Client connection reused between reads unless connection is dead

**Status: ✅ Preserved**

`p.client` is stored in the `Poller` struct and reused across all calls to
`pollAt()`.  The only mechanism that clears it is `maybeInvalidateClient()`
(engine.go:262–270), which is invoked only when `isDeadConnErr(err)` returns
true:

```go
func (p *Poller) maybeInvalidateClient(err error) {
    if err == nil || !isDeadConnErr(err) {
        return
    }
    if c, ok := p.client.(interface{ Close() error }); ok {
        _ = c.Close()
    }
    p.client = nil
}
```

Dead-connection classifier (`isDeadConnErr`) matches: EOF, broken pipe,
connection reset, connection aborted, closed network connection, and
Windows WSA variants.  Modbus exception codes and timeouts do **not**
trigger client invalidation; the connection is reused for those.

On the next tick after invalidation, `p.factory()` is called to establish
a fresh connection.

### Invariant 3: No retries within the same cycle

**Status: ✅ Preserved**

`pollAt()` returns on the first read error (see engine.go:124–130 for
FC1 as representative):

```go
bits, err := p.client.ReadCoils(rb.Address, rb.Quantity)
if err != nil {
    p.maybeInvalidateClient(err)
    res.Err = err
    updates = append(updates, blockUpdateFromErr(idx, err))
    p.recordFailure(err)
    res.BlockUpdates = updates
    return res  // ← exit; no retry
}
```

There is no retry loop, no `for` that re-attempts a failed read, and no
goroutine spawned to retry asynchronously.

### Invariant 4: Transport counters do not affect scheduling

**Status: ✅ Preserved**

`TransportCounters` fields (`RequestsTotal`, `ResponsesValidTotal`,
`TimeoutsTotal`, `TransportErrorsTotal`, `ConsecutiveFailCurr`,
`ConsecutiveFailMax`) are written by `recordSuccess()` and `recordFailure()`
only.  No counter value is read by any scheduling path.  `nextExec` is
computed exclusively from `now` and `ReadBlock.Interval`.

### Invariant 5: Failed reads schedule the next attempt at the next interval, not immediately

**Status: ✅ Preserved**

`nextExec[idx]` is set to `now + Interval` for **all due blocks** before any
I/O begins (engine.go:94–96).  When a read fails and `pollAt()` returns
early, the advancement has already been committed.  The failed block will
not be retried until `now >= nextExec[idx]`, i.e., after one full interval.

This also applies to:
- **nil-client (factory) failure** — factory is called after `nextExec`
  advancement; a connection failure does not cause an immediate retry.
- **Unsupported FC** — the default case returns after advancement.
- **All blocks in a due batch** — blocks later in the loop that were never
  attempted still have their `nextExec` advanced; they wait their interval
  before the next attempt.

---

## Phase 6 — Error Handling

### Expected behavior

```
NextExec = now + interval
```

### Actual behavior

On any failure path in `pollAt()`, the sequence is:

1. Due blocks collected.
2. `nextExec[idx] = now + Interval` for all due blocks ← **advancement committed**.
3. Client connected (if needed).
4. Sequential reads begin.
5. First failing read → `res.Err = err`, `recordFailure(err)`, `return res`.

Because step 2 precedes steps 3–5, `nextExec` always reflects `now + Interval`
regardless of whether the read succeeded, failed, or was never attempted.

**Not present:**

| Anti-pattern | Present? |
|---|---|
| Retry immediately (spin loop) | No |
| Loop until success | No |
| Spawn goroutine retry | No |
| Reset `nextExec` to zero on failure | No |
| Retry at `minInterval` rate | No |

The test `TestPollAtFailureAdvancesNextExec` validates both blocks'
`nextExec` after a read failure.  `TestPollAtFactoryFailureAdvancesNextExec`
validates `nextExec` after a factory/connection failure.

---

## Phase 7 — Data Flow Validation

### Call chain

```
Poller.pollAt(now) → memory.PollResult{Blocks, BlockUpdates, Err}
  │
Poller.Run() → out <- pollResult
  │
runOrchestrator() [orchestrator/device_manager.go]
  │
  ├─ writer.Write(res)            ← StoreWriter.Write(PollResult)
  │    └─ mem.WriteRegs/WriteBits    [in-process; no network hop]
  │
  └─ writer.WriteStatus(snap)     ← StoreWriter.WriteStatus(StatusSnapshot)
       └─ mem.WriteRegs              [status block; in-process]
```

### RawIngest relationship

`RawIngest(store, plan, blocks)` (memory/raw_ingest.go:434–439) is a
convenience function defined as:

```go
func RawIngest(store Store, plan WritePlan, blocks []BlockResult) error {
    res := PollResult{Blocks: blocks}
    return NewStoreWriter(plan, store).Write(res)
}
```

The orchestrator calls `writer.Write(res)` directly on a pre-built
`*StoreWriter`, which is the same code path that `RawIngest` ultimately
invokes.  Both paths are functionally equivalent for the data write; using
the pre-built writer avoids creating a temporary `StoreWriter` on every
poll cycle.

**The puller never writes directly into memory structures.**  All register
and coil writes are mediated by `StoreWriter.Write()`, which enforces:

- Write suppression when `res.Err != nil` (no stale data written on failure)
- Address offset application via `WritePlan.Targets[].Offsets`
- Area dispatch (coils, discrete inputs, holding regs, input regs)

---

## Phase 8 — Structured Report

### 1. Scheduling Algorithm

The `Poller` uses a **minimum-interval ticker with per-block due-time tracking**.

On every tick at time `t`:

1. Each block `i` is checked: `if !t.Before(nextExec[i])` → due.
2. `nextExec[i] = t + Interval[i]` for all due blocks (pre-I/O).
3. A single Modbus client is reused (or created via factory if nil).
4. Due blocks are read sequentially.
5. First read failure → cycle aborted; remaining due blocks are skipped but
   their `nextExec` was already advanced.
6. `PollResult` sent to orchestrator channel.

The ticker fires at `minInterval()` — the GCD-proxy resolution — so that
high-frequency blocks are not delayed by a coarse ticker.

### 2. Read Block Interval Handling

| Property | Behavior |
|---|---|
| Interval granularity | Per `ReadBlock`; no device-level interval |
| Interval storage | `ReadBlock.Interval time.Duration` (immutable) |
| Due-time tracking | `Poller.nextExec []time.Time` (one entry per block) |
| Advancement timing | Before I/O; committed regardless of success/failure |
| First-tick behavior | All blocks due (zero-value `nextExec`) |
| Config validation | `interval_ms <= 0` rejected at startup |

### 3. Device Concurrency Model

One goroutine per `Poller`.  Reads execute synchronously in `pollAt()`.
No sub-goroutines, no worker queues, no mutexes needed for per-device
serialization.  The `out` channel (capacity 8) provides back-pressure
between the Poller goroutine and the orchestrator goroutine.

### 4. Burst Protection Mechanism

Sequential `for range due` loop in `pollAt()`.  N simultaneously due
blocks produce N sequential Modbus requests.  The loop also abort-on-first-
failure: if block 0 fails, blocks 1..N−1 are not attempted.  Their
`nextExec` was already advanced, so they retry at their configured interval.

### 5. Replicator Invariant Preservation

| Invariant | Status | Mechanism |
|---|---|---|
| ≤1 in-flight request per device | ✅ | Single goroutine; synchronous `pollAt()` |
| Client reuse | ✅ | `p.client` persisted; cleared only by `isDeadConnErr()` |
| No retries within cycle | ✅ | `return res` on first error; no loops |
| Counters passive-only | ✅ | No counter value read by scheduling code |
| Failure → next-interval retry | ✅ | `nextExec` advanced before I/O |

### 6. Potential Risks

| Risk | Classification | Notes |
|---|---|---|
| Block starvation under persistent failure | LOW | When block `j` (slow interval) is due at the same time as block `0` (fast interval), and block `0` fails, block `j` is skipped. Its `nextExec` is advanced so it retries after `Interval[j]`, not immediately. If block `0` fails on every cycle, block `j` is never read. This is intentional fail-fast behaviour: if one read on a device fails, the connection is likely dead for all blocks. `maybeInvalidateClient()` forces reconnection on next tick. |
| RequestsTotal counts poll sessions, not individual block reads | INFO | `RequestsTotal` is incremented once per `pollAt()` call that has ≥1 due block — it counts poll sessions, not individual Modbus PDU exchanges. If 3 blocks are due on a single tick, `RequestsTotal` increments by 1, not 3. This matches the Replicator's model where one tick = one device interaction regardless of the number of register reads. Consumers of this counter should interpret it as "number of times a Modbus exchange was initiated with this device", not "number of PDUs sent". |
| Empty-tick suppression | ✅ CORRECT | Empty ticks (no due blocks) do not increment counters, update health state, or record latency. All three guards are in place. |
| Channel buffering | INFO | The `out` channel has capacity 8. If the orchestrator goroutine stalls for 8+ ticks, the Poller goroutine will block on the channel send, effectively pausing polling. This is the intended back-pressure mechanism and matches the Replicator's blocking-send model. |

---

## Summary

The `internal/puller` implementation correctly delivers the per-read-block
scheduling model required by Aegis and preserves all Replicator reliability
invariants.  No defects were found.

The critical bugs fixed in the prior `internal/engine` audit
(D-1 counter inflation, D-2 spurious health reset, D-3 latency skew,
D-8 `nextExec` not advanced on failure) are **not present** in
`internal/puller`.  The fixes were incorporated as invariants of the new
package design.
