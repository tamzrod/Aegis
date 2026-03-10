// internal/puller/device.go
// ReadBlock, TransportCounters, Unit, and Build live here.
package puller

import (
	"fmt"
	"time"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/engine/modbusclient"
	"github.com/tamzrod/Aegis/internal/memory"
)

// ReadBlock describes one Modbus read geometry and its independent poll cadence.
type ReadBlock struct {
	FC       uint8
	Address  uint16
	Quantity uint16
	Interval time.Duration
}

// TransportCounters holds lifetime transport instrumentation for a single polling unit.
// These counters are:
//   - Monotonic
//   - Integer-only
//   - Passive observability only (do not influence control flow)
type TransportCounters struct {
	RequestsTotal        uint32
	ResponsesValidTotal  uint32
	TimeoutsTotal        uint32
	TransportErrorsTotal uint32

	ConsecutiveFailCurr uint16
	ConsecutiveFailMax  uint16
}

// Unit is one fully-built polling unit: a Poller and its StoreWriter.
type Unit struct {
	Poller *Poller
	Writer *memory.StoreWriter
}

// Build constructs all engine units from configuration.
// store must already be fully populated (call config.BuildMemStore first).
// Assumes config has already passed validation.
func Build(cfg *config.Config, store memory.Store) ([]Unit, error) {
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

func buildUnit(u config.UnitConfig, store memory.Store) (Unit, error) {
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
		nil,
		factory,
	)
	if err != nil {
		return Unit{}, fmt.Errorf("poller build failed: %w", err)
	}

	t := u.Target

	targets := []memory.TargetMemory{
		{
			MemoryID: memory.MemoryID{Port: t.Port, UnitID: t.UnitID},
			Offsets:  t.Offsets,
		},
	}

	plan := memory.WritePlan{
		UnitID:  u.ID,
		Targets: targets,
	}

	if t.StatusSlot != nil && t.StatusUnitID != nil {
		plan.Status = &memory.StatusTarget{
			MemoryID:   memory.MemoryID{Port: t.Port, UnitID: *t.StatusUnitID},
			BaseSlot:   *t.StatusSlot,
			DeviceName: u.Source.DeviceName,
		}
	}

	w := memory.NewStoreWriter(plan, store)

	return Unit{Poller: p, Writer: w}, nil
}
