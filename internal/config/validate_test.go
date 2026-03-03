// internal/config/validate_test.go
package config

import (
	"testing"
)

func validBaseConfig() *Config {
	return &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{
							UnitID:      1,
							HoldingRegs: AreaDef{Start: 0, Count: 100},
						},
					},
				},
			},
		},
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
						ListenerID: "main",
						UnitID:     1,
					},
				},
			},
		},
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

func TestValidateMissingListeners(t *testing.T) {
	cfg := &Config{}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for missing listeners")
	}
}

func TestValidateDuplicateListenerID(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{ID: "dup", Listen: ":502", Memory: []MemoryDef{{UnitID: 1, HoldingRegs: AreaDef{Count: 10}}}},
				{ID: "dup", Listen: ":503", Memory: []MemoryDef{{UnitID: 1, HoldingRegs: AreaDef{Count: 10}}}},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for duplicate listener IDs")
	}
}

func TestValidateDuplicateMemoryIdentity(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{UnitID: 1, HoldingRegs: AreaDef{Count: 10}},
						{UnitID: 1, HoldingRegs: AreaDef{Count: 10}}, // duplicate (port=502, unit=1)
					},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for duplicate (port, unit_id)")
	}
}

func TestValidateAreaOverflow(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{
							UnitID:      1,
							HoldingRegs: AreaDef{Start: 65535, Count: 2}, // overflow
						},
					},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for address space overflow")
	}
}

func TestValidateReplicatorUnknownListenerID(t *testing.T) {
	cfg := validBaseConfig()
	cfg.Replicator.Units[0].Target.ListenerID = "nonexistent"
	if err := Validate(cfg); err == nil {
		t.Error("expected error for unknown listener_id")
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

func TestValidateStateSealingValid(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{
							UnitID: 1,
							Coils:  AreaDef{Start: 0, Count: 16},
							StateSealing: &StateSealingDef{
								Area:    "coil",
								Address: 0,
							},
						},
					},
				},
			},
		},
	}
	if err := Validate(cfg); err != nil {
		t.Errorf("expected valid state sealing config, got: %v", err)
	}
}

func TestValidateStateSealingOutOfBounds(t *testing.T) {
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{
							UnitID: 1,
							Coils:  AreaDef{Start: 0, Count: 8},
							StateSealing: &StateSealingDef{
								Area:    "coil",
								Address: 8, // out of bounds [0,8)
							},
						},
					},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for state sealing address out of bounds")
	}
}

func TestValidateStatusSlotRequiresStatusUnitID(t *testing.T) {
	cfg := validBaseConfig()
	slot := uint16(0)
	cfg.Replicator.Units[0].Source.StatusSlot = &slot
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
	// Two units targeting the same (listener, unit_id) with overlapping FC3 reads.
	slot0 := uint16(0)
	slot1 := uint16(1)
	statusUID := uint16(255)
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{UnitID: 1, HoldingRegs: AreaDef{Start: 0, Count: 100}},
						{UnitID: 255, HoldingRegs: AreaDef{Start: 0, Count: 60}},
					},
				},
			},
		},
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000, StatusSlot: &slot0, DeviceName: "PLC1"},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{ListenerID: "main", UnitID: 1, StatusUnitID: &statusUID},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000, StatusSlot: &slot1, DeviceName: "PLC2"},
					Reads:  []ReadConfig{{FC: 3, Address: 5, Quantity: 10, IntervalMs: 1000}}, // overlaps [0,10) at [5,15)
					Target: TargetConfig{ListenerID: "main", UnitID: 1, StatusUnitID: &statusUID},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected write conflict error for overlapping FC3 reads to same target")
	}
}

func TestValidateReplicatorWriteConflictDifferentUnitIDs(t *testing.T) {
	// Two units target different unit_ids — no conflict, even with overlapping read addresses.
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{UnitID: 1, HoldingRegs: AreaDef{Start: 0, Count: 100}},
						{UnitID: 2, HoldingRegs: AreaDef{Start: 0, Count: 100}},
					},
				},
			},
		},
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{ListenerID: "main", UnitID: 1},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{ListenerID: "main", UnitID: 2}, // different unit_id
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
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{UnitID: 1, HoldingRegs: AreaDef{Start: 0, Count: 100}},
						{UnitID: 255, HoldingRegs: AreaDef{Start: 0, Count: 60}},
					},
				},
			},
		},
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000, StatusSlot: &slot, DeviceName: "PLC1"},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{ListenerID: "main", UnitID: 1, StatusUnitID: &statusUID},
				},
				{
					ID:     "plc2",
					Source: SourceConfig{Endpoint: "192.168.1.2:502", TimeoutMs: 1000, StatusSlot: &slot, DeviceName: "PLC2"}, // same slot
					Reads:  []ReadConfig{{FC: 3, Address: 50, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{ListenerID: "main", UnitID: 2, StatusUnitID: &statusUID},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error for duplicate status_slot")
	}
}

func TestValidateStatusSlotExceedsCapacity(t *testing.T) {
	// slot 1 requires (1+1)*30 = 60 registers, but only 30 are allocated.
	slot := uint16(1)
	statusUID := uint16(255)
	cfg := &Config{
		Server: ServerConfig{
			Listeners: []ListenerConfig{
				{
					ID:     "main",
					Listen: ":502",
					Memory: []MemoryDef{
						{UnitID: 1, HoldingRegs: AreaDef{Start: 0, Count: 100}},
						{UnitID: 255, HoldingRegs: AreaDef{Start: 0, Count: 30}}, // only 30 regs
					},
				},
			},
		},
		Replicator: ReplicatorConfig{
			Units: []UnitConfig{
				{
					ID:     "plc1",
					Source: SourceConfig{Endpoint: "192.168.1.1:502", TimeoutMs: 1000, StatusSlot: &slot, DeviceName: "PLC1"},
					Reads:  []ReadConfig{{FC: 3, Address: 0, Quantity: 10, IntervalMs: 1000}},
					Target: TargetConfig{ListenerID: "main", UnitID: 1, StatusUnitID: &statusUID},
				},
			},
		},
	}
	if err := Validate(cfg); err == nil {
		t.Error("expected error when status_slot exceeds status memory capacity")
	}
}
