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
						{FC: 3, Address: 0, Quantity: 10},
					},
					Target: TargetConfig{
						ListenerID: "main",
						UnitID:     1,
					},
					Poll: PollConfig{IntervalMs: 1000},
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
