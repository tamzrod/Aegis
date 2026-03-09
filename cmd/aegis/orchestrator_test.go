// cmd/aegis/orchestrator_test.go
// Regression tests for the orchestrator's empty-tick invariants.
//
// Invariants under test:
//   - Empty tick (BlockUpdates == nil, Err == nil) must NOT record latency.
//   - Real tick with BlockUpdates must record latency exactly once.
//   - Empty tick must NOT call WriteStatus with changed health state.
package main

import (
	"context"
	"testing"
	"time"

	"github.com/tamzrod/Aegis/internal/engine"
)

// ---- test doubles --------------------------------------------------------

type fakeCounterSource struct{ c engine.TransportCounters }

func (f *fakeCounterSource) Counters() engine.TransportCounters { return f.c }

type fakeWriter struct {
	writeCount       int
	writeStatusCalls []engine.StatusSnapshot
}

func (w *fakeWriter) Write(_ engine.PollResult) error { w.writeCount++; return nil }
func (w *fakeWriter) WriteStatus(s engine.StatusSnapshot) error {
	w.writeStatusCalls = append(w.writeStatusCalls, s)
	return nil
}

type fakeHealthWriter struct{ calls int }

func (h *fakeHealthWriter) SetBlockHealth(_ string, _ int, _ engine.ReadBlockHealth) {
	h.calls++
}

// ---- helper --------------------------------------------------------------

// drainOrchestrator starts runOrchestrator in a goroutine, sends the given
// results, waits briefly for processing, and cancels.  It returns the writer
// and tracker so callers can inspect the side-effects.
func drainOrchestrator(
	t *testing.T,
	results []engine.PollResult,
) (*fakeWriter, *fakeHealthWriter, *PollLatencyTracker) {
	t.Helper()

	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan engine.PollResult, len(results)+1)
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

	// Allow the goroutine to drain the buffered channel items.
	time.Sleep(20 * time.Millisecond)
	cancel()
	<-done

	return writer, health, tracker
}

// ---- tests ---------------------------------------------------------------

// TestOrchestratorEmptyTickDoesNotRecordLatency verifies that an empty-tick
// PollResult (Err==nil, no BlockUpdates) is not forwarded to the latency tracker.
//
// Invariant: Empty tick must NOT record latency.
func TestOrchestratorEmptyTickDoesNotRecordLatency(t *testing.T) {
	// Empty-tick result: At is set but no BlockUpdates and no error.
	emptyTick := engine.PollResult{
		UnitID: "u1",
		At:     time.Now(),
	}

	_, _, tracker := drainOrchestrator(t, []engine.PollResult{emptyTick})

	last, avg, max := tracker.Get("u1")
	if last != 0 || avg != 0 || max != 0 {
		t.Errorf("empty tick: latency tracker must not be updated; got last=%d avg=%d max=%d",
			last, avg, max)
	}
}

// TestOrchestratorRealTickRecordsLatency verifies that a non-empty tick (with
// BlockUpdates present) does update the latency tracker.
//
// Invariant: Only real Modbus I/O may affect poll latency.
func TestOrchestratorRealTickRecordsLatency(t *testing.T) {
	realTick := engine.PollResult{
		UnitID: "u1",
		At:     time.Now().Add(-5 * time.Millisecond), // 5 ms ago
		BlockUpdates: []engine.BlockUpdate{
			{BlockIdx: 0, Success: true},
		},
	}

	_, _, tracker := drainOrchestrator(t, []engine.PollResult{realTick})

	_, avg, _ := tracker.Get("u1")
	if avg == 0 {
		// avg==0 only if no sample was ever recorded.
		t.Error("real tick: latency tracker must have received at least one sample")
	}
}

// TestOrchestratorEmptyTickDoesNotMutateBlockHealth verifies that an empty tick
// (no BlockUpdates) does not trigger any call to SetBlockHealth.
//
// Invariant: Empty tick must NOT mark health OK or health failed.
func TestOrchestratorEmptyTickDoesNotMutateBlockHealth(t *testing.T) {
	emptyTick := engine.PollResult{
		UnitID: "u1",
		At:     time.Now(),
	}

	_, health, _ := drainOrchestrator(t, []engine.PollResult{emptyTick})

	if health.calls != 0 {
		t.Errorf("empty tick: SetBlockHealth must not be called; got %d call(s)", health.calls)
	}
}

// TestOrchestratorEmptyTickDoesNotChangeHealthSnapshot verifies that an empty
// tick does not cause WriteStatus to be called with a different health value
// than the initial state.
//
// The initial WriteStatus call happens before the loop (always one call).
// Only changes caused by real I/O may trigger additional WriteStatus calls.
//
// Invariant: Empty tick must NOT write status updates implying fresh communication.
func TestOrchestratorEmptyTickDoesNotChangeHealthSnapshot(t *testing.T) {
	emptyTick := engine.PollResult{
		UnitID: "u1",
		At:     time.Now(),
	}

	writer, _, _ := drainOrchestrator(t, []engine.PollResult{emptyTick})

	// The orchestrator always calls WriteStatus once at startup (initial assert).
	// An empty tick must NOT trigger a second call.
	if len(writer.writeStatusCalls) > 1 {
		t.Errorf("empty tick: WriteStatus must not be called more than once (initial); got %d call(s)",
			len(writer.writeStatusCalls))
	}
}

// TestOrchestratorRealFailureTickChangesHealthSnapshot verifies that a real
// failed tick (Err != nil, BlockUpdates present) does trigger a WriteStatus call
// beyond the initial assert.
//
// This is the complementary positive-path test to confirm the guard works both
// ways: empty ticks are suppressed, real ticks are not.
func TestOrchestratorRealFailureTickChangesHealthSnapshot(t *testing.T) {
	failedTick := engine.PollResult{
		UnitID: "u1",
		At:     time.Now(),
		Err:    errFake,
		BlockUpdates: []engine.BlockUpdate{
			{BlockIdx: 0, Success: false, Timeout: true},
		},
	}

	writer, _, _ := drainOrchestrator(t, []engine.PollResult{failedTick})

	// We expect the initial WriteStatus plus at least one more from the failure.
	if len(writer.writeStatusCalls) < 2 {
		t.Errorf("real failure tick: WriteStatus must be called at least twice (initial + update); got %d call(s)",
			len(writer.writeStatusCalls))
	}

	// The final snapshot must reflect the error.
	last := writer.writeStatusCalls[len(writer.writeStatusCalls)-1]
	if last.Health != engine.HealthError {
		t.Errorf("real failure tick: final snapshot Health must be Error, got %d", last.Health)
	}
}

// errFake is a sentinel error used by orchestrator tests.
var errFake = errFakeType("simulated modbus failure")

type errFakeType string

func (e errFakeType) Error() string { return string(e) }
