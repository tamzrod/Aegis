```go
// cmd/aegis/main.go — IO handling domain
// Responsibility: config load, engine startup, WebUI server, OS signal handling.
// All per-unit orchestration is delegated to runOrchestrator (orchestrator.go).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/tamzrod/Aegis/internal/adapter"
	webui "github.com/tamzrod/Aegis/internal/adapter/http"
	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/engine"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: aegis <config.yaml>")
	}

	cfgPath := os.Args[1]

	// --------------------
	// Load configuration
	// --------------------
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	// --------------------
	// Build the shared in-process memory store (derived from replicator config)
	// --------------------
	store, err := config.BuildMemStore(cfg)
	if err != nil {
		log.Fatalf("aegis: memory store build failed: %v", err)
	}

	// Collect unique listener ports derived from replicator targets.
	seenPorts := make(map[uint16]struct{})
	for _, u := range cfg.Replicator.Units {
		seenPorts[u.Target.Port] = struct{}{}
	}

	log.Printf("aegis: memory store initialized (%d unique port(s))", len(seenPorts))

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
	// One adapter is started per unique target port derived from replicator config.
	// --------------------
	authority := adapter.BuildAuthorityRegistry(cfg, healthStore)
	for port := range seenPorts {
		listenAddr := fmt.Sprintf(":%d", port)
		srv := adapter.NewServer(listenAddr, store, authority)

		go func(listen string, s *adapter.Server) {
			if err := s.ListenAndServe(); err != nil {
				log.Fatalf("aegis: adapter (%s) failed: %v", listen, err)
			}
		}(listenAddr, srv)
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
		go runOrchestrator(ctx, unitID, p, w, healthStore, out)

		go p.Run(ctx, out)
	}

	log.Println("aegis: replication engine started")

	// --------------------
	// Start optional read-only WebUI HTTP adapter (if enabled)
	// --------------------
	if cfg.WebUI.Enabled {
		var readBlocks int
		for _, u := range cfg.Replicator.Units {
			readBlocks += len(u.Reads)
		}

		configBytes, err := os.ReadFile(cfgPath)
		if err != nil {
			log.Printf("aegis: webui: could not read config file for /config endpoint: %v", err)
		}

		rv := &runtimeView{
			startTime:      time.Now(),
			deviceCount:    len(cfg.Replicator.Units),
			readBlockCount: readBlocks,
		}

		cv := &configView{data: configBytes}

		go webui.NewServer(cfg.WebUI.Listen, rv, cv).Start(ctx)

		log.Printf("aegis: webui adapter starting on %s", cfg.WebUI.Listen)
	}

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
```
