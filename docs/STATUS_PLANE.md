# Aegis — Status Plane v1 (Locked Contract)

> **This document describes the locked v1 Status Plane architecture.**
> It is a protocol contract, not a design proposal. All described register layouts,
> magic constants, and behavioral rules are fixed and must not change without a
> versioned protocol bump.

---

## 1. Purpose

The Status Plane is the mechanism by which Aegis exposes the live health of each
upstream device to downstream Modbus clients. It occupies a dedicated region of
holding registers in the in-process memory store and is written exclusively by the
replication engine.

Key goals:

- Downstream clients can always read device health without out-of-band signalling.
- Reads are never blocked due to upstream failure.
- The status block is always present and always current, regardless of data plane
  success or failure.
- The layout is deterministic, enabling tooling to parse status blocks without
  configuration.

State sealing is not part of the v1 status plane.

---

## 2. Authority Model

The upstream source device is the sole authority for data. Aegis makes no
decisions about the correctness or meaning of values received from an upstream
device. Aegis does, however, make authoritative decisions about the **health** of
the upstream connection.

Rules:

- Data registers in the Aegis memory store always contain the last known good
  values from the upstream device.
- The upstream device owns those values. Aegis stores them but does not originate
  them.
- The status block is owned by Aegis. It reflects Aegis's observation of the
  upstream device's reachability and protocol compliance.
- No local flag or configuration can override the health state. Health is derived
  entirely from observed poll outcomes.

---

## 3. Behavioral Rules

### 3.1 Successful Poll

| Event | Data Registers | Status Block |
|---|---|---|
| Poll succeeds | Updated with values from upstream | Health → `OK (1)`; error code → `0`; `SecondsInError` reset to `0` |

### 3.2 Timeout

| Event | Data Registers | Status Block |
|---|---|---|
| Poll times out | **Not updated** (stale data preserved) | Health → `Error (2)`; error code set; `SecondsInError` incremented each second while in error |

### 3.3 Modbus Exception

| Event | Data Registers | Status Block |
|---|---|---|
| Upstream returns a Modbus exception response | **Not updated** | Health → `Error (2)`; error code contains the Modbus exception code |

### 3.4 Disconnection

| Event | Data Registers | Status Block |
|---|---|---|
| TCP connection lost (EOF, broken pipe, reset) | **Not updated** | Client is invalidated; next tick attempts reconnect; health → `Error (2)` until reconnect and successful poll |

---

## 4. Read Behavior

- The Aegis Modbus TCP server **always serves data** from the in-process memory
  store. It does not forward requests upstream.
- Memory always contains the **last known good data** from the upstream device.
  If the upstream has never been polled successfully, registers contain the
  zero-initialized values allocated at startup.
- Health and error detail are exposed through the status block (see Section 6),
  not through the data registers.
- Reads are **never blocked or refused** due to upstream failure.
- Header validation is not performed on the read path. The header is present for
  tooling and diagnostic purposes only. The Modbus server returns register values
  as-is; the interpretation of those values is the reader's responsibility.

---

## 5. Write Behavior

- **Data registers**: written only on a successful poll. If a poll produces any
  error — timeout, Modbus exception, connection failure — data registers are not
  updated. Stale data is never overwritten with partial or corrupt values.
- **Status block**: updated on every poll cycle, regardless of outcome. The
  status block is also updated on a one-second heartbeat tick (to increment
  `SecondsInError`) even when no new poll result arrives.
- There is no path by which a failed poll can corrupt the data registers.

---

## 6. Status Block Specification

### 6.1 Ownership and Sizing

- Each upstream device that has status reporting enabled owns **exactly one**
  status block.
- Each status block is exactly **30 holding registers (60 bytes)**.
- Blocks are sequential and fixed. The start address of block `n` is:

  ```
  block_start = base_address + (n × 30)
  ```

  where `base_address` is the holding register start of the status memory
  instance, and `n` is the zero-based block index (0–255).

### 6.2 Register Map

All offsets below are relative to `block_start`.

| Offset | Register | Name | Type | Description |
|---|---|---|---|---|
| 0 | base+0 | Header Word 0 | uint16 | `magic[0] << 8 \| magic[1]` — always `0x4147` |
| 1 | base+1 | Header Word 1 | uint16 | `magic[2] << 8 \| block_index` — always `0x53XX` where `XX` is the block index |
| 2 | base+2 | Health Code | uint16 | `0`=Unknown, `1`=OK, `2`=Error, `3`=Stale, `4`=Disabled |
| 3 | base+3 | Last Error Code | uint16 | Modbus exception code or transport error code; `0` when healthy |
| 4 | base+4 | Seconds In Error | uint16 | Seconds continuously in a non-OK health state; reset to `0` on recovery |
| 5 | base+5 | Device Name [0] | uint16 | Characters 1–2 of device name (ASCII, high byte first) |
| 6 | base+6 | Device Name [1] | uint16 | Characters 3–4 |
| 7 | base+7 | Device Name [2] | uint16 | Characters 5–6 |
| 8 | base+8 | Device Name [3] | uint16 | Characters 7–8 |
| 9 | base+9 | Device Name [4] | uint16 | Characters 9–10 |
| 10 | base+10 | Device Name [5] | uint16 | Characters 11–12 |
| 11 | base+11 | Device Name [6] | uint16 | Characters 13–14 |
| 12 | base+12 | Device Name [7] | uint16 | Characters 15–16 |
| 13–19 | base+13 | Reserved | uint16×7 | Always zero. Reserved for future protocol use. Must not be relied upon. |
| 20 | base+20 | Requests Total (low) | uint16 | Low word of lifetime poll request count |
| 21 | base+21 | Requests Total (high) | uint16 | High word of lifetime poll request count |
| 22 | base+22 | Responses Valid (low) | uint16 | Low word of lifetime successful poll count |
| 23 | base+23 | Responses Valid (high) | uint16 | High word of lifetime successful poll count |
| 24 | base+24 | Timeouts (low) | uint16 | Low word of lifetime timeout count |
| 25 | base+25 | Timeouts (high) | uint16 | High word of lifetime timeout count |
| 26 | base+26 | Transport Errors (low) | uint16 | Low word of lifetime transport error count |
| 27 | base+27 | Transport Errors (high) | uint16 | High word of lifetime transport error count |
| 28 | base+28 | Consecutive Fail (curr) | uint16 | Current consecutive failure streak |
| 29 | base+29 | Consecutive Fail (max) | uint16 | Lifetime maximum consecutive failure streak |

**Total: 30 registers (60 bytes).**

### 6.3 Health Code Values

| Value | Constant | Meaning |
|---|---|---|
| `0` | `Unknown` | Not yet polled since startup |
| `1` | `OK` | Last poll succeeded |
| `2` | `Error` | Last poll failed (timeout, exception, or disconnection) |
| `3` | `Stale` | Reserved for future use; not currently assigned by the engine |
| `4` | `Disabled` | Reserved for future use; not currently assigned by the engine |

### 6.4 32-Bit Counter Encoding

All counters that exceed 16-bit range are split across two consecutive registers
using **little-endian word order**: the low 16 bits occupy the lower register
address, the high 16 bits occupy the higher register address.

```
uint32 value V:
  register[low]  = V & 0xFFFF
  register[high] = (V >> 16) & 0xFFFF
```

To reconstruct: `value = register[low] | (register[high] << 16)`.

---

## 7. Header Binary Layout

```
 Byte offset within block_start:
 ┌───────┬───────┬───────┬───────┐
 │  0x00 │  0x01 │  0x02 │  0x03 │
 ├───────┼───────┼───────┼───────┤
 │magic[0│magic[1│magic[2│ index │
 │ 0x41  │ 0x47  │ 0x53  │ 0xNN  │
 └───────┴───────┴───────┴───────┘
   Register 0 (offset 0)   Register 1 (offset 1)
   ←── Header Word 0 ───→  ←── Header Word 1 ───→
```

- `magic[0..2]` = `0x41`, `0x47`, `0x53` — ASCII `"AGS"` (fixed, not configurable).
- `index` = sequential block index `0`–`255`.
- Registers are big-endian uint16 (standard Modbus byte order).

Example for block index `0`:
- Register 0 = `0x4147`
- Register 1 = `0x5300`

Example for block index `3`:
- Register 0 = `0x4147`
- Register 1 = `0x5303`

### 7.1 Magic Constant

The magic constant `0x41`, `0x47`, `0x53` (`"AGS"`) is embedded in the source
code as an unexported fixed constant and is not exposed to configuration. Any
status block not beginning with `0x4147` / `0x53XX` is not a valid Aegis v1
status block.

---

## 8. Why No Footer Is Required

A footer would serve either integrity verification (checksum/CRC) or framing
termination (end-of-block sentinel). Neither is necessary here:

- **Integrity**: Each 30-register block is written atomically under a single
  `sync.RWMutex` write lock. There is no window in which a reader could observe
  a partial write. A checksum would add complexity without providing additional
  correctness guarantees within this architecture.
- **Framing**: Block boundaries are fixed by address arithmetic
  (`base + index × 30`). A reader does not need to scan for a terminator. The
  end of each block is always at a known, pre-calculated address.

---

## 9. Why Header Validation Is Not Performed on the Read Path

The Aegis Modbus TCP server serves register values directly from the in-process
memory store without inspecting content. This is by design:

- The status block is in the same memory store as all other registers. The server
  does not distinguish between data registers and status registers; it applies
  identical read logic to both.
- Header validation belongs to the **consumer** of the status data (e.g., a
  monitoring client or diagnostic tool), not to the transport layer.
- Performing validation on the read path would require the server to have
  knowledge of the status plane layout, violating the clean separation between
  the transport adapter and the engine.
- If the header is incorrect or missing, it indicates a configuration error or
  memory initialisation issue that should be detected at startup, not at read
  time.

---

## 10. Reserved Registers Policy

Registers 13–19 (inclusive) within each status block are reserved.

- They are always written as zero by the engine.
- Consumers **must not** rely on any particular value in these registers.
- Reserved registers will not be repurposed within the v1 protocol. Any future
  use of these registers constitutes a new protocol version.

---

## 11. Rationale for Fixed Block Size

The 30-register block size was chosen for the following reasons:

1. **Address alignment**: 30 divides cleanly into common Modbus address spaces
   and aligns well with typical status memory allocations (e.g., 30, 60, 90
   registers for 1, 2, 3 devices).
2. **Capacity**: 30 registers (60 bytes) is sufficient to hold: a 4-byte header,
   a 16-character device name, health and error state, seconds-in-error, four
   32-bit counters, and two 16-bit consecutive failure counters — with 14 bytes
   of reserved headroom.
3. **No padding needed**: All 30 slots are assigned (header, state, name,
   counters, reserved). There is no internal fragmentation.
4. **Simplicity**: A fixed, power-of-ten-adjacent size avoids the complexity of
   variable-length blocks and makes address calculation trivial.

---

## 12. Determinism and Tooling Benefits

Because the status block layout is fixed and not configurable:

- Any tool that knows the base address and block index can parse all 30 registers
  without reading any Aegis configuration file.
- Monitoring dashboards can hard-code the register offsets.
- The magic header enables sanity-checking: a tool can verify it is reading the
  correct memory region before interpreting health codes.
- Automated testing can assert exact register values at exact offsets without
  abstraction layers.
- The layout is language-agnostic; any Modbus-capable client can consume it.

---

## 13. Locked v1 Contract

This document describes the **locked v1 Status Plane contract**. The following
are fixed and must not change without a versioned protocol revision:

- The magic constant (`0x41`, `0x47`, `0x53`).
- The block size (30 registers, 60 bytes).
- The block address formula (`base + index × 30`).
- The header layout (registers 0–1).
- The register assignments for offsets 2–12 and 20–29.
- The health code values (0–4).
- The 32-bit counter word order (low word at lower address).

Any extension or modification to the status block layout constitutes a new
protocol version and requires a new magic constant or version field.
