# Authority Mode Test Results

## Overview

Aegis supports three authority modes that control how the Modbus TCP server
handles client read and write requests.

---

## Mode Definitions

### strict (DEFAULT)

- **Client writes are rejected** with Modbus exception **0x01** (Illegal Function).
- **Reads are blocked** with exception **0x0B** (Gateway Target Device Failed to Respond)
  when upstream health is not OK.
- Reads are served from memory only when upstream health == OK.

### standalone

- **Client writes are allowed** (existing write behavior is preserved).
- **Reads always return memory** regardless of upstream health state.
- Health state does not gate reads.
- The engine may overwrite values on a successful poll.

### buffer

- **Client writes are rejected** with exception **0x01** (Illegal Function).
- **Reads always return memory** regardless of upstream health state.
- Health state does not block reads.

---

## Explicit Statements

- **strict is the default**: if `authority_mode` is absent from the configuration file,
  `config.Load()` normalises the value to `"strict"`.
- **standalone allows client writes**: FC 5, 6, 15, and 16 are processed normally.
  All other modes reject these FCs with exception 0x01.
- **buffer never blocks reads**: FC 1, 2, 3, and 4 are always served from memory,
  regardless of the upstream health state.

---

## Expected Behavior per Mode

| FC  | Description             | standalone | strict (healthy) | strict (unhealthy) | buffer |
|-----|-------------------------|------------|------------------|--------------------|--------|
| 1   | Read Coils              | ✅ data    | ✅ data          | ❌ 0x0B            | ✅ data |
| 2   | Read Discrete Inputs    | ✅ data    | ✅ data          | ❌ 0x0B            | ✅ data |
| 3   | Read Holding Registers  | ✅ data    | ✅ data          | ❌ 0x0B            | ✅ data |
| 4   | Read Input Registers    | ✅ data    | ✅ data          | ❌ 0x0B            | ✅ data |
| 5   | Write Single Coil       | ✅ allowed | ❌ 0x01          | ❌ 0x01            | ❌ 0x01 |
| 6   | Write Single Register   | ✅ allowed | ❌ 0x01          | ❌ 0x01            | ❌ 0x01 |
| 15  | Write Multiple Coils    | ✅ allowed | ❌ 0x01          | ❌ 0x01            | ❌ 0x01 |
| 16  | Write Multiple Registers| ✅ allowed | ❌ 0x01          | ❌ 0x01            | ❌ 0x01 |

✅ = success (data or echo response)  
❌ = Modbus exception response

---

## Exception Codes

| Code | Meaning                               | Used when                                      |
|------|---------------------------------------|------------------------------------------------|
| 0x01 | Illegal Function                      | Write FC in strict or buffer mode              |
| 0x0B | Gateway Target Device Failed to Respond | Read FC in strict mode with unhealthy upstream |

---

## Configuration Files

| File                          | authority_mode | Expected validation result |
|-------------------------------|----------------|---------------------------|
| `test_authority_default.yaml` | (absent)       | ✅ success; defaults to `strict` |
| `test_authority_strict.yaml`  | `strict`       | ✅ success                 |
| `test_authority_buffer.yaml`  | `buffer`       | ✅ success                 |
| `test_authority_standalone.yaml` | `standalone` | ✅ success               |
| `test_authority_invalid.yaml` | `unknown`      | ❌ validation error        |

---

## Health Source

Health is read from the status plane (in-process store) written by the replication
engine. The health code is stored at holding register offset 2 within the
30-register status block for each configured device. The adapter reads this value
directly from the store — it does not track health independently.

Health codes:

| Code | Meaning  |
|------|----------|
| 0    | Unknown  |
| 1    | OK       |
| 2    | Error    |
| 3    | Stale    |
| 4    | Disabled |

In strict mode, only code **1 (OK)** allows reads to proceed.
