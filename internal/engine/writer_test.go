// internal/engine/writer_test.go
package engine

import (
	"errors"
	"testing"

	"github.com/tamzrod/Aegis/internal/core"
)

// mockStore implements core.Store for testing.
type mockStore struct {
	memories map[core.MemoryID]*core.Memory
}

func (m *mockStore) Get(id core.MemoryID) (*core.Memory, bool) {
	mem, ok := m.memories[id]
	return mem, ok
}

func (m *mockStore) MustGet(id core.MemoryID) (*core.Memory, error) {
	mem, ok := m.memories[id]
	if !ok {
		return nil, core.ErrUnknownMemoryID
	}
	return mem, nil
}

func newMockStore(t *testing.T) (*mockStore, *core.Memory, core.MemoryID) {
	t.Helper()
	mem, err := core.NewMemory(core.MemoryLayouts{
		HoldingRegs:    &core.AreaLayout{Start: 0, Size: 100},
		InputRegs:      &core.AreaLayout{Start: 0, Size: 100},
		Coils:          &core.AreaLayout{Start: 0, Size: 64},
		DiscreteInputs: &core.AreaLayout{Start: 0, Size: 64},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}

	id := core.MemoryID{Port: 502, UnitID: 1}
	store := &mockStore{
		memories: map[core.MemoryID]*core.Memory{id: mem},
	}
	return store, mem, id
}

// TestWriterWritesHoldingRegs verifies FC3 data reaches the store.
func TestWriterWritesHoldingRegs(t *testing.T) {
	store, mem, id := newMockStore(t)

	plan := WritePlan{
		UnitID: "test",
		Targets: []TargetMemory{
			{MemoryID: id, Offsets: nil},
		},
	}
	w := NewStoreWriter(plan, store)

	res := PollResult{
		UnitID: "test",
		Blocks: []BlockResult{
			{FC: 3, Address: 5, Quantity: 2, Registers: []uint16{0x1234, 0x5678}},
		},
	}

	if err := w.Write(res); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dst := make([]byte, 4)
	if err := mem.ReadRegs(core.AreaHoldingRegs, 5, 2, dst); err != nil {
		t.Fatalf("ReadRegs: %v", err)
	}

	if dst[0] != 0x12 || dst[1] != 0x34 {
		t.Errorf("reg[5] expected 0x1234, got 0x%02X%02X", dst[0], dst[1])
	}
	if dst[2] != 0x56 || dst[3] != 0x78 {
		t.Errorf("reg[6] expected 0x5678, got 0x%02X%02X", dst[2], dst[3])
	}
}

// TestWriterSkipsOnPollError verifies that failed poll results do not write to store.
func TestWriterSkipsOnPollError(t *testing.T) {
	store, mem, id := newMockStore(t)

	plan := WritePlan{
		UnitID: "test",
		Targets: []TargetMemory{
			{MemoryID: id, Offsets: nil},
		},
	}
	w := NewStoreWriter(plan, store)

	// Pre-write a known value
	src := []byte{0xAB, 0xCD}
	if err := mem.WriteRegs(core.AreaHoldingRegs, 0, 1, src); err != nil {
		t.Fatalf("pre-write: %v", err)
	}

	// Write a failed poll result
	res := PollResult{
		UnitID: "test",
		Err:    errors.New("device unreachable"),
		Blocks: []BlockResult{
			{FC: 3, Address: 0, Quantity: 1, Registers: []uint16{0xFFFF}},
		},
	}

	if err := w.Write(res); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// The store should still hold the original value (0xABCD)
	dst := make([]byte, 2)
	if err := mem.ReadRegs(core.AreaHoldingRegs, 0, 1, dst); err != nil {
		t.Fatalf("ReadRegs: %v", err)
	}
	if dst[0] != 0xAB || dst[1] != 0xCD {
		t.Errorf("expected 0xABCD (unchanged), got 0x%02X%02X", dst[0], dst[1])
	}
}

// TestWriterWithOffset verifies that FC offsets are applied correctly.
func TestWriterWithOffset(t *testing.T) {
	store, mem, id := newMockStore(t)

	plan := WritePlan{
		UnitID: "test",
		Targets: []TargetMemory{
			{
				MemoryID: id,
				Offsets:  map[int]uint16{3: 10}, // FC3 offset = 10
			},
		},
	}
	w := NewStoreWriter(plan, store)

	res := PollResult{
		UnitID: "test",
		Blocks: []BlockResult{
			{FC: 3, Address: 0, Quantity: 1, Registers: []uint16{0xBEEF}},
		},
	}

	if err := w.Write(res); err != nil {
		t.Fatalf("Write: %v", err)
	}

	// Should be written at address 0 + 10 = 10
	dst := make([]byte, 2)
	if err := mem.ReadRegs(core.AreaHoldingRegs, 10, 1, dst); err != nil {
		t.Fatalf("ReadRegs: %v", err)
	}
	if dst[0] != 0xBE || dst[1] != 0xEF {
		t.Errorf("expected 0xBEEF at addr=10, got 0x%02X%02X", dst[0], dst[1])
	}
}

// TestWriterWritesCoils verifies FC1 (coil) data reaches the store.
func TestWriterWritesCoils(t *testing.T) {
	store, mem, id := newMockStore(t)

	plan := WritePlan{
		UnitID:  "test",
		Targets: []TargetMemory{{MemoryID: id}},
	}
	w := NewStoreWriter(plan, store)

	res := PollResult{
		UnitID: "test",
		Blocks: []BlockResult{
			{FC: 1, Address: 0, Quantity: 4, Bits: []bool{true, false, true, false}},
		},
	}

	if err := w.Write(res); err != nil {
		t.Fatalf("Write: %v", err)
	}

	dst := make([]byte, 1)
	if err := mem.ReadBits(core.AreaCoils, 0, 4, dst); err != nil {
		t.Fatalf("ReadBits: %v", err)
	}
	// Expect bit pattern 0101 = 0x05
	if dst[0]&0x0F != 0x05 {
		t.Errorf("expected coil pattern 0101 (0x05), got 0x%02X", dst[0]&0x0F)
	}
}

// TestWriterMissingMemory verifies that a missing store target returns an error.
func TestWriterMissingMemory(t *testing.T) {
	store, _, _ := newMockStore(t)

	badID := core.MemoryID{Port: 502, UnitID: 99} // not in store
	plan := WritePlan{
		UnitID:  "test",
		Targets: []TargetMemory{{MemoryID: badID}},
	}
	w := NewStoreWriter(plan, store)

	res := PollResult{
		UnitID: "test",
		Blocks: []BlockResult{
			{FC: 3, Address: 0, Quantity: 1, Registers: []uint16{0x1234}},
		},
	}

	if err := w.Write(res); err == nil {
		t.Error("expected error for missing memory in store, got nil")
	}
}
