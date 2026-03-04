// internal/adapter/webui/server.go
package webui

import (
	"embed"
	"io/fs"
	"net/http"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/runtime"
)

//go:embed web
var webFiles embed.FS

// Manager is the interface the WebUI server uses to interact with the running Aegis runtime.
// It is implemented by the concrete Runtime in cmd/aegis.
type Manager interface {
	// GetActiveConfigYAML returns a copy of the currently active config YAML.
	GetActiveConfigYAML() []byte
	// ApplyConfig validates yamlBytes, writes it to disk, and soft-rebuilds the runtime.
	ApplyConfig(yamlBytes []byte) error
	// ReloadFromDisk re-reads the config file, validates it, and soft-rebuilds the runtime.
	ReloadFromDisk() error
	// Rebuild atomically stops the running engine and starts it with the new config.
	// The caller is responsible for validating cfg before calling Rebuild.
	Rebuild(cfg *config.Config, yamlBytes []byte) error
}

// StatusProvider is an optional extension of Manager for exposing runtime state.
// If the concrete Manager also implements StatusProvider, the WebUI serves
// GET /api/runtime/status with the result of RuntimeStatus().
type StatusProvider interface {
	RuntimeStatus() runtime.RuntimeState
}

// Server is the embedded WebUI HTTP server.
type Server struct {
	listen string
	srv    *http.Server
}

// NewServer creates a WebUI Server that listens on listen and uses mgr for runtime operations.
// If mgr also implements StatusProvider, the /api/runtime/status endpoint becomes active.
func NewServer(listen string, mgr Manager) *Server {
	mux := http.NewServeMux()

	h := &handlers{mgr: mgr}
	if sp, ok := mgr.(StatusProvider); ok {
		h.sp = sp
	}
	mux.HandleFunc("/api/config/view", h.handleConfigView)
	mux.HandleFunc("/api/config/apply", h.handleConfigApply)
	mux.HandleFunc("/api/config/raw", h.handleConfigRaw)
	mux.HandleFunc("/api/config/export", h.handleConfigExport)
	mux.HandleFunc("/api/config/import", h.handleConfigImport)
	mux.HandleFunc("/api/reload", h.handleReload)
	mux.HandleFunc("/api/restart", h.handleRestart)
	mux.HandleFunc("/api/runtime/status", h.handleRuntimeStatus)

	webFS, _ := fs.Sub(webFiles, "web")
	mux.Handle("/", http.FileServer(http.FS(webFS)))

	return &Server{
		listen: listen,
		srv:    &http.Server{Addr: listen, Handler: mux},
	}
}

// ListenAndServe starts the WebUI HTTP server. It blocks until the server stops.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}
