# Aegis Authority Model — v1 Contract

This document describes the per-target authority model introduced in Aegis v1.
It defines how incoming Modbus client requests are evaluated, how upstream health
state is tracked per read block, and how the three authority modes are enforced.

---

## 1. Separation of Concerns

Aegis separates three distinct responsibilities:

| Layer | Responsibility |
|---|---|
| **Engine** | Polls upstream devices and records per-read-block health state. Never enforces authority. |
| **Adapter** | Enforces authority on incoming client requests using per-target mode and per-block health. Never writes to memory. |
| **Core** | Stores Modbus register data in-process. No awareness of authority or health. |

The engine produces health. The adapter enforces policy. The core stores memory.
These three layers do not import each other (except through `core.Store`).

**There is no global authority mode.** Authority is defined per replicator target.

---

## 2. Read-Block-Level Health Tracking

The engine tracks health state independently for each configured read block.
A read block is one entry in a replicator unit's `reads` list, identified by its
index within that list.

Each read block maintains:

| Field | Type | Meaning |
|---|---|---|
| `Timeout` | bool | True if the last poll attempt for this block timed out or the connection was lost |
| `ConsecutiveErrors` | int | Number of consecutive failed polls for this block |
| `LastExceptionCode` | byte | Most recent Modbus exception code received (0 = none) |
| `LastSuccess` | time.Time | Timestamp of the last successful poll for this block |
| `LastError` | time.Time | Timestamp of the last failed poll for this block |

### Polling Behavior

- **On success** → `Timeout = false`, `ConsecutiveErrors = 0`, `LastExceptionCode = 0`,
  `LastSuccess = now`.
- **On upstream Modbus exception** → `LastExceptionCode = code`, `ConsecutiveErrors++`,
  `Timeout = false`, `LastError = now`.
- **On timeout or connection loss** → `Timeout = true`, `ConsecutiveErrors++`,
  `LastError = now`.

Blocks that were not due at a given tick retain their previous health state
unchanged. Health state is never collapsed to device level.

---

## 3. Target Authority Mode

Authority is configured per replicator target (one `replicator.units[i]` entry).
The `target.mode` field accepts three values:

### Mode A — Standalone (Memory Authoritative)

- Client writes are **allowed**.
- Reads are **always served**; block health is not consulted.
- Upstream exceptions are **not forwarded** to clients.
- Suitable for configurations where Aegis acts as an independent memory device
  that clients can freely read and write.

### Mode B — Strict (default)

- Client writes are **rejected** with Modbus exception `0x01` (Illegal Function).
- For read requests:
  - If any covering read block has `Timeout = true` → return `0x0B` (Gateway Target Device Failed to Respond).
  - If any covering read block has `LastExceptionCode != 0` → forward that exception code.
  - Otherwise → serve memory normally.
- Suitable for configurations where stale or failed upstream data must not be
  served to clients.

### Mode C — Buffered

- Client writes are **rejected** with Modbus exception `0x01`.
- Reads are **always served**; block health is not consulted.
- Health state is still tracked and exposed in the status plane.
- Suitable for configurations where continuous read availability is required
  even during upstream failures.

---

## 4. Request Resolution Algorithm

On each incoming Modbus request:

1. **Resolve (listener_port, unit_id).**
   Derive the port from the server listener's TCP address. Match the MBAP unit ID.

2. **Look up the target entry.**
   If no replicator target is registered for (port, unit_id), skip authority
   enforcement and dispatch normally (e.g. status-only memory units).

3. **Write FCs (5, 6, 15, 16):**
   - Mode A → allow.
   - Mode B or C → return exception `0x01`.

4. **Read FCs (1, 2, 3, 4):**

   a. Find all read blocks for this target whose FC matches the request FC and
      whose address range overlaps with the request range.

   b. **Coverage check:** verify that the union of matching blocks fully covers
      the request range `[address, address+quantity)`. If there is any gap or
      the request extends beyond all blocks → return `0x02` (Illegal Data Address).

   c. **Health check (Mode B only):** for each covering block, query its health:
      - Timeout → return `0x0B`.
      - Exception code → forward that exception.

   d. Mode A and C → always serve memory.

### Why Partial Success Is Not Allowed

If a request spans multiple read blocks and any one of them is unhealthy, the
entire request is rejected. This is intentional:

- Partial responses would return data from some blocks but silently omit data
  from others, producing a result that the client cannot distinguish from a
  fully valid response.
- Deterministic rejection ensures clients know they are receiving stale or
  partial data and can take appropriate action.

---

## 5. Error Domain

The error domain for authority enforcement is the **read block**, not the device.

Two read blocks on the same device may have different health states because they
are polled at different intervals and may fail for different reasons. For example:

- Block A: FC4, address 0–9, interval 200 ms — may be a fast-moving sensor.
- Block B: FC4, address 10–19, interval 5000 ms — may be a slow configuration area.

If Block A times out but Block B is healthy, a read request covering only Block B
succeeds in Mode B. A read request covering only Block A is rejected with `0x0B`.

---

## 6. Exception Forwarding

In Mode B, upstream Modbus exceptions recorded against a read block are forwarded
verbatim to the client as Modbus exception responses. The exception code is the
raw Modbus application-layer code returned by the upstream device (e.g. `0x02`
for Illegal Data Address, `0x04` for Slave Device Failure).

Exception codes are only forwarded for covered read requests. Write requests are
rejected with a fixed `0x01` exception regardless of upstream state.

---

## 7. Configuration Reference

```yaml
replicator:
  units:
    - id: "plc1"
      source:
        endpoint: "192.168.1.100:502"
        timeout_ms: 1000
      reads:
        - fc: 4
          address: 0
          quantity: 10
          interval_ms: 200
        - fc: 4
          address: 10
          quantity: 10
          interval_ms: 5000
      target:
        listener_id: "primary"
        unit_id: 1
        mode: "B"          # "A", "B" (default), or "C"
```

- `mode` is per target memory surface (per `replicator.units[i].target`).
- `mode` defaults to `"B"` when omitted.
- Invalid mode values are rejected at startup.
- There is no global `authority_mode` field.

---

## 8. Constraints

- No global authority state. Authority is per-target.
- No device-level health enforcement.
- Engine produces health only; it does not enforce authority.
- Adapter enforces policy only; it does not write to memory.
- Core stores memory only; it is unaware of authority and health.
- Status block layout is unchanged (30 registers per slot, protocol-locked).
- This is the **v1 authority contract**. Future versions may extend but not
  break this contract.
