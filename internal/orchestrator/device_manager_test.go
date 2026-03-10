// internal/orchestrator/device_manager_test.go
// Regression tests for the orchestrator's empty-tick invariants and snapshot logic.
package orchestrator

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/tamzrod/Aegis/internal/memory"
	"github.com/tamzrod/Aegis/internal/puller"
)

// ---- test doubles --------------------------------------------------------

type fakeCounterSource struct{ c puller.TransportCounters }

func (f *fakeCounterSource) Counters() puller.TransportCounters { return f.c }

type fakeWriter struct {
	writeCount       int
	writeStatusCalls []memory.StatusSnapshot
}

func (w *fakeWriter) Write(_ memory.PollResult) error { w.writeCount++; return nil }
func (w *fakeWriter) WriteStatus(s memory.StatusSnapshot) error {
	w.writeStatusCalls = append(w.writeStatusCalls, s)
	return nil
}

type fakeHealthWriter struct{ calls int }

func (h *fakeHealthWriter) SetBlockHealth(_ string, _ int, _ memory.ReadBlockHealth) {
	h.calls++
}

// ---- helper --------------------------------------------------------------

func drainOrchestrator(
	t *testing.T,
	results []memory.PollResult,
) (*fakeWriter, *fakeHealthWriter, *PollLatencyTracker) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan memory.PollResult, len(results)+1)
	for _, r := range results {
		ch <- r
	}

	writer := &fakeWriter{}
	health := &fakeHealthWriter{}
	tracker := NewPollLatencyTracker()
	counters := &fakeCounterSource{}

	done := make(chan struct{})
	go func() {
		defer close(done)
		runOrchestrator(ctx, "u1", counters, writer, health, ch, tracker)
	}()

	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	return writer, health, tracker
}

// ---- orchestrator tests ---------------------------------------------------------------

func TestOrchestratorEmptyTickDoesNotRecordLatency(t *testing.T) {
	emptyTick := memory.PollResult{
		UnitID: "u1",
		At:     time.Now(),
	}

	_, _, tracker := drainOrchestrator(t, []memory.PollResult{emptyTick})

	last, avg, max := tracker.Get("u1")
	if last != 0 || avg != 0 || max != 0 {
		t.Errorf("empty tick: latency tracker must not be updated; got last=%d avg=%d max=%d",
			last, avg, max)
	}
}

func TestOrchestratorRealTickRecordsLatency(t *testing.T) {
	realTick := memory.PollResult{
		UnitID: "u1",
		At:     time.Now().Add(-5 * time.Millisecond),
		BlockUpdates: []memory.BlockUpdate{
			{BlockIdx: 0, Success: true},
		},
	}

	_, _, tracker := drainOrchestrator(t, []memory.PollResult{realTick})

	_, avg, _ := tracker.Get("u1")
	if avg == 0 {
		t.Error("real tick: latency tracker must have received at least one sample")
	}
}

func TestOrchestratorEmptyTickDoesNotMutateBlockHealth(t *testing.T) {
	emptyTick := memory.PollResult{
		UnitID: "u1",
		At:     time.Now(),
	}

	_, health, _ := drainOrchestrator(t, []memory.PollResult{emptyTick})

	if health.calls != 0 {
		t.Errorf("empty tick: SetBlockHealth must not be called; got %d call(s)", health.calls)
	}
}

func TestOrchestratorEmptyTickDoesNotChangeHealthSnapshot(t *testing.T) {
	emptyTick := memory.PollResult{
		UnitID: "u1",
		At:     time.Now(),
	}

	writer, _, _ := drainOrchestrator(t, []memory.PollResult{emptyTick})

	if len(writer.writeStatusCalls) > 1 {
		t.Errorf("empty tick: WriteStatus must not be called more than once (initial); got %d call(s)",
			len(writer.writeStatusCalls))
	}
}

func TestOrchestratorRealFailureTickChangesHealthSnapshot(t *testing.T) {
	failedTick := memory.PollResult{
		UnitID: "u1",
		At:     time.Now(),
		Err:    errFake,
		BlockUpdates: []memory.BlockUpdate{
			{BlockIdx: 0, Success: false, Timeout: true},
		},
	}

	writer, _, _ := drainOrchestrator(t, []memory.PollResult{failedTick})

	if len(writer.writeStatusCalls) < 2 {
		t.Errorf("real failure tick: WriteStatus must be called at least twice (initial + update); got %d call(s)",
			len(writer.writeStatusCalls))
	}

	last := writer.writeStatusCalls[len(writer.writeStatusCalls)-1]
	if last.Health != memory.HealthError {
		t.Errorf("real failure tick: final snapshot Health must be Error, got %d", last.Health)
	}
}

var errFake = errFakeType("simulated modbus failure")

type errFakeType string

func (e errFakeType) Error() string { return string(e) }

// ---- snapshot tests ---------------------------------------------------------------

func TestApplyPollResultEmptyTickNoHealthChange(t *testing.T) {
	snap := memory.StatusSnapshot{
		Health:         memory.HealthError,
		LastErrorCode:  2,
		SecondsInError: 30,
	}

	res := memory.PollResult{UnitID: "u1"}

	got, changed := applyPollResult(snap, res)

	if changed {
		t.Error("empty tick: applyPollResult must not report a change")
	}
	if got.Health != memory.HealthError {
		t.Errorf("empty tick: Health must remain Error, got %d", got.Health)
	}
	if got.LastErrorCode != 2 {
		t.Errorf("empty tick: LastErrorCode must remain 2, got %d", got.LastErrorCode)
	}
	if got.SecondsInError != 30 {
		t.Errorf("empty tick: SecondsInError must remain 30, got %d", got.SecondsInError)
	}
}

func TestApplyPollResultEmptyTickFromUnknown(t *testing.T) {
	snap := memory.StatusSnapshot{Health: memory.HealthUnknown}

	res := memory.PollResult{UnitID: "u1"}

	got, changed := applyPollResult(snap, res)

	if changed {
		t.Error("empty tick from Unknown: must not change state")
	}
	if got.Health != memory.HealthUnknown {
		t.Errorf("empty tick from Unknown: Health must remain Unknown, got %d", got.Health)
	}
}

func TestApplyPollResultSuccessTransitionsToOK(t *testing.T) {
	snap := memory.StatusSnapshot{
		Health:         memory.HealthError,
		LastErrorCode:  5,
		SecondsInError: 10,
	}

	res := memory.PollResult{
		UnitID: "u1",
		BlockUpdates: []memory.BlockUpdate{
			{BlockIdx: 0, Success: true},
		},
	}

	got, changed := applyPollResult(snap, res)

	if !changed {
		t.Error("successful poll: applyPollResult must report a change")
	}
	if got.Health != memory.HealthOK {
		t.Errorf("successful poll: Health must be OK, got %d", got.Health)
	}
	if got.LastErrorCode != 0 {
		t.Errorf("successful poll: LastErrorCode must be cleared, got %d", got.LastErrorCode)
	}
	if got.SecondsInError != 0 {
		t.Errorf("successful poll: SecondsInError must be cleared, got %d", got.SecondsInError)
	}
}

func TestApplyPollResultErrorSetsHealthError(t *testing.T) {
	snap := memory.StatusSnapshot{Health: memory.HealthOK}

	res := memory.PollResult{
		UnitID: "u1",
		Err:    errors.New("device timeout"),
		BlockUpdates: []memory.BlockUpdate{
			{BlockIdx: 0, Success: false, Timeout: true},
		},
	}

	got, changed := applyPollResult(snap, res)

	if !changed {
		t.Error("error poll: applyPollResult must report a change")
	}
	if got.Health != memory.HealthError {
		t.Errorf("error poll: Health must be Error, got %d", got.Health)
	}
}

func TestApplyPollResultErrorWithNoBlockUpdates(t *testing.T) {
	snap := memory.StatusSnapshot{Health: memory.HealthUnknown}

	res := memory.PollResult{
		UnitID: "u1",
		Err:    errors.New("connection refused"),
	}

	got, changed := applyPollResult(snap, res)

	if !changed {
		t.Error("factory failure: applyPollResult must report a change")
	}
	if got.Health != memory.HealthError {
		t.Errorf("factory failure: Health must be Error, got %d", got.Health)
	}
}
