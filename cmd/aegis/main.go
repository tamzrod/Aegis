// cmd/aegis/main.go — IO handling domain
// Responsibility: recoverable startup, config load, WebUI server, OS signal handling.
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
	// Read and parse config file.
	// A missing or completely un-parseable file is still a hard failure because
	// we need at least the WebUI listen address to start the HTTP server.
	// --------------------
	data, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("config read failed: %v", err)
	}

	cfg, err := config.LoadBytes(data)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}

	// --------------------
	// Create the Runtime object early so it can be handed to the WebUI server
	// before the engine starts (or even if it never starts due to invalid config).
	// --------------------
	rt := NewRuntime(cfgPath)

	// --------------------
	// Start WebUI server first, before validating config, so the UI is reachable
	// even when the config is invalid and the user needs to fix it via WebUI.
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

	// --------------------
	// Validate config.  On failure, record the error in runtime state and keep
	// running so the WebUI remains accessible for the user to fix the config.
	// --------------------
	if err := config.Validate(cfg); err != nil {
		log.Printf("aegis: config validation failed: %v", err)
		rt.SetError(err)
		log.Println("aegis: running in degraded mode — fix config via WebUI or press Ctrl+C to stop")
	} else {
		log.Println("aegis: config loaded and validated")

		// --------------------
		// Build and start the engine (Modbus adapters + poll loops).
		// --------------------
		if err := rt.StartEngine(cfg, data); err != nil {
			log.Printf("aegis: runtime build failed: %v", err)
			rt.SetError(err)
		} else {
			log.Println("aegis: runtime started")
		}
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
