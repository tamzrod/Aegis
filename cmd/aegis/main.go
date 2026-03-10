// cmd/aegis/main.go — IO handling domain
// Responsibility: config load, engine startup, WebUI server, OS signal handling.
// All coordination is delegated to orchestrator.Manager.
package main

import (
"context"
"errors"
"log"
"os"
"os/signal"
"path/filepath"
"syscall"

"github.com/tamzrod/Aegis/internal/adapter/webui"
"github.com/tamzrod/Aegis/internal/config"
"github.com/tamzrod/Aegis/internal/orchestrator"
)

const defaultConfigPath = "config.yaml"
const defaultWebUIListen = ":8080"

func main() {
processCtx, processCancel := context.WithCancel(context.Background())
defer processCancel()

cfgPath := defaultConfigPath
if len(os.Args) >= 2 {
cfgPath = os.Args[1]
}

mgr := orchestrator.NewManager(cfgPath, processCtx)

webuiListen := defaultWebUIListen
startWebUI := false
var authCfg config.AuthConfig

if _, statErr := os.Stat(cfgPath); errors.Is(statErr, os.ErrNotExist) {
log.Println("aegis: config.yaml not found, creating new configuration")
minYAML := []byte(config.MinimalConfigYAML)
if writeErr := config.CreateMinimal(cfgPath); writeErr != nil {
log.Printf("aegis: create config file: %v", writeErr)
}
mgr.SetActiveConfigYAML(minYAML)
startWebUI = true
} else {
cfg, err := config.Load(cfgPath)
if err != nil {
log.Printf("aegis: config load failed: %v", err)
mgr.SetError(err)
startWebUI = true
} else if err := config.Validate(cfg); err != nil {
log.Printf("aegis: config validation failed: %v", err)
mgr.SetError(err)
webuiListen = cfg.WebUI.Listen
authCfg = cfg.Auth
startWebUI = true
} else {
webuiListen = cfg.WebUI.Listen
authCfg = cfg.Auth

rawYAML, err := os.ReadFile(cfgPath)
if err != nil {
log.Printf("aegis: read config file: %v", err)
mgr.SetError(err)
startWebUI = true
} else {
if err := mgr.Start(cfg, rawYAML); err != nil {
mgr.SetError(err)
log.Printf("aegis: engine start failed: %v", err)
}

defer mgr.Stop()

startWebUI = cfg.WebUI.Enabled
}
}
}

if startWebUI {
srv := webui.NewServer(webuiListen, mgr, authCfg)
dvPath := filepath.Join(filepath.Dir(cfgPath), "dataview.yaml")
srv.SetDataviewPath(dvPath)
go func() {
if err := srv.ListenAndServe(); err != nil {
log.Printf("aegis: webui: %v", err)
}
}()
log.Printf("aegis: webui adapter starting on %s", webuiListen)
}

log.Println("aegis: running — press Ctrl+C to stop")

quit := make(chan os.Signal, 1)
signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

<-quit

processCancel()
log.Println("aegis: shutting down")
}
