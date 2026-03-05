# Lifecycle Audit Notes

## Overview

Aegis is a single binary composed of two independent lifetime domains:

| Domain | Owner | Lifetime |
|--------|-------|----------|
| **Process** | `main()` | From launch until OS signal |
| **Runtime Engine** | `RuntimeManager` | Controlled by Start/Stop/Restart actions |

The WebUI HTTP server belongs to the **Process** domain.  It starts once in
`main()` and is never touched by any runtime action.

---

## Context Hierarchy

```
context.Background()
    └─ processCtx   (created in main, cancelled on SIGINT/SIGTERM)
           └─ runtimeCtx  (created per Start/Restart, cancelled by StopRuntime/rebuild)
```

* **processCtx** — lives for the entire process lifetime.  Cancelled only when
  an OS shutdown signal is received.  The WebUI server does NOT use this context.
* **runtimeCtx** — derived from `processCtx` by `context.WithCancel`.  Cancelled
  by `StopRuntime()` or `rebuild()` to stop pollers and orchestrators.
  Recreated on each `Start`/`Restart` call.

Cancelling `runtimeCtx` does **not** cancel `processCtx`, so the WebUI HTTP
server is never affected.

---

## State Machine

The runtime engine follows a four-state machine enforced by `RuntimeManager`:

```
        ┌──────────────────────────────────────────┐
        │                                          ▼
   [STOPPED] ──StartRuntime()──► [STARTING] ──► [RUNNING]
        ▲                                          │
        │                                          │ StopRuntime() / Restart()
        └──────── [STOPPING] ◄────────────────────┘
```

| State | Allowed transitions |
|-------|-------------------|
| `STOPPED` | → `STARTING` via `StartRuntime()` or `rebuild()` |
| `STARTING` | → `RUNNING` (success) or back to `STOPPED` with error |
| `RUNNING` | → `STOPPING` via `StopRuntime()` or `rebuild()` |
| `STOPPING` | → `STOPPED` |

Calling `StartRuntime()` while not `STOPPED`, or `StopRuntime()` while not
`RUNNING`, returns a **409 Conflict** error with a descriptive message.  The
WebUI buttons are disabled accordingly.

---

## What Each Action Touches

### `StartRuntime()`
1. Checks state is `STOPPED` (or initial blank state); otherwise returns 409.
2. Loads and validates the active config YAML.
3. Sets state → `STARTING`.
4. Builds memory store, engine units, health store, authority registry.
5. **Pre-binds all Modbus TCP ports synchronously** using `net.Listen()`.
   - If any port fails to bind, all already-bound listeners are closed, state
     returns to `STOPPED` with `lastError`, and the error is returned to the
     caller (WebUI shows it in the error bar).
6. Creates a new `runtimeCtx` derived from `processCtx`.
7. Starts adapter goroutines using `adapter.NewServerWithListener` + `Serve()`.
8. Starts poller and orchestrator goroutines, tracked in `wg`.
9. Sets state → `RUNNING`.

**WebUI HTTP server: not touched.**

### `StopRuntime()`
1. Checks state is `RUNNING`; otherwise returns 409.
2. Sets state → `STOPPING`.
3. Calls `runtimeCancel()` — cancels `runtimeCtx`, stopping all pollers and
   orchestrators.
4. Calls `srv.Shutdown()` on each adapter — closes TCP listeners.
5. `wg.Wait()` — blocks until all goroutines have exited.
6. Sets state → `STOPPED`.

**WebUI HTTP server: not touched.**

### `Restart()` / `ReloadFromDisk()` / `ApplyConfig()` / `Rebuild()`
These all call the internal `rebuild()` function, which:
1. Cancels `runtimeCancel` (if set) and shuts down old adapters.
2. Calls `wg.Wait()` to drain all old goroutines **before** binding new ports.
   This eliminates the race where old goroutines still hold a port when new
   ones attempt to bind the same address.
3. Proceeds through the same `STARTING` → `RUNNING` path as `StartRuntime()`.

**WebUI HTTP server: not touched.**

### Process Startup (`main()`)
1. Creates `processCtx` with cancel.
2. Creates `RuntimeManager` with `processCtx`.
3. Loads and validates config; calls `rt.Start(cfg, yaml)` if valid.
4. **Starts the WebUI HTTP server once, unconditionally** (if enabled or if
   config was absent/invalid — to always provide a management endpoint).
5. Waits for OS signal.
6. Calls `processCancel()`, which propagates to `runtimeCtx` → stops all
   runtime goroutines.

---

## Why the WebUI Cannot Be Affected

1. **Separate goroutine**: The WebUI `srv.ListenAndServe()` runs in its own
   goroutine spawned in `main()`.  `RuntimeManager` holds no reference to it.
2. **No shared context**: `webui.Server` does not accept a context parameter.
   It runs until the process exits or an HTTP-level error occurs.
3. **No shared port**: The WebUI binds its own address (`:8080` by default).
   Modbus adapters bind their own ports (e.g. `:502`).  There is no overlap.
4. **No os.Exit**: No runtime action calls `os.Exit`, `log.Fatal`, or any other
   process-terminating function.

---

## Port Binding — Eliminating the Goroutine Race

**Old behaviour (bug):** `rebuild()` started goroutines that called
`adapter.Server.ListenAndServe()` (which calls `net.Listen()` internally).
If `rebuild()` was called again before the old goroutine had scheduled, the
old `Shutdown()` saw a nil listener and returned immediately.  The new goroutine
and the still-starting old goroutine then both attempted `net.Listen()` on the
same address, causing a silent "address already in use" failure.

**Fix:** `rebuild()` now calls `net.Listen()` synchronously before any goroutine
is started.  `adapter.NewServerWithListener()` stores the pre-bound listener in
the `Server` struct immediately, so `Shutdown()` always sees a non-nil `ln` and
reliably closes the listener and waits for `Serve()` to return before
returning control to the caller.

---

## Endpoints

| Method | Path | Auth | Description |
|--------|------|------|-------------|
| `POST` | `/api/login`                 | None    | Validate credentials; set session cookie. Returns `password_change_required` flag when default password is active. |
| `POST` | `/api/logout`                | Session | Invalidate session cookie; redirect to `/login`. |
| `POST` | `/api/change-password`       | Session | Hash and persist a new password; clear the password-change-required flag. |
| `GET`  | `/api/runtime/status`        | Session | `{ running, state, error }` |
| `GET`  | `/api/runtime/listeners`     | Session | Per-port `[{ port, status, error }]` |
| `GET`  | `/api/runtime/devices`       | Session | Per-device `[{ id, status, polling }]` |
| `GET`  | `/api/device/status`         | Session | Decoded status block for one device. Query params: `port`, `unit_id`, `slot`. |
| `POST` | `/api/runtime/start`         | Session | Start engine; 409 if not STOPPED. |
| `POST` | `/api/runtime/stop`          | Session | Stop engine; 409 if not RUNNING. |
| `POST` | `/api/restart`               | Session | Reload config from disk and rebuild runtime. |
| `POST` | `/api/reload`                | Session | Re-read config file, validate, and rebuild runtime. |
| `GET`  | `/api/config/view`           | Session | Config as a structured JSON view model (device list). |
| `PUT`  | `/api/config/apply`          | Session | Merge a JSON view model into the active config, validate, write to disk, and rebuild. |
| `GET`  | `/api/config/raw`            | Session | Active config as `text/yaml`. |
| `PUT`  | `/api/config/raw`            | Session | Replace config with raw YAML body; validate, write to disk, and rebuild. |
| `GET`  | `/api/config/export`         | Session | Download active `config.yaml` as an attachment. |
| `POST` | `/api/config/import`         | Session | Upload raw YAML; validate, write to disk, and rebuild. |
