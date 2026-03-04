// cmd/aegis/main.go — IO handling domain
// Responsibility: config load, engine startup, WebUI server, OS signal handling.
// All per-unit orchestration is delegated to runOrchestrator (orchestrator.go).
package main

import (
	"errors"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/tamzrod/Aegis/internal/adapter/webui"
	"github.com/tamzrod/Aegis/internal/config"
)

const defaultConfigPath = "config.yaml"
const defaultWebUIListen = ":8080"

func main() {
	// --------------------
	// Determine config path (default: "config.yaml" in working directory)
	// --------------------
	cfgPath := defaultConfigPath
	if len(os.Args) >= 2 {
		cfgPath = os.Args[1]
	}

	rt := NewRuntimeManager(cfgPath)

	webuiListen := defaultWebUIListen
	startWebUI := false

	// --------------------
	// Load and validate configuration
	// --------------------
	if _, statErr := os.Stat(cfgPath); errors.Is(statErr, os.ErrNotExist) {
		// Config file not found — create a minimal config and start WebUI only.
		log.Println("aegis: config.yaml not found, creating new configuration")
		minYAML := []byte(config.MinimalConfigYAML)
		if writeErr := config.CreateMinimal(cfgPath); writeErr != nil {
			log.Printf("aegis: create config file: %v", writeErr)
		}
		rt.activeConfigYAML = minYAML
		startWebUI = true
	} else {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			log.Printf("aegis: config load failed: %v", err)
			rt.SetError(err)
			startWebUI = true
		} else if err := config.Validate(cfg); err != nil {
			log.Printf("aegis: config validation failed: %v", err)
			rt.SetError(err)
			webuiListen = cfg.WebUI.Listen
			startWebUI = true
		} else {
			// --------------------
			// Config is valid — start the engine
			// --------------------
			webuiListen = cfg.WebUI.Listen

			rawYAML, err := os.ReadFile(cfgPath)
			if err != nil {
				log.Printf("aegis: read config file: %v", err)
				rt.SetError(err)
				startWebUI = true
			} else {
				if err := rt.Start(cfg, rawYAML); err != nil {
					rt.SetError(err)
					log.Printf("aegis: engine start failed: %v", err)
				}

				defer rt.Stop()

				startWebUI = cfg.WebUI.Enabled
			}
		}
	}

	// --------------------
	// Start WebUI HTTP adapter
	// --------------------
	if startWebUI {
		srv := webui.NewServer(webuiListen, rt)
		go func() {
			if err := srv.ListenAndServe(); err != nil {
				log.Printf("aegis: webui: %v", err)
			}
		}()
		log.Printf("aegis: webui adapter starting on %s", webuiListen)
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
