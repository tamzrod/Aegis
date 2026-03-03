# Aegis — Architecture Lock

This document records design decisions that are **locked** and must not be changed
without explicit review. All new code and configuration changes must conform to these
rules.

---

## Surface Identity Rule

**One device per `(target.port, target.unit_id)`.**

### Rules

1. **One replicator unit represents one device.**
   A replicator unit (`replicator.units[*]`) models exactly one upstream Modbus device.
   It defines the device's source address, the register blocks to read, and the in-process
   memory surface to write.

2. **One `(port, unit_id)` surface must serve exactly one device.**
   The pair `(target.port, target.unit_id)` is the unique identity of an in-process memory
   surface. No two replicator units may share this pair.

3. **Duplicate `(port, unit_id)` is a hard validation error.**
   `config.Validate()` rejects any configuration where two or more replicator units claim
   the same `(target.port, target.unit_id)`. The process must not start.

   Error format:
   ```
   replicator.units[N] (id): duplicate target surface (port=X, unit_id=Y) already assigned to unit "Z"
   ```

4. **No surface merging.**
   There is no runtime logic that groups or merges blocks from multiple replicator units
   under the same memory surface. `BuildAuthorityRegistry` assigns exactly one
   `targetEntry` per surface, and that entry is owned by exactly one replicator unit.

5. **Mode is per device (per replicator unit), not per read.**
   The authority mode (`A`, `B`, `C`) is set on `target.mode` of a replicator unit and
   governs all reads and writes to that unit's memory surface. There is no per-read
   mode override.

6. **Health namespace is per replicator unit.**
   Block health records are keyed by `(replicatorUnitID, blockIdx)`.
   Because each surface belongs to exactly one unit, health lookups always use
   `entry.replicatorID` directly. There is no cross-unit health lookup.

### Invariant summary

| Invariant | Enforced by |
|---|---|
| Unique `(port, unit_id)` per unit | `config.Validate()` → `validateTargetSurfaces()` |
| One `targetEntry` per surface | `adapter.BuildAuthorityRegistry()` |
| Health lookup scoped to one unit | `adapter.AuthorityRegistry.Enforce()` |

### Why this rule exists

Allowing multiple units to share a surface creates non-deterministic write ordering,
ambiguous mode ownership, and incorrect health attribution. A surface that belongs to
two devices cannot have a single authoritative health state. Forbidding shared surfaces
eliminates this class of bugs entirely.
