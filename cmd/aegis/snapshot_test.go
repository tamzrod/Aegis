// cmd/aegis/snapshot_test.go
package main

import (
	"errors"
	"testing"

	"github.com/tamzrod/Aegis/internal/engine"
)

// TestApplyPollResultEmptyTickNoHealthChange verifies that an empty-tick result
// (no block updates, no error) does not modify the health snapshot.
//
// This is the primary guard against spurious health-state resets: if the
// scheduler fires a tick when no read blocks are due, the poller returns a
// result with Err==nil and no BlockUpdates.  applyPollResult must treat this
// as a no-op rather than interpreting silence as "success."
func TestApplyPollResultEmptyTickNoHealthChange(t *testing.T) {
	// Snapshot already in Error state.
	snap := engine.StatusSnapshot{
		Health:         engine.HealthError,
		LastErrorCode:  2,
		SecondsInError: 30,
	}

	// Empty-tick result: Err==nil, no blocks, no updates.
	res := engine.PollResult{
		UnitID: "u1",
	}

	got, changed := applyPollResult(snap, res)

	if changed {
		t.Error("empty tick: applyPollResult must not report a change")
	}
	if got.Health != engine.HealthError {
		t.Errorf("empty tick: Health must remain Error, got %d", got.Health)
	}
	if got.LastErrorCode != 2 {
		t.Errorf("empty tick: LastErrorCode must remain 2, got %d", got.LastErrorCode)
	}
	if got.SecondsInError != 30 {
		t.Errorf("empty tick: SecondsInError must remain 30, got %d", got.SecondsInError)
	}
}

// TestApplyPollResultEmptyTickFromUnknown verifies that an empty tick also
// does not change health when the device is in the initial Unknown state.
func TestApplyPollResultEmptyTickFromUnknown(t *testing.T) {
	snap := engine.StatusSnapshot{Health: engine.HealthUnknown}

	res := engine.PollResult{UnitID: "u1"} // Err==nil, no BlockUpdates

	got, changed := applyPollResult(snap, res)

	if changed {
		t.Error("empty tick from Unknown: must not change state")
	}
	if got.Health != engine.HealthUnknown {
		t.Errorf("empty tick from Unknown: Health must remain Unknown, got %d", got.Health)
	}
}

// TestApplyPollResultSuccessTransitionsToOK verifies that a real successful poll
// (BlockUpdates present, Err==nil) correctly sets health to OK and clears error state.
func TestApplyPollResultSuccessTransitionsToOK(t *testing.T) {
	snap := engine.StatusSnapshot{
		Health:         engine.HealthError,
		LastErrorCode:  5,
		SecondsInError: 10,
	}

	res := engine.PollResult{
		UnitID: "u1",
		// Err is nil and BlockUpdates is non-empty → real successful read.
		BlockUpdates: []engine.BlockUpdate{
			{BlockIdx: 0, Success: true},
		},
	}

	got, changed := applyPollResult(snap, res)

	if !changed {
		t.Error("successful poll: applyPollResult must report a change")
	}
	if got.Health != engine.HealthOK {
		t.Errorf("successful poll: Health must be OK, got %d", got.Health)
	}
	if got.LastErrorCode != 0 {
		t.Errorf("successful poll: LastErrorCode must be cleared, got %d", got.LastErrorCode)
	}
	if got.SecondsInError != 0 {
		t.Errorf("successful poll: SecondsInError must be cleared, got %d", got.SecondsInError)
	}
}

// TestApplyPollResultErrorSetsHealthError verifies that a poll failure correctly
// sets health to Error and records the error code.
func TestApplyPollResultErrorSetsHealthError(t *testing.T) {
	snap := engine.StatusSnapshot{Health: engine.HealthOK}

	res := engine.PollResult{
		UnitID: "u1",
		Err:    errors.New("device timeout"),
		BlockUpdates: []engine.BlockUpdate{
			{BlockIdx: 0, Success: false, Timeout: true},
		},
	}

	got, changed := applyPollResult(snap, res)

	if !changed {
		t.Error("error poll: applyPollResult must report a change")
	}
	if got.Health != engine.HealthError {
		t.Errorf("error poll: Health must be Error, got %d", got.Health)
	}
}

// TestApplyPollResultErrorWithNoBlockUpdates verifies that a connection-attempt
// failure (factory failed, no reads attempted) still sets health to Error.
// This case has Err!=nil but no BlockUpdates — it must NOT be treated as
// an empty tick.
func TestApplyPollResultErrorWithNoBlockUpdates(t *testing.T) {
	snap := engine.StatusSnapshot{Health: engine.HealthUnknown}

	res := engine.PollResult{
		UnitID: "u1",
		Err:    errors.New("connection refused"),
		// No BlockUpdates: factory failed before any read attempt.
	}

	got, changed := applyPollResult(snap, res)

	if !changed {
		t.Error("factory failure: applyPollResult must report a change")
	}
	if got.Health != engine.HealthError {
		t.Errorf("factory failure: Health must be Error, got %d", got.Health)
	}
}
