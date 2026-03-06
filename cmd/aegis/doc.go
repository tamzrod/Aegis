// Package main is the Aegis replication gateway entry point.
//
// # File organisation by intent domain
//
//	main.go                — IO handling
//	                         Config load, memory-store build, Modbus TCP server adapter
//	                         startup, OS signal handling.
//
//	runtime.go             — RuntimeManager core
//	                         Struct definition, construction (NewRuntimeManager), and the
//	                         internal rebuild engine that atomically stops the current
//	                         runtime, builds new components, pre-binds all adapter ports,
//	                         and starts goroutines.
//
//	runtime_lifecycle.go   — Lifecycle control
//	                         External lifecycle operations: StopRuntime, StartRuntime,
//	                         Stop (graceful shutdown), RuntimeStatus, ListenerStatuses.
//
//	runtime_status.go      — Observability
//	                         DeviceStatuses, ReadDeviceStatus, ReadViewerRegisters, and
//	                         supporting helpers (healthCodeToString, deriveDeviceStatus,
//	                         isDevicePolling, unpackBitsToUint16, bytesToUint16s).
//
//	runtime_config.go      — Config management
//	                         ApplyConfig, ReloadFromDisk, UpdatePasswordHash, and the
//	                         shared atomicWriteConfig helper.
//
//	orchestrator.go        — Scheduling + policy enforcement
//	                         Per-unit poll-result consumption loop (select over channel
//	                         and secTicker).  Enforces the write-change policy: status
//	                         is written to the store only when the snapshot differs from
//	                         the previously written value.
//	                         Couples to engine only through narrow local interfaces:
//	                           counterSource  — abstracts *engine.Poller (Counters only)
//	                           pollWriter     — abstracts *engine.StoreWriter
//	                           blockHealthWriter — abstracts *engine.BlockHealthStore (write side)
//
//	health.go              — State mutation
//	                         Per-read-block health tracking: updateBlockHealth applies
//	                         one BlockUpdate to the mutable health record for a block.
//
//	snapshot.go            — Data transformation
//	                         Pure functions that derive StatusSnapshot fields from
//	                         PollResult (applyPollResult) and TransportCounters
//	                         (applyCounters).  Both functions are value-in / value-out
//	                         with no side effects.
//
//	latency.go             — Poll latency tracker
//	                         PollLatencyTracker records per-unit last/avg/max poll
//	                         latency in milliseconds.  Passive-only: does not
//	                         influence control flow.
//
//	views.go               — WebUI view adapters
//	                         runtimeView and configView satisfy the view.RuntimeView
//	                         and view.ConfigView interfaces using startup-captured
//	                         static state.
//
// # Dependency direction
//
//	main.go
//	  ├─► runtime.go          (RuntimeManager construction + rebuild)
//	  │     ├─► runtime_lifecycle.go   (Stop, StopRuntime, StartRuntime)
//	  │     ├─► runtime_status.go      (DeviceStatuses, ReadDeviceStatus, ReadViewerRegisters)
//	  │     └─► runtime_config.go      (ApplyConfig, ReloadFromDisk, UpdatePasswordHash)
//	  └─► orchestrator.go   (scheduling, policy)
//	        ├─► [counterSource]  satisfied by *engine.Poller
//	        ├─► [pollWriter]     satisfied by *engine.StoreWriter → core.Store  (IO)
//	        ├─► [blockHealthWriter] satisfied by *engine.BlockHealthStore
//	        ├─► health.go    (state mutation)
//	        └─► snapshot.go  (data transformation)
//
// Arrows point in the direction of the dependency (A → B means A calls B).
// Brackets denote local interfaces defined in orchestrator.go.
// No cycles exist between domain files.
package main
