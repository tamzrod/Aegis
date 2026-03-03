// internal/config/validate.go
package config

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// Validate performs structural validation on the configuration.
// It enforces bounds and consistency only.
// It does NOT mutate the configuration.
// If validation fails, the process must exit immediately.
func Validate(cfg *Config) error {
	if cfg == nil {
		return fmt.Errorf("config is nil")
	}

	if err := validateServer(cfg); err != nil {
		return err
	}

	if err := validateReplicator(cfg); err != nil {
		return err
	}

	return nil
}

// --------------------
// Server validation
// --------------------

func validateServer(cfg *Config) error {
	if len(cfg.Server.Listeners) == 0 {
		return fmt.Errorf("server.listeners: at least one listener required")
	}

	seenIDs := make(map[string]struct{})
	seenAddrs := make(map[string]struct{})

	// Track all (port, unit_id) pairs to detect duplicates
	type memKey struct {
		port   uint16
		unitID uint16
	}
	seenMem := make(map[memKey]string)

	for i, l := range cfg.Server.Listeners {
		if l.ID == "" {
			return fmt.Errorf("server.listeners[%d]: id is required", i)
		}
		if _, ok := seenIDs[l.ID]; ok {
			return fmt.Errorf("server.listeners[%d]: duplicate id %q", i, l.ID)
		}
		seenIDs[l.ID] = struct{}{}

		if strings.TrimSpace(l.Listen) == "" {
			return fmt.Errorf("server.listeners[%d] (%s): listen is required", i, l.ID)
		}
		if _, ok := seenAddrs[l.Listen]; ok {
			return fmt.Errorf("server.listeners[%d] (%s): duplicate listen address %q", i, l.ID, l.Listen)
		}
		seenAddrs[l.Listen] = struct{}{}

		port, err := parseListenPort(l.Listen)
		if err != nil {
			return fmt.Errorf("server.listeners[%d] (%s): invalid listen %q: %w", i, l.ID, l.Listen, err)
		}

		if len(l.Memory) == 0 {
			return fmt.Errorf("server.listeners[%d] (%s): at least one memory block required", i, l.ID)
		}

		for j, m := range l.Memory {
			path := fmt.Sprintf("server.listeners[%d] (%s).memory[%d]", i, l.ID, j)

			if m.UnitID == 0 {
				return fmt.Errorf("%s: unit_id must be > 0", path)
			}
			if m.UnitID > 0xFF {
				return fmt.Errorf("%s: unit_id must be <= 255", path)
			}

			if err := validateAreaDef(path, "coils", m.Coils); err != nil {
				return err
			}
			if err := validateAreaDef(path, "discrete_inputs", m.DiscreteInputs); err != nil {
				return err
			}
			if err := validateAreaDef(path, "holding_registers", m.HoldingRegs); err != nil {
				return err
			}
			if err := validateAreaDef(path, "input_registers", m.InputRegs); err != nil {
				return err
			}

			if err := validateStateSealingDef(path, m); err != nil {
				return err
			}

			mk := memKey{port: port, unitID: m.UnitID}
			if prev, ok := seenMem[mk]; ok {
				return fmt.Errorf(
					"memory identity conflict: (port=%d unit_id=%d) defined in %s and %s",
					port, m.UnitID, prev, path,
				)
			}
			seenMem[mk] = path
		}
	}

	return nil
}

func validateAreaDef(path, name string, a AreaDef) error {
	if a.Count == 0 {
		return nil
	}
	end := uint32(a.Start) + uint32(a.Count)
	if end > 0x10000 {
		return fmt.Errorf(
			"%s.%s: start(%d)+count(%d) exceeds 16-bit address space",
			path, name, a.Start, a.Count,
		)
	}
	return nil
}

func validateStateSealingDef(path string, m MemoryDef) error {
	if m.StateSealing == nil {
		return nil
	}

	area := strings.ToLower(strings.TrimSpace(m.StateSealing.Area))
	if area != "coil" {
		return fmt.Errorf("%s.state_sealing.area must be 'coil'", path)
	}

	if m.Coils.Count == 0 {
		return fmt.Errorf("%s.state_sealing requires coils to be allocated", path)
	}

	start := m.Coils.Start
	count := m.Coils.Count
	addr := m.StateSealing.Address
	endExclusive := uint32(start) + uint32(count)

	if uint32(addr) < uint32(start) || uint32(addr) >= endExclusive {
		return fmt.Errorf(
			"%s.state_sealing.address (%d) out of bounds for coils [%d..%d)",
			path, addr, start, uint16(endExclusive),
		)
	}

	return nil
}

// --------------------
// Replicator validation
// --------------------

func validateReplicator(cfg *Config) error {
	// Build a lookup: listenerID → port
	listenerPort := make(map[string]uint16)
	for _, l := range cfg.Server.Listeners {
		port, err := parseListenPort(l.Listen)
		if err != nil {
			// Already validated in validateServer
			continue
		}
		listenerPort[l.ID] = port
	}

	seenIDs := make(map[string]struct{})

	for i, u := range cfg.Replicator.Units {
		if u.ID == "" {
			return fmt.Errorf("replicator.units[%d]: id is required", i)
		}
		if _, ok := seenIDs[u.ID]; ok {
			return fmt.Errorf("replicator.units[%d]: duplicate id %q", i, u.ID)
		}
		seenIDs[u.ID] = struct{}{}

		if err := validateUnitConfig(u, listenerPort); err != nil {
			return fmt.Errorf("replicator.units[%d] (%s): %w", i, u.ID, err)
		}
	}

	return nil
}

func validateUnitConfig(u UnitConfig, listenerPort map[string]uint16) error {
	// Source
	if strings.TrimSpace(u.Source.Endpoint) == "" {
		return fmt.Errorf("source.endpoint is required")
	}
	if u.Source.TimeoutMs <= 0 {
		return fmt.Errorf("source.timeout_ms must be > 0")
	}
	if u.Source.DeviceName != "" {
		for i := 0; i < len(u.Source.DeviceName); i++ {
			if u.Source.DeviceName[i] > 0x7F {
				return fmt.Errorf("source.device_name must contain ASCII characters only")
			}
		}
		if len(u.Source.DeviceName) > 16 {
			return fmt.Errorf("source.device_name must be <= 16 characters")
		}
	}

	// Reads
	if len(u.Reads) == 0 {
		return fmt.Errorf("reads: at least one read block required")
	}
	for j, r := range u.Reads {
		if r.FC < 1 || r.FC > 4 {
			return fmt.Errorf("reads[%d]: fc must be 1, 2, 3, or 4", j)
		}
		if r.Quantity == 0 {
			return fmt.Errorf("reads[%d]: quantity must be > 0", j)
		}
	}

	// Target
	t := u.Target
	if strings.TrimSpace(t.ListenerID) == "" {
		return fmt.Errorf("target.listener_id is required")
	}
	if _, ok := listenerPort[t.ListenerID]; !ok {
		return fmt.Errorf("target.listener_id %q: no matching listener", t.ListenerID)
	}
	if t.UnitID == 0 {
		return fmt.Errorf("target.unit_id must be > 0")
	}
	if t.UnitID > 0xFF {
		return fmt.Errorf("target.unit_id must be <= 255")
	}

	// Status block validation
	if u.Source.StatusSlot != nil {
		if t.StatusUnitID == nil {
			return fmt.Errorf("source.status_slot is set but target.status_unit_id is not")
		}
		if *t.StatusUnitID == 0 {
			return fmt.Errorf("target.status_unit_id must be > 0")
		}
		if *t.StatusUnitID > 0xFF {
			return fmt.Errorf("target.status_unit_id must be <= 255")
		}
	}

	// Poll
	if u.Poll.IntervalMs <= 0 {
		return fmt.Errorf("poll.interval_ms must be > 0")
	}

	return nil
}

// --------------------
// Helpers
// --------------------

// parseListenPort parses "host:port" and returns the port as uint16.
func parseListenPort(listen string) (uint16, error) {
	_, portStr, err := net.SplitHostPort(listen)
	if err != nil {
		return 0, fmt.Errorf("invalid listen address (expected host:port): %w", err)
	}

	n, err := strconv.Atoi(portStr)
	if err != nil {
		return 0, fmt.Errorf("invalid port %q: %w", portStr, err)
	}
	if n < 1 || n > 65535 {
		return 0, fmt.Errorf("port out of range: %d", n)
	}

	return uint16(n), nil
}
