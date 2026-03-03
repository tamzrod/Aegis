// internal/config/build_store.go
package config

import (
	"fmt"
	"strings"

	"github.com/tamzrod/Aegis/internal/core"
)

// BuildMemStore constructs a core.MemStore from the validated server configuration.
// Assumes Validate() has already passed.
func BuildMemStore(cfg *Config) (*core.MemStore, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}

	store := core.NewMemStore()

	for li, l := range cfg.Server.Listeners {
		port, err := parseListenPort(l.Listen)
		if err != nil {
			return nil, fmt.Errorf(
				"server.listeners[%d] (%s): invalid listen %q: %w",
				li, l.ID, l.Listen, err,
			)
		}

		for mi, m := range l.Memory {
			key := fmt.Sprintf(
				"server.listeners[%d] (%s).memory[%d] (unit_id=%d)",
				li, l.ID, mi, m.UnitID,
			)

			if err := buildAndRegisterMemory(store, port, key, m); err != nil {
				return nil, err
			}
		}
	}

	return store, nil
}

// buildAndRegisterMemory builds one Memory instance and adds it to the store.
func buildAndRegisterMemory(
	store *core.MemStore,
	port uint16,
	key string,
	def MemoryDef,
) error {
	layouts := core.MemoryLayouts{}

	if def.Coils.Count > 0 {
		layouts.Coils = &core.AreaLayout{
			Start: def.Coils.Start,
			Size:  def.Coils.Count,
		}
	}

	if def.DiscreteInputs.Count > 0 {
		layouts.DiscreteInputs = &core.AreaLayout{
			Start: def.DiscreteInputs.Start,
			Size:  def.DiscreteInputs.Count,
		}
	}

	if def.HoldingRegs.Count > 0 {
		layouts.HoldingRegs = &core.AreaLayout{
			Start: def.HoldingRegs.Start,
			Size:  def.HoldingRegs.Count,
		}
	}

	if def.InputRegs.Count > 0 {
		layouts.InputRegs = &core.AreaLayout{
			Start: def.InputRegs.Start,
			Size:  def.InputRegs.Count,
		}
	}

	mem, err := core.NewMemory(layouts)
	if err != nil {
		return fmt.Errorf("%s: memory create failed: %w", key, err)
	}

	// State sealing (presence = enabled)
	if def.StateSealing != nil {
		area := strings.ToLower(strings.TrimSpace(def.StateSealing.Area))
		if area != "coil" {
			return fmt.Errorf("%s: state_sealing.area must be 'coil'", key)
		}
		mem.SetStateSealing(core.StateSealingDef{
			Area:    core.AreaCoils,
			Address: def.StateSealing.Address,
		})
	}

	id := core.MemoryID{
		Port:   port,
		UnitID: def.UnitID,
	}

	if err := store.Add(id, mem); err != nil {
		return fmt.Errorf(
			"%s (port=%d unit_id=%d): register failed: %w",
			key, id.Port, id.UnitID, err,
		)
	}

	return nil
}
