// internal/engine/builder.go
package engine

import (
	"fmt"
	"time"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/core"
	"github.com/tamzrod/Aegis/internal/engine/modbusclient"
)

// Unit is one fully-built polling unit: a Poller and its StoreWriter.
type Unit struct {
	Poller *Poller
	Writer *StoreWriter
}

// Build constructs all engine units from configuration.
// store must already be fully populated (call config.BuildMemStore first).
// Assumes config has already passed validation.
func Build(cfg *config.Config, store core.Store) ([]Unit, error) {
	var units []Unit

	for _, u := range cfg.Replicator.Units {
		unit, err := buildUnit(u, store)
		if err != nil {
			return nil, fmt.Errorf("unit %q: %w", u.ID, err)
		}
		units = append(units, unit)
	}

	return units, nil
}

func buildUnit(
	u config.UnitConfig,
	store core.Store,
) (Unit, error) {

	// ---- Poller ----
	factory := func() (Client, error) {
		return modbusclient.New(modbusclient.Config{
			Endpoint: u.Source.Endpoint,
			UnitID:   u.Source.UnitID,
			Timeout:  time.Duration(u.Source.TimeoutMs) * time.Millisecond,
		})
	}

	reads := make([]ReadBlock, 0, len(u.Reads))
	for _, r := range u.Reads {
		reads = append(reads, ReadBlock{
			FC:       r.FC,
			Address:  r.Address,
			Quantity: r.Quantity,
			Interval: time.Duration(r.IntervalMs) * time.Millisecond,
		})
	}

	p, err := NewPoller(
		PollerConfig{
			UnitID: u.ID,
			Reads:  reads,
		},
		nil,     // no initial client; lazy connect on first tick
		factory,
	)
	if err != nil {
		return Unit{}, fmt.Errorf("poller build failed: %w", err)
	}

	// ---- WritePlan ----
	t := u.Target

	targets := []TargetMemory{
		{
			MemoryID: core.MemoryID{Port: t.Port, UnitID: t.UnitID},
			Offsets:  t.Offsets,
		},
	}

	plan := WritePlan{
		UnitID:  u.ID,
		Targets: targets,
	}

	// Optional device status target
	if t.StatusSlot != nil && t.StatusUnitID != nil {
		plan.Status = &StatusTarget{
			MemoryID:   core.MemoryID{Port: t.Port, UnitID: *t.StatusUnitID},
			BaseSlot:   *t.StatusSlot,
			DeviceName: u.Source.DeviceName,
		}
	}

	w := NewStoreWriter(plan, store)

	return Unit{Poller: p, Writer: w}, nil
}
