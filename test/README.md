# test/

This directory contains reference configuration files used by Aegis integration tests.

---

## Active Files

| File | Purpose |
|---|---|
| `test.yaml` | Current integration test configuration. Used by `internal/config/config_integration_test.go`. |

`test.yaml` conforms to the current derived-memory model:

- Memory surfaces (data and status) are derived at runtime from `replicator.units[*].target`.
- No `server.listeners` or explicit memory definitions are present.
- Each `(target.port, target.unit_id)` is assigned to exactly one replicator unit.

---

## Archived Files

Files in `_archive_pre_surface_identity/` reflect the **old architecture** and must
not be used in active tests or as templates for new configuration.

### Why they are archived

These files were written before the **Surface Identity Rule** was enforced
(see `docs/ARCHITECTURE_LOCK.md`). They use a now-removed schema that included:

- `server.listeners` with explicit memory definitions
- `authority_mode` as a top-level field
- `target.listener_id` references

All of these constructs were removed when the config schema was simplified to the
derived-memory model.

### Surface Identity Rule (summary)

> One `(target.port, target.unit_id)` surface must serve exactly one replicator unit.
> Duplicate surfaces are rejected by `config.Validate()`.
> There is no surface merging. Mode is per device, not per read.

See `docs/ARCHITECTURE_LOCK.md` for the full specification.

---

## Adding New Test Configurations

New reference YAML files added to `test/` must:

1. Omit `server.listeners` — memory is derived automatically.
2. Assign each `(target.port, target.unit_id)` to exactly one replicator unit.
3. Set `target.mode` explicitly (`"A"`, `"B"`, or `"C"`).
4. Pass `config.Validate()` without errors (unless the file is intentionally testing
   a validation failure, in which case document that clearly in the file header).
