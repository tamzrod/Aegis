// internal/adapter/http/server.go
// Package webui provides a read-only HTTP adapter for the Aegis runtime.
// It exposes three endpoints: /healthz (liveness), /status (runtime JSON),
// and /config (active config YAML). No write endpoints exist.
// The adapter reads state exclusively through narrow view interfaces to prevent
// cross-layer contamination between the HTTP layer and core/engine packages.
package webui

import (
	"context"
	"encoding/json"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/tamzrod/Aegis/internal/view"
)

// Server is a read-only HTTP server adapter.
type Server struct {
	listen  string
	runtime view.RuntimeView
	config  view.ConfigView
}

// NewServer creates a new read-only HTTP server.
// listen is the TCP address to bind (e.g. ":8080").
func NewServer(listen string, rv view.RuntimeView, cv view.ConfigView) *Server {
	return &Server{
		listen:  listen,
		runtime: rv,
		config:  cv,
	}
}

// Start binds the listener and serves requests until ctx is cancelled.
// Call as go srv.Start(ctx) to avoid blocking the caller.
func (s *Server) Start(ctx context.Context) {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", s.HandleHealthz)
	mux.HandleFunc("/status", s.HandleStatus)
	mux.HandleFunc("/config", s.HandleConfig)

	ln, err := net.Listen("tcp", s.listen)
	if err != nil {
		log.Printf("webui: failed to listen on %s: %v", s.listen, err)
		return
	}

	srv := &http.Server{Handler: mux}

	go func() {
		<-ctx.Done()
		if err := srv.Close(); err != nil {
			log.Printf("webui: error closing http server: %v", err)
		}
	}()

	log.Printf("webui: http server listening on %s", s.listen)
	if err := srv.Serve(ln); err != nil && err != http.ErrServerClosed {
		log.Printf("webui: http server error: %v", err)
	}
}

// HandleHealthz returns a plain-text liveness response.
func (s *Server) HandleHealthz(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK\n"))
}

// statusResponse is the JSON shape for the /status endpoint.
type statusResponse struct {
	StartTime      time.Time `json:"start_time"`
	UptimeSeconds  float64   `json:"uptime_seconds"`
	DeviceCount    int       `json:"device_count"`
	ReadBlockCount int       `json:"read_block_count"`
}

// HandleStatus returns a JSON runtime status snapshot.
func (s *Server) HandleStatus(w http.ResponseWriter, _ *http.Request) {
	now := time.Now()
	st := s.runtime.StartTime()
	resp := statusResponse{
		StartTime:      st,
		UptimeSeconds:  now.Sub(st).Seconds(),
		DeviceCount:    s.runtime.DeviceCount(),
		ReadBlockCount: s.runtime.ReadBlockCount(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// HandleConfig returns the active configuration as YAML bytes.
func (s *Server) HandleConfig(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/yaml")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(s.config.ActiveConfigYAML())
}
