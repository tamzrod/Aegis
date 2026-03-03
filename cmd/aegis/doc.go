// Package main is the Aegis replication gateway entry point.
//
// # File organisation by intent domain
//
//	main.go         — IO handling
//	                  Config load, memory-store build, Modbus TCP server adapter
//	                  startup, OS signal handling.
//
//	orchestrator.go — Scheduling + policy enforcement
//	                  Per-unit poll-result consumption loop (select over channel
//	                  and secTicker).  Enforces the write-change policy: status
//	                  is written to the store only when the snapshot differs from
//	                  the previously written value.
//	                  Couples to engine only through narrow local interfaces:
//	                    counterSource  — abstracts *engine.Poller (Counters only)
//	                    pollWriter     — abstracts *engine.StoreWriter
//	                    blockHealthWriter — abstracts *engine.BlockHealthStore (write side)
//
//	health.go       — State mutation
//	                  Per-read-block health tracking: updateBlockHealth applies
//	                  one BlockUpdate to the mutable health record for a block.
//
//	snapshot.go     — Data transformation
//	                  Pure functions that derive StatusSnapshot fields from
//	                  PollResult (applyPollResult) and TransportCounters
//	                  (applyCounters).  Both functions are value-in / value-out
//	                  with no side effects.
//
// # Dependency direction
//
//	main.go
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
