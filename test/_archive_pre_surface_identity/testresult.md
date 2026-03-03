<!-- ARCHIVED — DO NOT USE
     This file reflects the pre–Surface Identity Rule architecture.
     It allowed multiple devices to share a target surface.
     As of ARCHITECTURE_LOCK.md (Surface Identity Rule),
     (port, unit_id) must serve exactly one device.
     This file is preserved for historical reference only.
-->

# Configuration Integration Test — Expected Results

## What `test.yaml` represents

A minimal but complete Aegis configuration for a single upstream device (`PLC1`)
that replicates Modbus holding registers into a local in-process memory store and
writes a device status block into a dedicated status memory region.

---

## Validation result

**Expected: success (no error)**

The configuration satisfies every constraint enforced by `config.Validate()`:

- Listener ID is non-empty and unique.
- Listen address is a valid `host:port` with a port in the range `[1, 65535]`.
- Each memory definition has `unit_id > 0` and `unit_id <= 255`.
- No two memory definitions share the same `(port, unit_id)` pair.
- All area start+count values fit within the 16-bit Modbus address space.
- Replicator unit ID is non-empty and unique.
- `source.endpoint` is non-empty.
- `source.timeout_ms > 0`.
- `source.device_name` contains only ASCII characters and is ≤ 16 characters.
- At least one read block is present; its FC is 1–4 and quantity > 0.
- `target.listener_id` references a declared listener.
- `target.unit_id > 0`.
- `source.status_slot` is set, and `target.status_unit_id` is also set and > 0.
- `poll.interval_ms > 0`.

---

## Expected structure counts

| Metric | Expected value |
|---|---|
| Listeners | 1 |
| Memory definitions | 2 (unit_id=1 data, unit_id=255 status) |
| Replicator units | 1 |
| Read blocks | 1 (FC3, address=0, quantity=10) |
| Status blocks | 1 (unit_id=255, block index 0) |

---

## Expected memory layout summary

| MemoryID (port, unit_id) | Area | Start | Count |
|---|---|---|---|
| (502, 1) | Holding Registers | 0 | 100 |
| (502, 1) | Input Registers | 0 | 50 |
| (502, 255) | Holding Registers | 0 | 30 |

---

## Expected block index mapping

```
device "plc1"  →  status_slot = 0  →  block_index = 0
block_start = base_address + (block_index × 30)
            = 0 + (0 × 30)
            = register 0 in (port=502, unit_id=255)
```

The status memory for `unit_id=255` holds exactly 30 registers, which is the
minimum required to store one complete status block starting at register 0.

---

## Explicit statements

- **Validation must succeed**: `config.Validate()` must return `nil`.
- **No runtime components are started**: the integration test calls only
  `config.Load()`, `config.Validate()`, and `config.BuildMemStore()`.
  No Modbus TCP server is bound, no polling goroutines are launched, and no
  network connections are made.
