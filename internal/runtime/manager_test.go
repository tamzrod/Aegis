// internal/runtime/manager_test.go
package runtime

import (
	"errors"
	"sync"
	"testing"
)

func TestInitialState(t *testing.T) {
	var m RuntimeManager
	s := m.Status()
	if s.Running {
		t.Errorf("initial state: want Running=false, got true")
	}
	if s.Error != "" {
		t.Errorf("initial state: want empty Error, got %q", s.Error)
	}
}

func TestSetRunning(t *testing.T) {
	var m RuntimeManager
	m.SetRunning()
	s := m.Status()
	if !s.Running {
		t.Errorf("after SetRunning: want Running=true, got false")
	}
	if s.Error != "" {
		t.Errorf("after SetRunning: want empty Error, got %q", s.Error)
	}
}

func TestSetError(t *testing.T) {
	var m RuntimeManager
	m.SetError(errors.New("connection refused"))
	s := m.Status()
	if s.Running {
		t.Errorf("after SetError: want Running=false, got true")
	}
	if s.Error != "connection refused" {
		t.Errorf("after SetError: want Error=%q, got %q", "connection refused", s.Error)
	}
}

func TestSetErrorClearsRunning(t *testing.T) {
	var m RuntimeManager
	m.SetRunning()
	m.SetError(errors.New("dial timeout"))
	s := m.Status()
	if s.Running {
		t.Errorf("after SetError following SetRunning: want Running=false, got true")
	}
	if s.Error == "" {
		t.Errorf("after SetError following SetRunning: want non-empty Error")
	}
}

func TestSetRunningClearsError(t *testing.T) {
	var m RuntimeManager
	m.SetError(errors.New("initial error"))
	m.SetRunning()
	s := m.Status()
	if !s.Running {
		t.Errorf("after SetRunning following SetError: want Running=true, got false")
	}
	if s.Error != "" {
		t.Errorf("after SetRunning following SetError: want empty Error, got %q", s.Error)
	}
}

func TestSetErrorNilIsNoOp(t *testing.T) {
	var m RuntimeManager
	m.SetRunning()
	m.SetError(nil)
	s := m.Status()
	// nil error must not overwrite the Running=true state
	if !s.Running {
		t.Errorf("SetError(nil) must be a no-op: want Running=true, got false")
	}
}

func TestStatusReturnsCopy(t *testing.T) {
	var m RuntimeManager
	m.SetRunning()
	s := m.Status()
	// Mutating the copy must not affect the manager's internal state.
	s.Running = false
	s.Error = "tampered"
	s2 := m.Status()
	if !s2.Running {
		t.Errorf("Status must return a copy: external mutation changed internal state")
	}
}

// TestConcurrentAccess runs concurrent SetRunning / SetError / Status calls to
// verify that RuntimeManager is safe for concurrent use (detected by the race detector).
func TestConcurrentAccess(t *testing.T) {
	var m RuntimeManager
	const goroutines = 50

	var wg sync.WaitGroup
	wg.Add(goroutines * 3)

	for i := 0; i < goroutines; i++ {
		go func() { defer wg.Done(); m.SetRunning() }()
		go func() { defer wg.Done(); m.SetError(errors.New("test error")) }()
		go func() { defer wg.Done(); _ = m.Status() }()
	}

	wg.Wait()
}
