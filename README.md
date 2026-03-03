# Aegis
Aegis is a single-binary Modbus memory backplane with an integrated replication engine that shields clients from upstream instability.

## Usage

```
aegis <config.yaml>
```

Configuration is loaded once at startup. Invalid config causes immediate exit. There is no hot reload.

## Sample Configuration

A ready-to-use example config is provided at [`aegis.example.yaml`](aegis.example.yaml).

```yaml
server:
  listeners:
    - id: "primary"
      listen: ":502"
      memory:
        # Data memory for device unit_id=1
        - unit_id: 1
          holding_registers:
            start: 0
            count: 100
          input_registers:
            start: 0
            count: 50
          coils:
            start: 0
            count: 32
          discrete_inputs:
            start: 0
            count: 32

        # Optional device status memory (unit_id=255)
        # The replication engine writes a 30-register status block here.
        - unit_id: 255
          holding_registers:
            start: 0
            count: 30

replicator:
  units:
    - id: "plc1"
      source:
        endpoint: "192.168.1.100:502"
        unit_id: 1
        timeout_ms: 1000

        # Optional device status block (comment out to disable)
        status_slot: 0        # writes status starting at holding register 0
        device_name: "PLC1"   # up to 16 ASCII characters

      reads:
        - fc: 3               # Read Holding Registers
          address: 0
          quantity: 10
          interval_ms: 200    # fast-moving data: 200 ms
        - fc: 4               # Read Input Registers
          address: 0
          quantity: 5
          interval_ms: 5000   # slow-moving data: 5 s

      target:
        listener_id: "primary"  # writes into the "primary" listener's memory
        unit_id: 1              # target memory unit_id in the store

        # status_unit_id: required when source.status_slot is set
        status_unit_id: 255

        # mode: authority mode for this target (default = "B")
        # A = standalone (allows writes, always serves reads)
        # B = strict     (rejects writes, blocks reads on unhealthy)
        # C = buffered   (rejects writes, always serves reads)
        mode: "B"
```

## Configuration Reference

### `server.listeners`

| Field | Type | Description |
|---|---|---|
| `id` | string | Unique listener identifier. Referenced by `replicator.units[].target.listener_id`. |
| `listen` | string | TCP address to bind (e.g. `":502"` or `"0.0.0.0:502"`). |
| `memory` | list | One or more memory blocks served on this listener. |

### `server.listeners[].memory`

| Field | Type | Description |
|---|---|---|
| `unit_id` | uint16 | Modbus unit ID (1–255). |
| `holding_registers` | `{start, count}` | Holding register area. `count: 0` disables the area. |
| `input_registers` | `{start, count}` | Input register area. |
| `coils` | `{start, count}` | Coil area. |
| `discrete_inputs` | `{start, count}` | Discrete input area. |

### `replicator.units[].source`

| Field | Type | Description |
|---|---|---|
| `endpoint` | string | Upstream device address (`"host:port"`). |
| `unit_id` | uint8 | Modbus unit ID on the upstream device (1–255). |
| `timeout_ms` | int | Read timeout in milliseconds. |
| `status_slot` | uint16 (optional) | Zero-based slot index in the status memory block. Enables status reporting when set. |
| `device_name` | string (optional) | Up to 16 ASCII characters written into the status block. Only used when `status_slot` is set. |

### `replicator.units[].reads`

| Field | Type | Description |
|---|---|---|
| `fc` | uint8 | Function code: `1` (coils), `2` (discrete inputs), `3` (holding registers), `4` (input registers). |
| `address` | uint16 | Start address on the upstream device. |
| `quantity` | uint16 | Number of registers or coils to read. |
| `interval_ms` | int | Poll interval in milliseconds (must be > 0). |

### `replicator.units[].target`

| Field | Type | Description |
|---|---|---|
| `listener_id` | string | ID of the server listener to write into. |
| `unit_id` | uint16 | Target memory unit ID in the store (1–255). |
| `status_unit_id` | uint16 (optional) | Unit ID of the status memory block. Required when `source.status_slot` is set. |
| `mode` | string | Authority mode: `"A"` (standalone), `"B"` (strict, default), `"C"` (buffered). |
| `offsets` | map (optional) | Per-FC address delta applied when writing to the store. Key is FC number (1–4); missing FCs default to 0. |

## Authority Modes

| Mode | Client Writes | Client Reads |
|---|---|---|
| `A` — Standalone | Allowed | Always served |
| `B` — Strict (default) | Rejected (`0x01`) | Blocked with `0x0B` when upstream is unhealthy |
| `C` — Buffered | Rejected (`0x01`) | Always served |

## Further Reading

- [`docs/architecture.md`](docs/architecture.md) — system architecture and component breakdown
- [`docs/AUTHORITY_MODEL.md`](docs/AUTHORITY_MODEL.md) — authority mode details and request resolution algorithm
- [`docs/STATUS_PLANE.md`](docs/STATUS_PLANE.md) — status block register layout and health codes
