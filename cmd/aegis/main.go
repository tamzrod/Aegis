// cmd/aegis/main.go
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tamzrod/Aegis/internal/adapter"
	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/engine"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: aegis <config.yaml>")
	}

	cfgPath := os.Args[1]

	// --------------------
	// Load and validate config (fail fast on invalid config)
	// --------------------
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	if err := config.Validate(cfg); err != nil {
		log.Fatalf("config validation failed: %v", err)
	}

	log.Println("aegis: config loaded and validated")

	// --------------------
	// Build the shared in-process memory store
	// --------------------
	store, err := config.BuildMemStore(cfg)
	if err != nil {
		log.Fatalf("aegis: memory store build failed: %v", err)
	}

	log.Printf("aegis: memory store initialized (%d listeners)", len(cfg.Server.Listeners))

	// --------------------
	// Build per-block health store
	// --------------------
	healthStore := engine.NewBlockHealthStore()

	// --------------------
	// Build replication engine units
	// --------------------
	units, err := engine.Build(cfg, store)
	if err != nil {
		log.Fatalf("aegis: engine build failed: %v", err)
	}

	log.Printf("aegis: engine built (%d units)", len(units))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// --------------------
	// Build authority registry and start Modbus TCP server adapters
	// --------------------
	authority := adapter.BuildAuthorityRegistry(cfg, healthStore)
	for _, l := range cfg.Server.Listeners {
		srv := adapter.NewServer(l.Listen, store, authority)
		go func(id, listen string, s *adapter.Server) {
			if err := s.ListenAndServe(); err != nil {
				log.Fatalf("aegis: adapter %s (%s) failed: %v", id, listen, err)
			}
		}(l.ID, l.Listen, srv)
	}

	log.Println("aegis: server adapters started")

	// --------------------
	// Start replication engine poll loops
	// --------------------
	for _, u := range units {
		out := make(chan engine.PollResult, 8)
		w := u.Writer
		p := u.Poller
		unitID := p.UnitID()

		// Orchestrator: consume poll results, write data, update per-block health and status
		go func(unitID string, poller *engine.Poller, writer *engine.StoreWriter, ch <-chan engine.PollResult) {
			snap := engine.StatusSnapshot{
				Health: engine.HealthUnknown,
			}

			// Per-block health state (indexed by block index)
			blockHealth := make(map[int]engine.ReadBlockHealth)

			secTicker := time.NewTicker(time.Second)
			defer secTicker.Stop()

			// Initial full assert of status block
			_ = writer.WriteStatus(snap)

			for {
				select {
				case <-ctx.Done():
					return

				case res := <-ch:
					if err := writer.Write(res); err != nil {
						log.Printf("aegis: write error (unit=%s): %v", unitID, err)
					}

					// Update per-block health state from poll result.
					for _, upd := range res.BlockUpdates {
						h := blockHealth[upd.BlockIdx]
						if upd.Success {
							h.Timeout = false
							h.ConsecutiveErrors = 0
							h.LastExceptionCode = 0
							h.LastSuccess = res.At
						} else {
							h.ConsecutiveErrors++
							h.LastError = res.At
							if upd.Timeout {
								h.Timeout = true
								h.LastExceptionCode = 0
							} else {
								h.Timeout = false
								h.LastExceptionCode = upd.ExceptionCode
							}
						}
						blockHealth[upd.BlockIdx] = h
						healthStore.Set(engine.BlockHealthKey{UnitID: unitID, BlockIdx: upd.BlockIdx}, h)
					}

					changed := false

					if res.Err == nil {
						if snap.Health != engine.HealthOK {
							snap.Health = engine.HealthOK
							changed = true
						}
						if snap.LastErrorCode != 0 {
							snap.LastErrorCode = 0
							changed = true
						}
						if snap.SecondsInError != 0 {
							snap.SecondsInError = 0
							changed = true
						}
					} else {
						if snap.Health != engine.HealthError {
							snap.Health = engine.HealthError
							changed = true
						}
						code := engine.ErrorCode(res.Err)
						if snap.LastErrorCode != code {
							snap.LastErrorCode = code
							changed = true
						}
					}

					// Inject transport counters
					c := poller.Counters()
					if snap.RequestsTotal != c.RequestsTotal {
						snap.RequestsTotal = c.RequestsTotal
						changed = true
					}
					if snap.ResponsesValidTotal != c.ResponsesValidTotal {
						snap.ResponsesValidTotal = c.ResponsesValidTotal
						changed = true
					}
					if snap.TimeoutsTotal != c.TimeoutsTotal {
						snap.TimeoutsTotal = c.TimeoutsTotal
						changed = true
					}
					if snap.TransportErrorsTotal != c.TransportErrorsTotal {
						snap.TransportErrorsTotal = c.TransportErrorsTotal
						changed = true
					}
					if snap.ConsecutiveFailCurr != c.ConsecutiveFailCurr {
						snap.ConsecutiveFailCurr = c.ConsecutiveFailCurr
						changed = true
					}
					if snap.ConsecutiveFailMax != c.ConsecutiveFailMax {
						snap.ConsecutiveFailMax = c.ConsecutiveFailMax
						changed = true
					}

					if changed {
						if err := writer.WriteStatus(snap); err != nil {
							log.Printf("aegis: status write error (unit=%s): %v", unitID, err)
						}
					}

				case <-secTicker.C:
					if snap.Health != engine.HealthOK && snap.SecondsInError < 65535 {
						snap.SecondsInError++
						if err := writer.WriteStatus(snap); err != nil {
							log.Printf("aegis: status tick write error (unit=%s): %v", unitID, err)
						}
					}
				}
			}
		}(unitID, p, w, out)

		go p.Run(ctx, out)
	}

	log.Println("aegis: replication engine started")
	log.Println("aegis: running — press Ctrl+C to stop")

	// --------------------
	// Wait for OS signal
	// --------------------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("aegis: shutting down")
	cancel()
}
