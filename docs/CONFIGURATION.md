# Aegis — Configuration Reference

## Overview

Aegis configuration is a single YAML file passed as a command-line argument.
Memory layout (data and status) is derived entirely from the `replicator` section.
There is no explicit `server` section; Modbus TCP listeners are created automatically
from the unique `target.port` values declared by replicator units.

---

## Top-Level Structure

```yaml
replicator:
  units:
    - ...
```

The `server` section has been removed. All configuration is driven by `replicator.units`.

---

## replicator.units[]

Each entry declares one upstream device poll loop.

### Required fields

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique identifier for this unit. |
| `source.endpoint` | string | `"ip:port"` of the upstream Modbus TCP device. Must be a strict IPv4 address (no hostnames). Port must be in range [1, 65535]. Example: `"192.168.1.100:502"`. |
| `source.unit_id` | uint8 | Modbus unit ID on the upstream device. |
| `source.timeout_ms` | int | Read timeout in milliseconds. Must be > 0. |
| `reads[]` | list | At least one read block required. |
| `target.port` | uint16 | TCP port of the local Modbus listener. One listener per unique port. |
| `target.unit_id` | uint16 | Unit ID of the data memory surface in the local store. Range: [1, 255]. |
| `target.mode` | string | Authority mode: `"A"`, `"B"`, or `"C"`. Default: `"B"`. |

### Optional fields

| Field | Type | Description |
|---|---|---|
| `source.device_name` | string | Up to 16 ASCII characters. Written into the status block header. |
| `target.status_unit_id` | uint16 | Unit ID for the status memory surface. When omitted, defaults to `100`. Required (explicitly or by default) when `target.status_slot` is set. Must differ from all data `unit_id` values on the same port. |
| `target.status_slot` | uint16 | Zero-based slot index for this unit's 30-register status block. Required when `target.status_unit_id` is set. Must be unique per `(port, status_unit_id)`. |
| `target.offsets` | map[int]uint16 | Per-FC address deltas applied to destination addresses. Key is FC (1–4); missing FC defaults to 0. |

---

## reads[]

Each read block declares one Modbus read and its independent poll cadence.

| Field | Type | Description |
|---|---|---|
| `fc` | uint8 | Function code: 1 (coils), 2 (discrete inputs), 3 (holding registers), 4 (input registers). |
| `address` | uint16 | Start address on the upstream device (zero-based). |
| `quantity` | uint16 | Number of coils or registers to read. Must be > 0. |
| `interval_ms` | int | Poll interval in milliseconds. Must be > 0. |

---

## Authority Modes

| Mode | Writes | Reads |
|---|---|---|
| `A` (Standalone) | Allowed | Always served |
| `B` (Strict) | Rejected (0x01) | Gated by block health; 0x0B on timeout/unprimed, upstream exception forwarded |
| `C` (Buffered) | Rejected (0x01) | Always served |

---

## Memory Derivation

### Data Memory

For each unique `(port, unit_id)` pair across all replicator units:

- All read blocks targeting this surface are collected.
- For each FC independently, the **bounding range** is computed:
  `[min(address), max(address + quantity))`.
- One `AreaLayout` per FC is allocated covering the full bounding range.
- Reads within the bounding range but not covered by any segment are **holes**.

### Status Memory

For each unique `(port, status_unit_id)` pair:

- The maximum `status_slot` value across all units targeting this surface is found.
- Status memory size = `(max_slot + 1) * 30` holding registers, starting at address 0.
- Each unit's status block occupies registers `[slot * 30, slot * 30 + 30)`.

---

## Serving Rules

### Inside bounding range and covered by a segment

- Mode A / C: always serve.
- Mode B: serve if healthy; 0x0B if timeout or unprimed; forward upstream exception code.

### Inside bounding range but in a hole (not covered)

- Mode A / C: serve zero-filled response.
- Mode B: return 0x02 (Illegal Data Address).

### Outside bounding range

- All modes: return 0x02 (Illegal Data Address).

---

## Validation Rules

The process exits immediately on any of the following:

- No replicator units defined.
- Duplicate replicator unit IDs.
- `source.endpoint` is not a valid strict IPv4:port string (hostnames are not accepted).
- `source.timeout_ms` is not > 0.
- `target.port == 0`.
- `target.unit_id` not in [1, 255].
- `target.status_slot` set without `target.status_unit_id`.
- `target.status_unit_id` equals any data `unit_id` on the same port.
- Duplicate `status_slot` for the same `(port, status_unit_id)`.
- Duplicate `(port, unit_id)` across different replicator units.
- Duplicate read block `(fc, address, quantity)` within the same unit.
- Invalid FC (not 1–4), zero quantity, or non-positive `interval_ms` in any read block.
- Unknown or empty `target.mode`.

---

## Example

```yaml
replicator:
  units:
    - id: "plc1"
      source:
        endpoint: "192.168.1.100:502"
        unit_id: 1
        timeout_ms: 1000
        device_name: "PLC1"

      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 200
        - fc: 4
          address: 0
          quantity: 5
          interval_ms: 5000

      target:
        port: 502
        unit_id: 1
        status_unit_id: 255
        status_slot: 0
        mode: "B"
```

This configuration:
- Creates a Modbus TCP listener on port 502.
- Allocates data memory for `(port=502, unit_id=1)`:
  - FC3 holding registers: bounding range `[0, 10)`.
  - FC4 input registers: bounding range `[0, 5)`.
- Allocates status memory for `(port=502, unit_id=255)`:
  - 30 holding registers (1 slot × 30 registers).
