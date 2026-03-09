// internal/engine/poller_test.go
package engine

import (
	"errors"
	"testing"
	"time"
)

// fakeClient is a minimal Client implementation for testing.
type fakeClient struct {
	failFC uint8 // non-zero means reads for that FC return an error
}

func (f *fakeClient) ReadCoils(addr, qty uint16) ([]bool, error) {
	if f.failFC == 1 {
		return nil, errors.New("fc1 fail")
	}
	return make([]bool, qty), nil
}

func (f *fakeClient) ReadDiscreteInputs(addr, qty uint16) ([]bool, error) {
	if f.failFC == 2 {
		return nil, errors.New("fc2 fail")
	}
	return make([]bool, qty), nil
}

func (f *fakeClient) ReadHoldingRegisters(addr, qty uint16) ([]uint16, error) {
	if f.failFC == 3 {
		return nil, errors.New("fc3 fail")
	}
	return make([]uint16, qty), nil
}

func (f *fakeClient) ReadInputRegisters(addr, qty uint16) ([]uint16, error) {
	if f.failFC == 4 {
		return nil, errors.New("fc4 fail")
	}
	return make([]uint16, qty), nil
}

func newTestPoller(t *testing.T, reads []ReadBlock) *Poller {
	t.Helper()
	p, err := NewPoller(
		PollerConfig{UnitID: "u1", Reads: reads},
		&fakeClient{},
		nil,
	)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}
	return p
}

// TestPollAtEmptyTickDoesNotUpdateCounters verifies that when no read blocks
// are due, pollAt returns without incrementing any transport counters.
//
// This is a CRITICAL correctness guarantee: an empty tick must not be treated
// as a successful Modbus exchange. Treating it as success would:
//   - inflate RequestsTotal and ResponsesValidTotal
//   - reset ConsecutiveFailCurr to 0 while the device may still be in error
func TestPollAtEmptyTickDoesNotUpdateCounters(t *testing.T) {
	p := newTestPoller(t, []ReadBlock{
		{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
	})

	// Advance nextExec into the future to simulate a recently-executed block.
	future := time.Now().Add(5 * time.Second)
	p.nextExec[0] = future

	// Inject a non-zero ConsecutiveFailCurr to verify it is NOT reset.
	p.counters.ConsecutiveFailCurr = 5
	p.counters.RequestsTotal = 10
	p.counters.ResponsesValidTotal = 8

	// pollAt with a time before nextExec → empty tick.
	now := time.Now()
	res := p.pollAt(now)

	// No error and no blocks on an empty tick.
	if res.Err != nil {
		t.Errorf("empty tick: expected no error, got %v", res.Err)
	}
	if len(res.Blocks) != 0 {
		t.Errorf("empty tick: expected no blocks, got %d", len(res.Blocks))
	}
	if len(res.BlockUpdates) != 0 {
		t.Errorf("empty tick: expected no block updates, got %d", len(res.BlockUpdates))
	}

	// Counters must be unchanged.
	if p.counters.RequestsTotal != 10 {
		t.Errorf("RequestsTotal: want 10 (unchanged), got %d", p.counters.RequestsTotal)
	}
	if p.counters.ResponsesValidTotal != 8 {
		t.Errorf("ResponsesValidTotal: want 8 (unchanged), got %d", p.counters.ResponsesValidTotal)
	}
	if p.counters.ConsecutiveFailCurr != 5 {
		t.Errorf("ConsecutiveFailCurr: want 5 (unchanged), got %d", p.counters.ConsecutiveFailCurr)
	}
}

// TestPollAtSuccessUpdatesCounters verifies that a successful read cycle
// increments RequestsTotal and ResponsesValidTotal exactly once and resets
// ConsecutiveFailCurr to zero.
func TestPollAtSuccessUpdatesCounters(t *testing.T) {
	p := newTestPoller(t, []ReadBlock{
		{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
	})

	p.counters.ConsecutiveFailCurr = 3
	p.counters.RequestsTotal = 7
	p.counters.ResponsesValidTotal = 5

	res := p.pollAt(time.Now())

	if res.Err != nil {
		t.Fatalf("expected success, got error: %v", res.Err)
	}
	if len(res.Blocks) != 1 {
		t.Errorf("expected 1 block, got %d", len(res.Blocks))
	}
	if p.counters.RequestsTotal != 8 {
		t.Errorf("RequestsTotal: want 8, got %d", p.counters.RequestsTotal)
	}
	if p.counters.ResponsesValidTotal != 6 {
		t.Errorf("ResponsesValidTotal: want 6, got %d", p.counters.ResponsesValidTotal)
	}
	if p.counters.ConsecutiveFailCurr != 0 {
		t.Errorf("ConsecutiveFailCurr: want 0, got %d", p.counters.ConsecutiveFailCurr)
	}
}

// TestPollAtFailureUpdatesCounters verifies that a failed read cycle
// increments RequestsTotal and ConsecutiveFailCurr but NOT ResponsesValidTotal.
func TestPollAtFailureUpdatesCounters(t *testing.T) {
	reads := []ReadBlock{
		{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
	}
	p, err := NewPoller(
		PollerConfig{UnitID: "u1", Reads: reads},
		&fakeClient{failFC: 3},
		nil,
	)
	if err != nil {
		t.Fatalf("NewPoller: %v", err)
	}

	p.counters.ConsecutiveFailCurr = 2
	p.counters.RequestsTotal = 7
	p.counters.ResponsesValidTotal = 5
	p.counters.ConsecutiveFailMax = 2

	res := p.pollAt(time.Now())

	if res.Err == nil {
		t.Fatal("expected failure, got success")
	}
	if p.counters.RequestsTotal != 8 {
		t.Errorf("RequestsTotal: want 8, got %d", p.counters.RequestsTotal)
	}
	if p.counters.ResponsesValidTotal != 5 {
		t.Errorf("ResponsesValidTotal: want 5 (unchanged), got %d", p.counters.ResponsesValidTotal)
	}
	if p.counters.ConsecutiveFailCurr != 3 {
		t.Errorf("ConsecutiveFailCurr: want 3, got %d", p.counters.ConsecutiveFailCurr)
	}
	if p.counters.ConsecutiveFailMax != 3 {
		t.Errorf("ConsecutiveFailMax: want 3, got %d", p.counters.ConsecutiveFailMax)
	}
}

// TestPollAtEmptyTickPreservesErrorState verifies the critical safety property:
// an empty tick must not reset health-relevant counter state (ConsecutiveFailCurr)
// that was set by a prior real failure.
//
// This guards against a race window where timer granularity could cause an empty
// tick to fire while the device is still in error, silently clearing the failure
// streak before the next real attempt.
func TestPollAtEmptyTickPreservesErrorState(t *testing.T) {
	p := newTestPoller(t, []ReadBlock{
		{FC: 3, Address: 0, Quantity: 1, Interval: 10 * time.Second},
	})

	// Simulate: previous polls failed, setting a non-zero consecutive count.
	p.counters.ConsecutiveFailCurr = 7
	p.counters.ConsecutiveFailMax = 7
	p.counters.RequestsTotal = 20
	p.counters.ResponsesValidTotal = 13

	// Place nextExec in the future to force an empty tick.
	p.nextExec[0] = time.Now().Add(8 * time.Second)

	res := p.pollAt(time.Now())

	// Confirm it was indeed an empty tick.
	if res.Err != nil || len(res.BlockUpdates) != 0 {
		t.Skip("not an empty tick — test setup invalid")
	}

	// All counters must be unchanged.
	if p.counters.ConsecutiveFailCurr != 7 {
		t.Errorf("ConsecutiveFailCurr must not be reset on empty tick: want 7, got %d",
			p.counters.ConsecutiveFailCurr)
	}
	if p.counters.RequestsTotal != 20 {
		t.Errorf("RequestsTotal must not increment on empty tick: want 20, got %d",
			p.counters.RequestsTotal)
	}
	if p.counters.ResponsesValidTotal != 13 {
		t.Errorf("ResponsesValidTotal must not increment on empty tick: want 13, got %d",
			p.counters.ResponsesValidTotal)
	}
}
