// internal/engine/runner.go
package engine

import (
	"context"
	"log"
	"time"
)

// Run drives the poll loop for this Poller.
// The ticker fires at the minimum read interval so that no read block is delayed
// by more than one tick relative to its configured cadence.
// It sends each PollResult (possibly with an empty Blocks slice when no reads
// were due) to out on every tick.
// It exits when ctx is cancelled.
func (p *Poller) Run(ctx context.Context, out chan<- PollResult) {
	log.Printf("engine: poller %q started", p.cfg.UnitID)

	ticker := time.NewTicker(p.minInterval())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("engine: poller %q stopped", p.cfg.UnitID)
			return

		case t := <-ticker.C:
			out <- p.pollAt(t)
		}
	}
}
