// internal/engine/runner.go
package engine

import (
	"context"
	"log"
	"time"
)

// Run drives the poll loop for this Poller.
// It sends each PollResult to out on every tick.
// It exits when ctx is cancelled.
func (p *Poller) Run(ctx context.Context, out chan<- PollResult) {
	log.Printf("engine: poller %q started", p.cfg.UnitID)

	ticker := time.NewTicker(p.cfg.Interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("engine: poller %q stopped", p.cfg.UnitID)
			return

		case <-ticker.C:
			out <- p.PollOnce()
		}
	}
}
