// cmd/aegis/orchestrator.go — scheduling and policy enforcement domain
// Responsibility: drives the poll-result consumption loop for one replication unit.
//
// Scheduling:
//   - Owns the per-unit secTicker that increments SecondsInError every second.
//   - Selects over the poll-result channel and the shutdown context.
//
// Policy enforcement:
//   - Writes status only when the snapshot has changed (write-change policy).
//   - Delegates data-write and status-write decisions to the StoreWriter.
//
// This file does NOT own health-state mutation (see health.go) or snapshot
// construction logic (see snapshot.go); it only coordinates them.
package main

import (
	"context"
	"log"
	"time"

	"github.com/tamzrod/Aegis/internal/engine"
)

// counterSource is the subset of *engine.Poller used by the orchestrator.
// Only transport counters are needed here; the Run loop is started in main.go.
type counterSource interface {
	Counters() engine.TransportCounters
}

// pollWriter is the subset of *engine.StoreWriter used by the orchestrator.
type pollWriter interface {
	Write(engine.PollResult) error
	WriteStatus(engine.StatusSnapshot) error
}

// blockHealthWriter is the subset of *engine.BlockHealthStore used by the orchestrator.
// It accepts primitive key components so the orchestrator never constructs
// engine.BlockHealthKey directly (no internal key-layout leakage).
type blockHealthWriter interface {
	SetBlockHealth(unitID string, blockIdx int, h engine.ReadBlockHealth)
}

// runOrchestrator consumes poll results for one replication unit and coordinates:
//   - per-block health updates (health.go — state mutation)
//   - status snapshot updates (snapshot.go — data transformation)
//   - store writes (pollWriter — IO)
//   - SecondsInError increment via secTicker (scheduling)
//   - write-change policy: WriteStatus is only called when snap actually changed
func runOrchestrator(
	ctx context.Context,
	unitID string,
	counters counterSource,
	writer pollWriter,
	health blockHealthWriter,
	ch <-chan engine.PollResult,
) {
	snap := engine.StatusSnapshot{
		Health: engine.HealthUnknown,
	}

	// Per-block health state (indexed by block index).
	blockHealth := make(map[int]engine.ReadBlockHealth)

	secTicker := time.NewTicker(time.Second)
	defer secTicker.Stop()

	// Initial full assert of status block.
	_ = writer.WriteStatus(snap)

	for {
		select {
		case <-ctx.Done():
			return

		case res := <-ch:
			if err := writer.Write(res); err != nil {
				log.Printf("aegis: write error (unit=%s): %v", unitID, err)
			}

			// Update per-block health state from poll result (state mutation).
			for _, upd := range res.BlockUpdates {
				blockHealth[upd.BlockIdx] = updateBlockHealth(blockHealth[upd.BlockIdx], upd, res.At)
				health.SetBlockHealth(unitID, upd.BlockIdx, blockHealth[upd.BlockIdx])
			}

			// Apply poll result and transport counters to the status snapshot
			// (data transformation).  Write status only when something changed
			// (policy enforcement).
			var c1, c2 bool
			snap, c1 = applyPollResult(snap, res)
			snap, c2 = applyCounters(snap, counters.Counters())
			if c1 || c2 {
				if err := writer.WriteStatus(snap); err != nil {
					log.Printf("aegis: status write error (unit=%s): %v", unitID, err)
				}
			}

		case <-secTicker.C:
			if snap.Health != engine.HealthOK && snap.SecondsInError < 65535 {
				snap.SecondsInError++
				if err := writer.WriteStatus(snap); err != nil {
					log.Printf("aegis: status tick write error (unit=%s): %v", unitID, err)
				}
			}
		}
	}
}
