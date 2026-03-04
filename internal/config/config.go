// internal/config/config.go
package config

// Per-target authority mode constants.
// Authority is defined per replicator target, not globally.
const (
	// TargetModeA (Standalone) allows client writes and always serves reads.
	// Health state does not gate reads.
	TargetModeA = "A"

	// TargetModeB (Strict) is the default target authority mode.
	// Client writes are rejected (0x01). Reads are blocked with 0x0B on timeout,
	// or forwarded with the upstream exception code if one was recorded.
	TargetModeB = "B"

	// TargetModeC (Buffered) rejects client writes (0x01) but always serves reads.
	// Health state is exposed in the status block only; reads are never gated.
	TargetModeC = "C"
)

// Config is the root configuration for Aegis.
// It is loaded once at startup and never modified at runtime.
// Listeners and memory are derived from replicator.units[*].target at startup.
type Config struct {
	// Replicator declares the upstream devices to poll and the in-process targets to write.
	Replicator ReplicatorConfig `yaml:"replicator"`

	// WebUI configures the embedded HTTP configuration editor.
	// If absent or enabled=false, no HTTP listener is started.
	WebUI WebUIConfig `yaml:"webui"`
}

// WebUIConfig configures the embedded HTTP configuration editor.
type WebUIConfig struct {
	// Enabled controls whether the WebUI HTTP listener is started.
	// Default: false.
	Enabled bool `yaml:"enabled"`

	// Listen is the TCP address the WebUI listens on.
	// Default: ":8080".
	Listen string `yaml:"listen"`
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

	// Optional device name included in the status block (up to 16 ASCII characters).
	DeviceName string `yaml:"device_name"`
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
// Listeners and memory are derived at runtime from this configuration.
type TargetConfig struct {
	// Port is the TCP port of the Modbus listener for this target.
	// A listener is created for each unique port across all replicator units.
	Port uint16 `yaml:"port"`

	// UnitID is the unit ID of the data memory block in the store.
	UnitID uint16 `yaml:"unit_id"`

	// StatusUnitID is the unit ID of the memory block used for device status.
	// Required when status_slot is set. Must differ from all data unit_ids on the same port.
	StatusUnitID *uint16 `yaml:"status_unit_id"`

	// StatusSlot is the zero-based slot index for this unit's status block.
	// Each slot occupies 30 consecutive holding registers starting at slot*30.
	// Required when status_unit_id is set. Must be unique per (port, status_unit_id).
	StatusSlot *uint16 `yaml:"status_slot"`

	// Offsets are per-FC address deltas applied when writing to the store.
	// Key is the function code (1, 2, 3, 4); missing FC defaults to 0.
	Offsets map[int]uint16 `yaml:"offsets"`

	// Mode controls how the server handles client reads and writes for this target.
	// Valid values: "A" (standalone), "B" (strict, default), "C" (buffered).
	// Authority is per target memory surface; there is no global authority mode.
	Mode string `yaml:"mode"`
}
