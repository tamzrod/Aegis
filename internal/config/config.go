// internal/config/config.go
package config

// Authority mode constants.
const (
	// AuthorityModeStandalone allows client writes and always serves memory reads.
	// Health state does not gate reads. Engine may overwrite values on successful poll.
	AuthorityModeStandalone = "standalone"

	// AuthorityModeStrict is the default authority mode.
	// Client writes are rejected (Modbus exception 0x01).
	// Reads are blocked with exception 0x0B when upstream health is not OK.
	AuthorityModeStrict = "strict"

	// AuthorityModeBuffer rejects client writes (0x01) but always serves memory reads.
	// Health state does not block reads.
	AuthorityModeBuffer = "buffer"
)

// Config is the root configuration for Aegis.
// It is loaded once at startup and never modified at runtime.
type Config struct {
	// AuthorityMode controls how the server handles client reads and writes.
	// Valid values: "standalone", "strict", "buffer".
	// Defaults to "strict" if not specified.
	AuthorityMode string `yaml:"authority_mode"`

	// Server declares one or more Modbus TCP listener gates and their memory layouts.
	Server ServerConfig `yaml:"server"`

	// Replicator declares the upstream devices to poll and the in-process targets to write.
	Replicator ReplicatorConfig `yaml:"replicator"`
}

// --------------------
// Server (Modbus TCP adapter)
// --------------------

// ServerConfig declares all Modbus TCP listeners and their associated memory.
type ServerConfig struct {
	Listeners []ListenerConfig `yaml:"listeners"`
}

// ListenerConfig defines a single Modbus TCP listener.
// Memory blocks are scoped to this listener; their MemoryID = (Port, UnitID).
type ListenerConfig struct {
	ID     string           `yaml:"id"`
	Listen string           `yaml:"listen"` // e.g. ":502" or "0.0.0.0:502"
	Memory []MemoryDef      `yaml:"memory"`
}

// MemoryDef declares one memory block within a listener.
type MemoryDef struct {
	UnitID         uint16   `yaml:"unit_id"`
	Coils          AreaDef  `yaml:"coils"`
	DiscreteInputs AreaDef  `yaml:"discrete_inputs"`
	HoldingRegs    AreaDef  `yaml:"holding_registers"`
	InputRegs      AreaDef  `yaml:"input_registers"`

	// Optional state sealing.
	// Presence enables state sealing for this memory block.
	StateSealing *StateSealingDef `yaml:"state_sealing"`
}

// AreaDef declares the address range for one Modbus area.
// A zero Count means the area is not allocated.
type AreaDef struct {
	Start uint16 `yaml:"start"`
	Count uint16 `yaml:"count"`
}

// StateSealingDef configures the state sealing flag for a memory block.
// Semantics: 0 = sealed (deny access), 1 = unsealed (allow access).
type StateSealingDef struct {
	Area    string `yaml:"area"`    // "coil" only
	Address uint16 `yaml:"address"` // zero-based address of the flag coil
}

// --------------------
// Replicator (engine)
// --------------------

// ReplicatorConfig declares all polling units.
type ReplicatorConfig struct {
	Units []UnitConfig `yaml:"units"`
}

// UnitConfig describes a single upstream device poll loop.
type UnitConfig struct {
	ID     string       `yaml:"id"`
	Source SourceConfig `yaml:"source"`
	Reads  []ReadConfig `yaml:"reads"`
	Target TargetConfig `yaml:"target"`
}

// SourceConfig describes the upstream Modbus device to read from.
type SourceConfig struct {
	Endpoint  string `yaml:"endpoint"`   // "host:port"
	UnitID    uint8  `yaml:"unit_id"`
	TimeoutMs int    `yaml:"timeout_ms"`

	// Optional device status block.
	// When set, device status is written into the store at the configured slot.
	StatusSlot *uint16 `yaml:"status_slot"`
	DeviceName string  `yaml:"device_name"`
}

// ReadConfig describes one Modbus read geometry and its independent poll cadence.
type ReadConfig struct {
	FC         uint8  `yaml:"fc"`
	Address    uint16 `yaml:"address"`
	Quantity   uint16 `yaml:"quantity"`
	IntervalMs int    `yaml:"interval_ms"` // how often this read block executes (ms); must be > 0
}

// TargetConfig describes the in-process store target to write into.
// No network endpoint: writes go directly into the in-process store.
type TargetConfig struct {
	// ListenerID references the server listener by its ID.
	// The port is derived from the listener's listen address.
	ListenerID string `yaml:"listener_id"`

	// UnitID is the unit ID of the memory block in the store.
	UnitID uint16 `yaml:"unit_id"`

	// StatusUnitID is the unit ID of the memory block used for device status.
	// Only required when source.status_slot is set.
	StatusUnitID *uint16 `yaml:"status_unit_id"`

	// Offsets are per-FC address deltas applied when writing to the store.
	// Key is the function code (1, 2, 3, 4); missing FC defaults to 0.
	Offsets map[int]uint16 `yaml:"offsets"`
}
