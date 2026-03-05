// cmd/aegis/latency_test.go
package main

import (
	"sync"
	"testing"
)

func TestPollLatencyTrackerZeroBeforeRecord(t *testing.T) {
	tr := NewPollLatencyTracker()
	last, avg, max := tr.Get("unit-1")
	if last != 0 || avg != 0 || max != 0 {
		t.Errorf("Get before Record: want (0,0,0), got (%d,%d,%d)", last, avg, max)
	}
}

func TestPollLatencyTrackerSingleSample(t *testing.T) {
	tr := NewPollLatencyTracker()
	tr.Record("unit-1", 42)
	last, avg, max := tr.Get("unit-1")
	if last != 42 {
		t.Errorf("last: want 42, got %d", last)
	}
	if avg != 42 {
		t.Errorf("avg: want 42, got %d", avg)
	}
	if max != 42 {
		t.Errorf("max: want 42, got %d", max)
	}
}

func TestPollLatencyTrackerAvgAndMax(t *testing.T) {
	tr := NewPollLatencyTracker()
	tr.Record("u", 10)
	tr.Record("u", 20)
	tr.Record("u", 30)
	last, avg, max := tr.Get("u")
	if last != 30 {
		t.Errorf("last: want 30, got %d", last)
	}
	if avg != 20 {
		t.Errorf("avg: want 20, got %d", avg)
	}
	if max != 30 {
		t.Errorf("max: want 30, got %d", max)
	}
}

func TestPollLatencyTrackerMultipleUnits(t *testing.T) {
	tr := NewPollLatencyTracker()
	tr.Record("a", 5)
	tr.Record("b", 100)
	la, _, ma := tr.Get("a")
	lb, _, mb := tr.Get("b")
	if la != 5 || ma != 5 {
		t.Errorf("unit a: want last=5 max=5, got last=%d max=%d", la, ma)
	}
	if lb != 100 || mb != 100 {
		t.Errorf("unit b: want last=100 max=100, got last=%d max=%d", lb, mb)
	}
}

func TestPollLatencyTrackerConcurrent(t *testing.T) {
	tr := NewPollLatencyTracker()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines * 2)
	for i := 0; i < goroutines; i++ {
		go func(v uint32) {
			defer wg.Done()
			tr.Record("unit", v)
		}(uint32(i))
		go func() {
			defer wg.Done()
			tr.Get("unit")
		}()
	}
	wg.Wait()
	// After all goroutines finish, count must equal goroutines.
	tr.mu.Lock()
	e := tr.entries["unit"]
	tr.mu.Unlock()
	if e == nil || e.count != goroutines {
		t.Errorf("after concurrent Record: want count=%d, got %d", goroutines, e.count)
	}
}
