// cmd/aegis/main.go — IO handling domain
// Responsibility: config load, memory-store build, Modbus TCP server adapter
// startup, poll-loop launch, and OS signal handling.
// All per-unit orchestration is delegated to runOrchestrator (orchestrator.go).
package main

import (
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/tamzrod/Aegis/internal/adapter/webui"
	"github.com/tamzrod/Aegis/internal/config"
)

func main() {
	if len(os.Args) < 2 {
		log.Fatal("usage: aegis <config.yaml>")
	}

	cfgPath := os.Args[1]

	// --------------------
	// Load and validate config (fail fast on invalid config)
	// --------------------
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	cfg, err := config.LoadBytes(data)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	if err := config.Validate(cfg); err != nil {
		log.Fatalf("config validation failed: %v", err)
	}

	log.Println("aegis: config loaded and validated")

	// --------------------
	// Build initial runtime (memory store, engine, Modbus adapters)
	// --------------------
	rt, err := BuildRuntime(cfg, data, cfgPath)
	if err != nil {
		log.Fatalf("aegis: runtime build failed: %v", err)
	}

	log.Println("aegis: runtime started")

	// --------------------
	// Start WebUI server if enabled
	// --------------------
	if cfg.WebUI.Enabled {
		wsrv := webui.NewServer(cfg.WebUI.Listen, rt)
		go func() {
			log.Printf("aegis: webui listening on %s", cfg.WebUI.Listen)
			if err := wsrv.ListenAndServe(); err != nil {
				log.Printf("aegis: webui stopped: %v", err)
			}
		}()
	}

	log.Println("aegis: running — press Ctrl+C to stop")

	// --------------------
	// Wait for OS signal
	// --------------------
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("aegis: shutting down")
	rt.Stop()
}
