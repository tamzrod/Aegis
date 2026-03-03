// internal/core/store_test.go
package core

import (
	"testing"
)

func makeTestMemory(t *testing.T) *Memory {
	t.Helper()
	m, err := NewMemory(MemoryLayouts{
		HoldingRegs: &AreaLayout{Start: 0, Size: 10},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	return m
}

func TestStoreAddAndGet(t *testing.T) {
	s := NewMemStore()
	id := MemoryID{Port: 502, UnitID: 1}
	mem := makeTestMemory(t)

	if err := s.Add(id, mem); err != nil {
		t.Fatalf("Add: %v", err)
	}

	got, ok := s.Get(id)
	if !ok {
		t.Fatal("Get: expected found, got not found")
	}
	if got != mem {
		t.Error("Get: returned different memory instance")
	}
}

func TestStoreMustGetUnknown(t *testing.T) {
	s := NewMemStore()
	id := MemoryID{Port: 502, UnitID: 99}

	_, err := s.MustGet(id)
	if err != ErrUnknownMemoryID {
		t.Errorf("expected ErrUnknownMemoryID, got %v", err)
	}
}

func TestStoreAddNilMemory(t *testing.T) {
	s := NewMemStore()
	id := MemoryID{Port: 502, UnitID: 1}

	if err := s.Add(id, nil); err != ErrNilMemory {
		t.Errorf("expected ErrNilMemory, got %v", err)
	}
}

func TestStoreAddInvalidID(t *testing.T) {
	s := NewMemStore()
	id := MemoryID{Port: 0, UnitID: 1} // Port 0 is invalid

	mem := makeTestMemory(t)
	if err := s.Add(id, mem); err == nil {
		t.Error("expected validation error for port=0, got nil")
	}
}

func TestStoreGetMissing(t *testing.T) {
	s := NewMemStore()
	id := MemoryID{Port: 502, UnitID: 1}

	mem, ok := s.Get(id)
	if ok || mem != nil {
		t.Error("expected not found for empty store")
	}
}

// TestStoreImplementsInterface verifies MemStore satisfies the Store interface.
func TestStoreImplementsInterface(t *testing.T) {
	var _ Store = (*MemStore)(nil)
}
