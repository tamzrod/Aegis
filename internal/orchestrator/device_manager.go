// internal/orchestrator/device_manager.go
// Responsibility: poll-result consumption loop, health state mutation,
// status snapshot construction, and related helper functions.
package orchestrator

import (
	"context"
	"log"
	"time"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/memory"
	"github.com/tamzrod/Aegis/internal/puller"
)

// counterSource is the subset of *puller.Poller used by the orchestrator.
type counterSource interface {
	Counters() puller.TransportCounters
}

// pollWriter is the subset of *memory.StoreWriter used by the orchestrator.
type pollWriter interface {
	Write(memory.PollResult) error
	WriteStatus(memory.StatusSnapshot) error
}

// blockHealthWriter is the subset of *memory.BlockHealthStore used by the orchestrator.
type blockHealthWriter interface {
	SetBlockHealth(unitID string, blockIdx int, h memory.ReadBlockHealth)
}

// runOrchestrator consumes poll results for one replication unit and coordinates:
//   - per-block health updates
//   - status snapshot updates
//   - store writes
//   - SecondsInError increment via secTicker
//   - write-change policy: WriteStatus is only called when snap actually changed
//   - poll latency recording (passive observability only)
func runOrchestrator(
	ctx context.Context,
	unitID string,
	counters counterSource,
	writer pollWriter,
	health blockHealthWriter,
	ch <-chan memory.PollResult,
	tracker *PollLatencyTracker,
) {
	snap := memory.StatusSnapshot{
		Health: memory.HealthUnknown,
	}

	blockHealth := make(map[int]memory.ReadBlockHealth)

	secTicker := time.NewTicker(time.Second)
	defer secTicker.Stop()

	_ = writer.WriteStatus(snap)

	for {
		select {
		case <-ctx.Done():
			return

		case res := <-ch:
			if tracker != nil && !res.At.IsZero() && (res.Err != nil || len(res.BlockUpdates) > 0) {
				ms := uint32(time.Since(res.At).Milliseconds())
				tracker.Record(unitID, ms)
			}

			if err := writer.Write(res); err != nil {
				log.Printf("aegis: write error (unit=%s): %v", unitID, err)
			}

			for _, upd := range res.BlockUpdates {
				blockHealth[upd.BlockIdx] = updateBlockHealth(blockHealth[upd.BlockIdx], upd, res.At)
				health.SetBlockHealth(unitID, upd.BlockIdx, blockHealth[upd.BlockIdx])
			}

			var c1, c2 bool
			snap, c1 = applyPollResult(snap, res)
			snap, c2 = applyCounters(snap, counters.Counters())
			if c1 || c2 {
				if err := writer.WriteStatus(snap); err != nil {
					log.Printf("aegis: status write error (unit=%s): %v", unitID, err)
				}
			}

		case <-secTicker.C:
			if snap.Health != memory.HealthOK && snap.SecondsInError < 65535 {
				snap.SecondsInError++
				if err := writer.WriteStatus(snap); err != nil {
					log.Printf("aegis: status tick write error (unit=%s): %v", unitID, err)
				}
			}
		}
	}
}

// updateBlockHealth applies a single BlockUpdate to the existing health record h.
func updateBlockHealth(h memory.ReadBlockHealth, upd memory.BlockUpdate, at time.Time) memory.ReadBlockHealth {
	if upd.Success {
		h.Timeout = false
		h.ConsecutiveErrors = 0
		h.LastExceptionCode = 0
		h.LastSuccess = at
	} else {
		h.ConsecutiveErrors++
		h.LastError = at
		if upd.Timeout {
			h.Timeout = true
			h.LastExceptionCode = 0
		} else {
			h.Timeout = false
			h.LastExceptionCode = upd.ExceptionCode
		}
	}
	return h
}

// applyPollResult derives health and error fields from res and writes them into a copy of snap.
// Returns the updated snapshot and whether any field changed.
//
// Empty-tick guard: when no blocks were due (BlockUpdates is empty) and no error occurred,
// the poller performed no Modbus exchange. The health state must not change.
func applyPollResult(snap memory.StatusSnapshot, res memory.PollResult) (memory.StatusSnapshot, bool) {
	if res.Err == nil && len(res.BlockUpdates) == 0 {
		return snap, false
	}

	changed := false

	if res.Err == nil {
		if snap.Health != memory.HealthOK {
			snap.Health = memory.HealthOK
			changed = true
		}
		if snap.LastErrorCode != 0 {
			snap.LastErrorCode = 0
			changed = true
		}
		if snap.SecondsInError != 0 {
			snap.SecondsInError = 0
			changed = true
		}
	} else {
		if snap.Health != memory.HealthError {
			snap.Health = memory.HealthError
			changed = true
		}
		code := memory.ErrorCode(res.Err)
		if snap.LastErrorCode != code {
			snap.LastErrorCode = code
			changed = true
		}
	}

	return snap, changed
}

// applyCounters syncs transport counter fields from c into a copy of snap.
// Returns the updated snapshot and whether any field changed.
func applyCounters(snap memory.StatusSnapshot, c puller.TransportCounters) (memory.StatusSnapshot, bool) {
	changed := false

	if snap.RequestsTotal != c.RequestsTotal {
		snap.RequestsTotal = c.RequestsTotal
		changed = true
	}
	if snap.ResponsesValidTotal != c.ResponsesValidTotal {
		snap.ResponsesValidTotal = c.ResponsesValidTotal
		changed = true
	}
	if snap.TimeoutsTotal != c.TimeoutsTotal {
		snap.TimeoutsTotal = c.TimeoutsTotal
		changed = true
	}
	if snap.TransportErrorsTotal != c.TransportErrorsTotal {
		snap.TransportErrorsTotal = c.TransportErrorsTotal
		changed = true
	}
	if snap.ConsecutiveFailCurr != c.ConsecutiveFailCurr {
		snap.ConsecutiveFailCurr = c.ConsecutiveFailCurr
		changed = true
	}
	if snap.ConsecutiveFailMax != c.ConsecutiveFailMax {
		snap.ConsecutiveFailMax = c.ConsecutiveFailMax
		changed = true
	}

	return snap, changed
}

// deriveDeviceStatus computes a single status string for a replicator unit
// based on the aggregate health of its read blocks.
func deriveDeviceStatus(hs *memory.BlockHealthStore, u config.UnitConfig) string {
	anyFound := false
	anyError := false
	for idx := range u.Reads {
		_, consecutiveErrors, _, found := hs.GetBlockHealth(u.ID, idx)
		if found {
			anyFound = true
			if consecutiveErrors > 0 {
				anyError = true
			}
		}
	}
	if !anyFound {
		return "warning"
	}
	if anyError {
		return "error"
	}
	return "online"
}

// activePollingThreshold is the window within which a successful poll is considered "recent".
const activePollingThreshold = 10 * time.Second

// isDevicePolling returns true if any read block for the unit had a successful poll recently.
func isDevicePolling(hs *memory.BlockHealthStore, u config.UnitConfig, now time.Time) bool {
	threshold := now.Add(-activePollingThreshold)
	for idx := range u.Reads {
		if t, ok := hs.GetLastSuccess(u.ID, idx); ok && t.After(threshold) {
			return true
		}
	}
	return false
}
