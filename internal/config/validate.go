// internal/config/validate.go
package config

import (
	"fmt"
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

	if err := validateReplicator(cfg); err != nil {
		return err
	}

	return nil
}

// --------------------
// Replicator validation
// --------------------

func validateReplicator(cfg *Config) error {
	if len(cfg.Replicator.Units) == 0 {
		return fmt.Errorf("replicator.units: at least one unit required")
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

		if err := validateUnitConfig(u); err != nil {
			return fmt.Errorf("replicator.units[%d] (%s): %w", i, u.ID, err)
		}
	}

	// Enforce the surface identity rule: each (port, unit_id) must belong to exactly one unit.
	if err := validateTargetSurfaces(cfg); err != nil {
		return err
	}

	// Detect status slot conflicts and status_unit_id vs data unit_id collisions.
	if err := validateStatusSlots(cfg); err != nil {
		return err
	}

	return nil
}

func validateUnitConfig(u UnitConfig) error {
	// Source
	if err := validateIPv4Port(u.Source.Endpoint); err != nil {
		return err
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
		if r.IntervalMs <= 0 {
			return fmt.Errorf("reads[%d]: interval_ms must be > 0", j)
		}
	}

	// Target
	target := u.Target
	if target.Port == 0 {
		return fmt.Errorf("target.port must be > 0")
	}
	if target.UnitID == 0 {
		return fmt.Errorf("target.unit_id must be > 0")
	}
	if target.UnitID > 0xFF {
		return fmt.Errorf("target.unit_id must be <= 255")
	}

	// Status block validation
	if target.StatusSlot != nil {
		if target.StatusUnitID == nil {
			return fmt.Errorf("target.status_slot is set but target.status_unit_id is not")
		}
		if *target.StatusUnitID == 0 {
			return fmt.Errorf("target.status_unit_id must be > 0")
		}
		if *target.StatusUnitID > 0xFF {
			return fmt.Errorf("target.status_unit_id must be <= 255")
		}
	}

	// Target mode validation
	switch target.Mode {
	case TargetModeA, TargetModeB, TargetModeC:
		// valid
	default:
		return fmt.Errorf("target.mode: unknown value %q (valid: A, B, C)", target.Mode)
	}

	return nil
}

// validateTargetSurfaces enforces the surface identity rule:
// each (port, unit_id) pair must be assigned to exactly one replicator unit.
func validateTargetSurfaces(cfg *Config) error {
	type surfaceKey struct {
		port   uint16
		unitID uint16
	}
	seen := make(map[surfaceKey]string) // key → first unit ID
	for i, u := range cfg.Replicator.Units {
		sk := surfaceKey{port: u.Target.Port, unitID: u.Target.UnitID}
		if prev, exists := seen[sk]; exists {
			return fmt.Errorf(
				"replicator.units[%d] (%s): duplicate target surface (port=%d, unit_id=%d) already assigned to unit %q",
				i, u.ID, sk.port, sk.unitID, prev,
			)
		}
		seen[sk] = u.ID
	}
	return nil
}

// validateStatusSlots ensures:
//   - No two replicator units on the same (port, status_unit_id) share the same status_slot.
//   - No status_unit_id equals a data unit_id on the same port.
func validateStatusSlots(cfg *Config) error {
	// Collect all data unit IDs per port.
	dataUnitsByPort := make(map[uint16]map[uint16]struct{})
	for _, u := range cfg.Replicator.Units {
		port := u.Target.Port
		if dataUnitsByPort[port] == nil {
			dataUnitsByPort[port] = make(map[uint16]struct{})
		}
		dataUnitsByPort[port][u.Target.UnitID] = struct{}{}
	}

	// slotKey identifies a status slot namespace: (port, status_unit_id).
	type slotKey struct {
		port         uint16
		statusUnitID uint16
	}
	// seenSlots maps (port, status_unit_id) → (slot → first unit ID that claimed it).
	seenSlots := make(map[slotKey]map[uint16]string)

	for i, u := range cfg.Replicator.Units {
		if u.Target.StatusSlot == nil {
			continue
		}
		if u.Target.StatusUnitID == nil {
			continue // already caught by per-unit validation
		}

		port := u.Target.Port
		statusUID := *u.Target.StatusUnitID
		slot := *u.Target.StatusSlot

		// status_unit_id must not equal any data unit_id on the same port.
		if dataUnits, ok := dataUnitsByPort[port]; ok {
			if _, conflict := dataUnits[statusUID]; conflict {
				return fmt.Errorf(
					"replicator.units[%d] (%s): target.status_unit_id %d conflicts with "+
						"a data unit_id on port %d",
					i, u.ID, statusUID, port,
				)
			}
		}

		sk := slotKey{port: port, statusUnitID: statusUID}
		if seenSlots[sk] == nil {
			seenSlots[sk] = make(map[uint16]string)
		}
		if prev, exists := seenSlots[sk][slot]; exists {
			return fmt.Errorf(
				"replicator.units[%d] (%s): status_slot %d already used by unit %q "+
					"on (port=%d, status_unit_id=%d)",
				i, u.ID, slot, prev, port, statusUID,
			)
		}
		seenSlots[sk][slot] = u.ID
	}
	return nil
}

// validateIPv4Port validates that endpoint is a strict IPv4:port string.
// It trims whitespace, rejects hostnames, and validates each octet (0–255)
// and the port (1–65535).
func validateIPv4Port(endpoint string) error {
	ep := strings.TrimSpace(endpoint)
	if ep == "" {
		return fmt.Errorf("source.endpoint is required")
	}

	colonIdx := strings.LastIndex(ep, ":")
	if colonIdx < 0 {
		return fmt.Errorf("source.endpoint must be in ip:port format (e.g. 192.168.1.1:502)")
	}

	host := ep[:colonIdx]
	portStr := ep[colonIdx+1:]

	if host == "" {
		return fmt.Errorf("source.endpoint: IP address is required")
	}
	if portStr == "" {
		return fmt.Errorf("source.endpoint: port is required")
	}

	port, err := strconv.Atoi(portStr)
	if err != nil || port < 1 || port > 65535 {
		return fmt.Errorf("source.endpoint: port must be a number between 1 and 65535")
	}

	parts := strings.Split(host, ".")
	if len(parts) != 4 {
		return fmt.Errorf("source.endpoint: IP address must be a valid IPv4 address (e.g. 192.168.1.1)")
	}
	for _, part := range parts {
		octet, err := strconv.Atoi(part)
		if err != nil || octet < 0 || octet > 255 {
			return fmt.Errorf("source.endpoint: each IP octet must be between 0 and 255")
		}
	}

	return nil
}
