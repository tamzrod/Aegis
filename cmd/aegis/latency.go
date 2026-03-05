// cmd/aegis/latency.go — per-unit poll latency statistics tracker.
// Responsibility: record and expose last/avg/max poll latency in milliseconds.
// This tracker is passive-only: it does not influence control flow.
package main

import "sync"

// PollLatencyTracker records per-unit poll latency statistics.
// All methods are safe for concurrent use.
type PollLatencyTracker struct {
	mu      sync.Mutex
	entries map[string]*latencyEntry
}

type latencyEntry struct {
	last  uint32
	max   uint32
	sumMs uint64
	count uint64
}

// NewPollLatencyTracker creates an empty PollLatencyTracker.
func NewPollLatencyTracker() *PollLatencyTracker {
	return &PollLatencyTracker{entries: make(map[string]*latencyEntry)}
}

// Record stores a single poll duration sample for the given unit.
func (t *PollLatencyTracker) Record(unitID string, ms uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[unitID]
	if !ok {
		e = &latencyEntry{}
		t.entries[unitID] = e
	}
	e.last = ms
	if ms > e.max {
		e.max = ms
	}
	e.sumMs += uint64(ms)
	e.count++
}

// Get returns the last, average, and maximum poll latency in milliseconds for
// the given unit. Returns zeros if no samples have been recorded.
func (t *PollLatencyTracker) Get(unitID string) (last, avg, max uint32) {
	t.mu.Lock()
	defer t.mu.Unlock()
	e, ok := t.entries[unitID]
	if !ok || e.count == 0 {
		return 0, 0, 0
	}
	return e.last, uint32(e.sumMs / e.count), e.max
}
