// cmd/aegis/main.go — IO handling domain
// Responsibility: config load, memory-store build, Modbus TCP server adapter
// startup, poll-loop launch, and OS signal handling.
// All per-unit orchestration is delegated to runOrchestrator (orchestrator.go).
package main

import (
	"context"
	"log"
	"os"
	"os/signal"
	"syscall"

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

		// Orchestrator: consume poll results, write data, update per-block health and status.
		// Scheduling, policy enforcement, state mutation, and data transformation are
		// handled by runOrchestrator (see orchestrator.go, health.go, snapshot.go).
		go runOrchestrator(ctx, unitID, p, w, healthStore, out)

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
