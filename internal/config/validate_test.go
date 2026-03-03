// internal/config/validate_test.go
package config

import (
	"testing"
)

func validBaseConfig() *Config {
	return &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID: "plc1",
					Source: SourceConfig{
						Endpoint:  "192.168.1.1:502",
						TimeoutMs: 1000,
					},
					Reads: []ReadConfig{
						{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000},
					},
					Target: TargetConfig{
						Port:   502,
						UnitID: 1,
						Mode:   TargetModeB,
					},
				},
			},
		},
	}
}

func TestValidateTargetModeB(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.Mode = TargetModeB
	if err := Validate(cfg); err != nil {
		t.Errorf("expected mode B to be valid, got: %v", err)
	}
}

func TestValidateTargetModeA(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.Mode = TargetModeA
	if err := Validate(cfg); err != nil {
		t.Errorf("expected mode A to be valid, got: %v", err)
	}
}

func TestValidateTargetModeC(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.Mode = TargetModeC
	if err := Validate(cfg); err != nil {
		t.Errorf("expected mode C to be valid, got: %v", err)
	}
}

func TestValidateTargetModeInvalid(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.Mode = "bogus"
	if err := Validate(cfg); err == nil {
		t.Error("expected error for invalid target mode value")
	}
}

func TestValidateTargetModeEmpty(t *testing.T) {
	// Empty mode should fail — Load() sets the default, Validate() rejects empty.
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.Mode = ""
	if err := Validate(cfg); err == nil {
		t.Error("expected error for empty target mode (default is applied by Load, not Validate)")
	}
}

func TestValidateValidConfig(t *testing.T) {
	cfg := validBaseConfig()
	if err := Validate(cfg); err != nil {
		t.Errorf("expected valid config, got error: %v", err)
	}
}

func TestValidateNilConfig(t *testing.T) {
	if err := Validate(nil); err == nil {
		t.Error("expected error for nil config")
	}
}

func TestValidateMissingReplicatorUnits(t *testing.T) {
	cfg := &Config{}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for missing replicator units")
	}
}

func TestValidateDuplicateReplicatorID(t *testing.T) {
	cfg := &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "dup",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 1, Mode: TargetModeB},
				},
				{
					ID:     "dup",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 50, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 2, Mode: TargetModeB},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for duplicate replicator unit IDs")
	}
}

func TestValidateTargetPortZero(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.Port = 0
	if err := Validate(cfg); err == nil {
		t.Error("expected error for target.port == 0")
	}
}

func TestValidateReplicatorMissingReads(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Reads = nil
	if err := Validate(cfg); err == nil {
		t.Error("expected error for missing reads")
	}
}

func TestValidateReplicatorInvalidFC(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Reads = []ReadConfig{{FC: 5, Address: 0, Quantity: 1}}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for FC=5 (only 1-4 allowed)")
	}
}

func TestValidateStatusSlotRequiresStatusUnitID(t *testing.T) {
	cfg := validBaseConfig()
	slot := uint16(0)
	cfg.Replicator.Units[0].Target.StatusSlot = &slot
	// No StatusUnitID set → should fail
	if err := Validate(cfg); err == nil {
		t.Error("expected error when status_slot is set but status_unit_id is missing")
	}
}

func TestValidateReadIntervalMsMissing(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Reads = []ReadConfig{
		{FC: 3, Address: 0, Quantity: 10, IntervalMs: 0}, // zero → invalid
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for reads[0].interval_ms == 0")
	}
}

func TestValidateReadIntervalMsNegative(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Reads = []ReadConfig{
		{FC: 3, Address: 0, Quantity: 10, IntervalMs: -1},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for reads[0].interval_ms < 0")
	}
}

func TestValidateReplicatorWriteConflict(t *testing.T) {
	// Two units targeting the same (port, unit_id) — now rejected as a duplicate surface.
	slot0 := uint16(0)
	slot1 := uint16(1)
	statusUID := uint16(255)
	cfg := &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000, DeviceName: "PLC1"},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 1, StatusUnitID: &statusUID, StatusSlot: &slot0, Mode: TargetModeB},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000, DeviceName: "PLC2"},
					Reads:  []ReadConfig{{FC: 3, Address: 5, Quantity: 10, IntervalMs: 1000}}, // overlaps [0,10) at [5,15)
					Target: TargetConfig{Port: 502, UnitID: 1, StatusUnitID: &statusUID, StatusSlot: &slot1, Mode: TargetModeB},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for two units targeting the same (port, unit_id) surface")
	}
}

func TestDuplicateSurfaceRejected(t *testing.T) {
	// Two units with non-overlapping reads targeting the same (port, unit_id).
	// The old write-conflict check would not have caught this; the surface uniqueness
	// rule must reject it unconditionally.
	cfg := &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "dev1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 1, Mode: TargetModeB},
				},
				{
					ID:     "dev2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 100, Quantity: 10, IntervalMs: 1000}}, // non-overlapping
					Target: TargetConfig{Port: 502, UnitID: 1, Mode: TargetModeB},              // same surface
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error: duplicate target surface (port=502, unit_id=1) assigned to multiple devices")
	}
}

func TestValidateReplicatorWriteConflictDifferentUnitIDs(t *testing.T) {
	// Two units target different unit_ids — no conflict, even with overlapping read addresses.
	cfg := &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 1, Mode: TargetModeB},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 2, Mode: TargetModeB}, // different unit_id
				},
			},
		},
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("expected no conflict for different target unit_ids, got: %v", err)
	}
}

func TestValidateStatusSlotDuplicate(t *testing.T) {
	slot := uint16(0)
	statusUID := uint16(255)
	cfg := &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000, DeviceName: "PLC1"},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 1, StatusUnitID: &statusUID, StatusSlot: &slot, Mode: TargetModeB},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000, DeviceName: "PLC2"},
					Reads:  []ReadConfig{{FC: 3, Address: 50, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 2, StatusUnitID: &statusUID, StatusSlot: &slot, Mode: TargetModeB}, // same slot
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for duplicate status_slot on same (port, status_unit_id)")
	}
}

func TestValidateStatusUnitIDConflictsWithDataUnitID(t *testing.T) {
	// status_unit_id == unit_id of another unit on the same port → conflict
	slot := uint16(0)
	statusUID := uint16(2) // conflicts with plc2's unit_id
	cfg := &Config{
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000, DeviceName: "PLC1"},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 1, StatusUnitID: &statusUID, StatusSlot: &slot, Mode: TargetModeB},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{Port: 502, UnitID: 2, Mode: TargetModeB}, // unit_id=2 == plc1's status_unit_id
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error when status_unit_id conflicts with a data unit_id on same port")
	}
}
