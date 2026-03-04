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
	// StartRuntime starts the runtime engine using the active config.
	// Returns an error if the runtime is not in STOPPED state.
	StartRuntime() error
	// StopRuntime stops the running runtime engine without changing the config.
	// Returns an error if the runtime is not in RUNNING state.
	StopRuntime() error
}

// StatusProvider is an optional extension of Manager for exposing runtime state.
// If the concrete Manager also implements StatusProvider, the WebUI serves
// GET /api/runtime/status with the result of RuntimeStatus().
type StatusProvider interface {
	RuntimeStatus() runtime.RuntimeState
}

// ListenerProvider is an optional extension for exposing per-port listener status.
// If the concrete Manager also implements ListenerProvider, the WebUI serves
// GET /api/runtime/listeners.
type ListenerProvider interface {
	ListenerStatuses() []runtime.ListenerStatus
}

// DeviceStatusProvider is an optional extension for exposing per-device health status.
// If the concrete Manager also implements DeviceStatusProvider, the WebUI serves
// GET /api/runtime/devices with the result of DeviceStatuses().
type DeviceStatusProvider interface {
	DeviceStatuses() []runtime.DeviceStatus
}

// Server is the embedded WebUI HTTP server.
type Server struct {
	listen string
	srv    *http.Server
}

// NewServer creates a WebUI Server that listens on listen and uses mgr for runtime operations.
// If mgr also implements StatusProvider, the /api/runtime/status endpoint becomes active.
// If mgr also implements ListenerProvider, the /api/runtime/listeners endpoint becomes active.
// auth configures form-based session authentication; both username and password_hash must be
// non-empty for authentication to be enforced.
func NewServer(listen string, mgr Manager, auth config.AuthConfig) *Server {
	store := newSessionStore()

	mux := http.NewServeMux()

	h := &handlers{mgr: mgr, sessions: store, auth: auth}
	if sp, ok := mgr.(StatusProvider); ok {
		h.sp = sp
	}
	if lp, ok := mgr.(ListenerProvider); ok {
		h.lp = lp
	}
	if dp, ok := mgr.(DeviceStatusProvider); ok {
		h.dp = dp
	}

	// Unprotected routes: login page and login API endpoint.
	webFS, _ := fs.Sub(webFiles, "web")
	mux.HandleFunc("/login", func(w http.ResponseWriter, r *http.Request) {
		http.ServeFileFS(w, r, webFS, "login.html")
	})
	mux.HandleFunc("/api/login", h.handleLogin)

	// Protected API routes — all require a valid session cookie.
	protected := http.NewServeMux()
	protected.HandleFunc("/api/config/view", h.handleConfigView)
	protected.HandleFunc("/api/config/apply", h.handleConfigApply)
	protected.HandleFunc("/api/config/raw", h.handleConfigRaw)
	protected.HandleFunc("/api/config/export", h.handleConfigExport)
	protected.HandleFunc("/api/config/import", h.handleConfigImport)
	protected.HandleFunc("/api/reload", h.handleReload)
	protected.HandleFunc("/api/restart", h.handleRestart)
	protected.HandleFunc("/api/runtime/status", h.handleRuntimeStatus)
	protected.HandleFunc("/api/runtime/start", h.handleRuntimeStart)
	protected.HandleFunc("/api/runtime/stop", h.handleRuntimeStop)
	protected.HandleFunc("/api/runtime/listeners", h.handleRuntimeListeners)
	protected.HandleFunc("/api/runtime/devices", h.handleRuntimeDevices)
	protected.HandleFunc("/api/logout", h.handleLogout)
	protected.Handle("/", http.FileServer(http.FS(webFS)))

	mux.Handle("/", requireSession(store, protected))

	return &Server{
		listen: listen,
		srv:    &http.Server{Addr: listen, Handler: mux},
	}
}

// ListenAndServe starts the WebUI HTTP server. It blocks until the server stops.
func (s *Server) ListenAndServe() error {
	return s.srv.ListenAndServe()
}
