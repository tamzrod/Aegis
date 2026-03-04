// cmd/aegis/main.go — IO handling domain
// Responsibility: config load, engine startup, WebUI server, OS signal handling.
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
	// Load and validate configuration
	// --------------------
	cfg, err := config.Load(cfgPath)
	if err != nil {
		log.Fatalf("config load failed: %v", err)
	}
	if err := config.Validate(cfg); err != nil {
		log.Fatalf("config validation failed: %v", err)
	}

	// --------------------
	// Create the Runtime and start the engine
	// --------------------
	rt := NewRuntime(cfgPath)

	rawYAML, err := os.ReadFile(cfgPath)
	if err != nil {
		log.Fatalf("aegis: read config file: %v", err)
	}

	if err := rt.StartEngine(cfg, rawYAML); err != nil {
		rt.SetError(err)
		log.Fatalf("aegis: engine start failed: %v", err)
	}

	defer rt.Stop()

	// --------------------
	// Start WebUI HTTP adapter (if enabled)
	// --------------------
	if cfg.WebUI.Enabled {
		srv := webui.NewServer(cfg.WebUI.Listen, rt)
		go func() {
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("aegis: webui: %v", err)
			}
		}()
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
}
