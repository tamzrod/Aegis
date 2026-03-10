# Aegis TCP Connection Behavior Audit

**Audit scope:** Aegis puller vs. Modbus Replicator — TCP connection lifecycle  
**Date:** 2026-03-10  
**Status:** Complete — no device-overload deviations found

---

## Objective

Confirm that Aegis does **not** introduce TCP connection patterns that could
overload Modbus field devices.

Specifically verify that:

- TCP connections are reused per device
- A new TCP connection is **not** created per read block
- A new TCP connection is **not** created per scheduler tick
- Connections are only recreated after fatal transport errors

---

## 1. Replicator — TCP Client Creation and Management

### Source references

| Component | File |
|---|---|
| `Poller` struct | `internal/poller/poller.go` |
| Poll loop runner | `internal/poller/runner.go` |
| Modbus TCP client | `internal/poller/modbus/client.go` |

### Client creation site

The Replicator creates the Modbus TCP client **once** during `Poller`
construction.  The client is stored as a field in the `Poller` struct and
reused for every call to `PollOnce()`.

```
Poller struct:
  client  *modbus.Client   ← created once, reused for all reads
  cfg     PollerConfig
```

`PollOnce()` iterates over all configured read blocks using the same
`p.client`:

```
func (p *Poller) PollOnce() PollResult {
    for _, block := range p.cfg.Reads {
        data, err := p.client.ReadXxx(block.Address, block.Quantity)
        if err != nil {
            p.maybeInvalidateClient(err)
            return PollResult{Err: err}
        }
        ...
    }
    return res
}
```

### Reconnect logic

`maybeInvalidateClient(err)` inspects the error with `isDeadConnErr()`:

- Matches: `eof`, `broken pipe`, `connection reset`, `connection aborted`,
  `use of closed network connection`, Windows WSA variants.
- Does **not** match: Modbus exception codes, network timeouts.

On match, `p.client` is set to `nil`.  On the next call to `PollOnce()`, a
fresh `net.DialTimeout("tcp", ...)` is issued to re-establish the connection.

### Connection lifecycle (Replicator)

```
[startup]
  Poller created → net.DialTimeout → p.client set

[steady state — each ticker tick]
  PollOnce():
    read block A  → p.client (reused)
    read block B  → p.client (reused)
    read block C  → p.client (reused)

[on fatal transport error]
  maybeInvalidateClient() → p.client = nil

[next tick after error]
  PollOnce():
    p.client == nil → net.DialTimeout → p.client set
    read block A  → p.client (new connection, then reused)
    ...
```

**Connection is never closed and re-opened between reads within the same
tick.**

---

## 2. Aegis — TCP Client Creation and Management

### Source references

| Component | File | Key symbol |
|---|---|---|
| `Poller` struct | `internal/puller/engine.go` | `Poller.client`, `Poller.factory` |
| Poll loop runner | `internal/puller/scheduler.go` | `Poller.Run()` |
| Client factory wiring | `internal/puller/device.go` | `buildUnit()` / `factory` closure |
| Modbus TCP client | `internal/engine/modbusclient/client.go` | `modbusclient.New()` |

### Client creation site — `buildUnit()` (device.go:61–67)

```go
factory := func() (Client, error) {
    return modbusclient.New(modbusclient.Config{
        Endpoint: u.Source.Endpoint,
        UnitID:   u.Source.UnitID,
        Timeout:  time.Duration(u.Source.TimeoutMs) * time.Millisecond,
    })
}
```

The `factory` closure is stored in `Poller.factory`.  **`modbusclient.New()`
is not called here.**  No TCP connection is opened during `buildUnit()`.

`NewPoller()` is called with `client: nil` — the client field starts empty.

### Lazy connection — `pollAt()` (engine.go:100–113)

```go
if p.client == nil {
    if p.factory == nil {
        res.Err = errors.New("poller: client is nil and no factory provided")
        p.recordFailure(res.Err)
        return res
    }
    c, err := p.factory()
    if err != nil {
        res.Err = err
        p.recordFailure(err)
        return res
    }
    p.client = c
}
```

`p.factory()` (i.e., `modbusclient.New()`) is called **only** when
`p.client == nil`.  After a successful connect, `p.client` is set and
reused until a fatal transport error clears it again.

### Reconnect logic — `maybeInvalidateClient()` (engine.go:262–270)

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

`isDeadConnErr()` (engine.go:272–287) matches:

```go
func isDeadConnErr(err error) bool {
    var ne net.Error
    if errors.As(err, &ne) && ne.Timeout() {
        return false      // timeouts do NOT invalidate
    }
    s := strings.ToLower(err.Error())
    return strings.Contains(s, "eof") ||
        strings.Contains(s, "broken pipe") ||
        strings.Contains(s, "connection reset") ||
        strings.Contains(s, "connection aborted") ||
        strings.Contains(s, "use of closed network connection") ||
        strings.Contains(s, "forcibly closed by the remote host") ||
        strings.Contains(s, "wsasend") ||
        strings.Contains(s, "wsarecv")
}
```

Modbus exception codes and TCP timeouts leave `p.client` intact.  Only hard
transport failures (dead socket) clear it.

### Scheduler — `Poller.Run()` (scheduler.go:18–35)

```go
func (p *Poller) Run(ctx context.Context, out chan<- memory.PollResult) {
    ticker := time.NewTicker(p.minInterval())
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            return
        case t := <-ticker.C:
            out <- p.pollAt(t)
        }
    }
}
```

- One goroutine per `Poller`.
- `pollAt()` is **synchronous** — the goroutine blocks until all due reads
  complete before it can receive the next ticker tick.
- No sub-goroutines are spawned.

### Connection lifecycle (Aegis)

```
[startup]
  buildUnit() → factory closure stored in Poller.factory
  p.client = nil

[first tick with due blocks]
  pollAt():
    p.client == nil → p.factory() → net.DialTimeout → p.client set
    read block A  → p.client (new connection)
    read block B  → p.client (reused)
    read block C  → p.client (reused)

[subsequent ticks — some blocks due, some not]
  pollAt():
    p.client != nil → skip factory call
    read due block(s) → p.client (reused)

[on fatal transport error mid-cycle]
  maybeInvalidateClient() → p.client = nil
  pollAt() returns early; remaining due blocks skipped

[next tick with due blocks after error]
  pollAt():
    p.client == nil → p.factory() → net.DialTimeout → p.client set
    read due block(s) → p.client (new connection, then reused)
```

**Connection is never closed and re-opened between reads within the same
tick.**

---

## 3. Reuse Behavior Preserved?

**Yes.**

| Property | Replicator | Aegis | Match? |
|---|---|---|---|
| `client` stored in struct | `Poller.client` | `Poller.client` | ✅ |
| Factory / dial called once at connect | Yes | Yes (lazy, on first due tick) | ✅ |
| Client reused across all reads in one tick | Yes | Yes | ✅ |
| Client reused across ticks | Yes | Yes | ✅ |
| Client invalidated on fatal transport error only | `isDeadConnErr()` | `isDeadConnErr()` (identical classifier) | ✅ |
| Timeouts do NOT invalidate client | Yes | Yes (`ne.Timeout()` guard) | ✅ |
| Modbus exceptions do NOT invalidate client | Yes | Yes (`modbusExceptionErr` not in classifier) | ✅ |
| Reconnect on next tick (not immediately) | Yes | Yes | ✅ |
| One TCP client per device (not per block) | Yes | Yes | ✅ |

---

## 4. Deviations Between Implementations

### No connection-lifecycle deviations

The Aegis connection lifecycle faithfully reproduces the Replicator model.
The only structural difference is that the Replicator eagerly dials on
`Poller` construction, while Aegis dials lazily on the first tick where a
block is due.  This is a safe difference:

- The Replicator dials even if a polling unit is misconfigured or the device
  is unreachable; the dial failure propagates at construction time.
- Aegis defers the dial to the first poll attempt; the error is returned via
  the `PollResult` channel rather than failing the startup sequence.

Neither pattern creates more connections than the other.  Both use exactly
one TCP connection per device per poller lifetime.

### Aegis-specific behaviors that do not affect the connection model

| Behavior | Classification | Effect on connections |
|---|---|---|
| Per-block independent intervals | SAFE — scheduling extension | No effect; all blocks still share one `p.client` |
| Empty ticks (no blocks due) | SAFE — new concept | `p.client` is not touched; no connect/disconnect |
| `nextExec` advanced before I/O | SAFE — failure-pacing fix | No effect on connection lifecycle |
| Lazy connect on first due tick | SAFE — minor timing difference | No extra connections; same total count |

---

## 5. Device-Overload Risk Assessment

### Anti-patterns checked

| Anti-pattern | Present in Aegis? | Evidence |
|---|---|---|
| `client := NewClient()` inside the read loop | **No** | `NewClient()` / `factory()` is guarded by `if p.client == nil`; the factory is not called on every tick |
| Connect → read → disconnect per cycle | **No** | `p.client` persists across ticks; no `p.client.Close()` after successful reads |
| New connection per read block | **No** | All blocks in `pollAt()` share the same `p.client` instance |
| Reconnect on timeout (aggressive retry) | **No** | `isDeadConnErr()` explicitly returns `false` for `ne.Timeout()` |
| Concurrent connections per device | **No** | Single goroutine per `Poller`; `pollAt()` is synchronous |
| Reconnect on Modbus exception | **No** | `ModbusException` does not satisfy `isDeadConnErr()` |
| Reconnect on empty tick | **No** | `pollAt()` returns before the `p.client == nil` check when `len(due) == 0` |

### Conclusion

Aegis introduces **no** connection patterns that could overload Modbus
devices.  Device connection frequency is bounded by the configured read
intervals, and reconnection is gated exclusively on hard transport failures.

A device with a single polling unit configured with three read blocks at
1 s, 5 s, and 60 s intervals will receive:

- At most **one TCP connection** over its lifetime (or one per fatal error).
- Sequential Modbus requests — never concurrent.
- Reconnection attempts at the cadence of the fastest due block, not
  continuous retries.

---

## 6. Audit Checklist

| # | Item | Status | Notes |
|---|---|---|---|
| 1 | TCP client created once per device poller | ✅ PASS | `factory()` guarded by `if p.client == nil`; see `engine.go:100–113` |
| 2 | Connections not opened/closed inside polling loop | ✅ PASS | No `Close()` called after successful reads; `p.client` persists |
| 3 | Multiple reads for same device share a single TCP client | ✅ PASS | All blocks in `pollAt()` for-range use the same `p.client` |
| 4 | Reconnect only on fatal transport errors | ✅ PASS | `maybeInvalidateClient()` / `isDeadConnErr()` — EOF, broken pipe, reset, aborted, closed, WSA |
| 5 | Scheduler does not cause concurrent connection attempts | ✅ PASS | Single goroutine per `Poller`; synchronous `pollAt()`; no sub-goroutines |

---

## 7. Key File Locations

| Topic | File | Symbol |
|---|---|---|
| Poller struct & TCP client field | `internal/puller/engine.go:38–47` | `Poller.client`, `Poller.factory` |
| Lazy connect logic | `internal/puller/engine.go:100–113` | `if p.client == nil` block |
| Multi-block sequential reads | `internal/puller/engine.go:118–187` | `for _, idx := range due` |
| Reconnect / invalidation gate | `internal/puller/engine.go:262–270` | `maybeInvalidateClient()` |
| Dead-connection classifier | `internal/puller/engine.go:272–287` | `isDeadConnErr()` |
| Poll loop (single goroutine) | `internal/puller/scheduler.go:18–35` | `Poller.Run()` |
| Factory wiring per device | `internal/puller/device.go:61–67` | `factory` closure in `buildUnit()` |
| TCP dial implementation | `internal/engine/modbusclient/client.go:48–73` | `modbusclient.New()` |
