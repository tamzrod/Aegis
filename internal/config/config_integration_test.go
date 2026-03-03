// internal/config/config_integration_test.go
package config_test

import (
	"path/filepath"
	"runtime"
	"testing"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/core"
)

// testYAMLPath returns the absolute path to test/test.yaml regardless of the
// working directory at test runtime.
func testYAMLPath(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	// file = .../internal/config/config_integration_test.go
	// test.yaml = .../test/test.yaml
	root := filepath.Join(filepath.Dir(file), "..", "..")
	return filepath.Join(root, "test", "test.yaml")
}

// statusBlockSize is the fixed number of holding registers per device status block.
// This mirrors engine.StatusSlotsPerDevice (30) without importing the engine package.
const statusBlockSize = 30

// TestConfigIntegration loads test/test.yaml, validates it, builds the memory store,
// and asserts the expected structure without starting any server or engine.
func TestConfigIntegration(t *testing.T) {
	path := testYAMLPath(t)

	// --- Load ---
	cfg, err := config.Load(path)
	if err != nil {
		t.Fatalf("Load(%q): unexpected error: %v", path, err)
	}

	// --- Validate ---
	if err := config.Validate(cfg); err != nil {
		t.Fatalf("Validate: unexpected error: %v", err)
	}

	// --- BuildMemStore ---
	store, err := config.BuildMemStore(cfg)
	if err != nil {
		t.Fatalf("BuildMemStore: unexpected error: %v", err)
	}

	// --- Assert: expected listener and memory counts ---
	if got := len(cfg.Server.Listeners); got != 1 {
		t.Errorf("listeners: want 1, got %d", got)
	}
	if got := len(cfg.Server.Listeners[0].Memory); got != 2 {
		t.Errorf("memory definitions: want 2, got %d", got)
	}
	if got := len(cfg.Replicator.Units); got != 1 {
		t.Errorf("replicator units: want 1, got %d", got)
	}
	if got := len(cfg.Replicator.Units[0].Reads); got != 1 {
		t.Errorf("read blocks: want 1, got %d", got)
	}

	// --- Assert: store contains the data MemoryID (port=502, unit_id=1) ---
	dataID := core.MemoryID{Port: 502, UnitID: 1}
	if _, ok := store.Get(dataID); !ok {
		t.Errorf("store missing data memory %+v", dataID)
	}

	// --- Assert: store contains the status MemoryID (port=502, unit_id=255) ---
	statusID := core.MemoryID{Port: 502, UnitID: 255}
	if _, ok := store.Get(statusID); !ok {
		t.Errorf("store missing status memory %+v", statusID)
	}

	// --- Assert: status slot base aligns with the 30-register block size ---
	unit := cfg.Replicator.Units[0]
	if unit.Source.StatusSlot == nil {
		t.Fatal("expected status_slot to be set")
	}
	blockIndex := uint32(*unit.Source.StatusSlot)
	blockStart := blockIndex * statusBlockSize
	blockEnd := blockStart + statusBlockSize

	// The status memory must have enough holding registers to contain the full block.
	statusMemHRCount := uint32(0)
	for _, mem := range cfg.Server.Listeners[0].Memory {
		if mem.UnitID == uint16(*unit.Target.StatusUnitID) {
			statusMemHRCount = uint32(mem.HoldingRegs.Count)
			break
		}
	}
	if statusMemHRCount == 0 {
		t.Fatal("status memory has no holding registers allocated")
	}
	if blockEnd > statusMemHRCount {
		t.Errorf(
			"status slot alignment: block [%d, %d) exceeds status memory holding register count %d",
			blockStart, blockEnd, statusMemHRCount,
		)
	}
}
