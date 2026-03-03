// internal/core/memory_test.go
package core

import (
	"testing"
)

func makeHoldingMemory(t *testing.T, start, count uint16) *Memory {
	t.Helper()
	m, err := NewMemory(MemoryLayouts{
		HoldingRegs: &AreaLayout{Start: start, Size: count},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	return m
}

func makeCoilMemory(t *testing.T, start, count uint16) *Memory {
	t.Helper()
	m, err := NewMemory(MemoryLayouts{
		Coils: &AreaLayout{Start: start, Size: count},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}
	return m
}

// TestWriteReadRegs verifies holding register round-trip.
func TestWriteReadRegs(t *testing.T) {
	m := makeHoldingMemory(t, 0, 10)

	src := []byte{0x00, 0x01, 0x00, 0x02} // two registers: 1, 2
	if err := m.WriteRegs(AreaHoldingRegs, 0, 2, src); err != nil {
		t.Fatalf("WriteRegs: %v", err)
	}

	dst := make([]byte, 4)
	if err := m.ReadRegs(AreaHoldingRegs, 0, 2, dst); err != nil {
		t.Fatalf("ReadRegs: %v", err)
	}

	if dst[1] != 0x01 || dst[3] != 0x02 {
		t.Errorf("unexpected values: %v", dst)
	}
}

// TestRegsOutOfBounds verifies that out-of-bounds writes are rejected.
func TestRegsOutOfBounds(t *testing.T) {
	m := makeHoldingMemory(t, 0, 10)

	src := make([]byte, 4)
	// address 9, count 2 → would write [9, 10] which exceeds [0,10)
	if err := m.WriteRegs(AreaHoldingRegs, 9, 2, src); err == nil {
		t.Error("expected out-of-bounds error, got nil")
	}
}

// TestRegsStartOffset verifies zero-based addressing with non-zero start layout.
func TestRegsStartOffset(t *testing.T) {
	// Memory starts at address 100
	m, err := NewMemory(MemoryLayouts{
		HoldingRegs: &AreaLayout{Start: 100, Size: 10},
	})
	if err != nil {
		t.Fatalf("NewMemory: %v", err)
	}

	src := []byte{0x12, 0x34}
	if err := m.WriteRegs(AreaHoldingRegs, 100, 1, src); err != nil {
		t.Fatalf("WriteRegs at 100: %v", err)
	}

	dst := make([]byte, 2)
	if err := m.ReadRegs(AreaHoldingRegs, 100, 1, dst); err != nil {
		t.Fatalf("ReadRegs at 100: %v", err)
	}

	if dst[0] != 0x12 || dst[1] != 0x34 {
		t.Errorf("unexpected values: %v", dst)
	}

	// address 0 should be out of bounds
	if err := m.WriteRegs(AreaHoldingRegs, 0, 1, src); err == nil {
		t.Error("expected out-of-bounds error for address 0, got nil")
	}
}

// TestWriteReadCoils verifies coil bit round-trip.
func TestWriteReadCoils(t *testing.T) {
	m := makeCoilMemory(t, 0, 16)

	// Write bits: 1010 1010 = 0xAA (LSB-first)
	src := []byte{0xAA}
	if err := m.WriteBits(AreaCoils, 0, 8, src); err != nil {
		t.Fatalf("WriteBits: %v", err)
	}

	dst := make([]byte, 1)
	if err := m.ReadBits(AreaCoils, 0, 8, dst); err != nil {
		t.Fatalf("ReadBits: %v", err)
	}

	if dst[0] != 0xAA {
		t.Errorf("expected 0xAA, got 0x%02X", dst[0])
	}
}

// TestCoilOutOfBounds verifies that out-of-bounds bit writes are rejected.
func TestCoilOutOfBounds(t *testing.T) {
	m := makeCoilMemory(t, 0, 8)

	src := []byte{0x01}
	if err := m.WriteBits(AreaCoils, 8, 1, src); err == nil {
		t.Error("expected out-of-bounds error, got nil")
	}
}

// TestAreaNotDefined verifies that accessing an undefined area returns an error.
func TestAreaNotDefined(t *testing.T) {
	m := makeHoldingMemory(t, 0, 10)

	// Try to read coils (not allocated)
	dst := make([]byte, 1)
	if err := m.ReadBits(AreaCoils, 0, 1, dst); err != ErrAreaNotDefined {
		t.Errorf("expected ErrAreaNotDefined, got %v", err)
	}
}

// TestCountZero verifies that zero count is rejected.
func TestCountZero(t *testing.T) {
	m := makeHoldingMemory(t, 0, 10)
	dst := make([]byte, 2)
	if err := m.ReadRegs(AreaHoldingRegs, 0, 0, dst); err != ErrCountZero {
		t.Errorf("expected ErrCountZero, got %v", err)
	}
}
