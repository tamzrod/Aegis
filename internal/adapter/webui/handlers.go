// internal/adapter/webui/handlers.go
package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/tamzrod/Aegis/internal/config"
	"github.com/tamzrod/Aegis/internal/runtime"
)

// maxConfigBodyBytes is the maximum accepted request body size for config endpoints.
const maxConfigBodyBytes = 1 << 20 // 1 MiB

// maxLoginBodyBytes is the maximum accepted request body size for the login endpoint.
const maxLoginBodyBytes = 1 << 16 // 64 KiB

type handlers struct {
	mgr      Manager
	sp       StatusProvider
	lp       ListenerProvider
	dp       DeviceStatusProvider
	dsr      DeviceStatusReader
	pu       PasswordUpdater
	sessions *sessionStore
	authMu   sync.RWMutex
	auth     config.AuthConfig
}

// handleConfigRaw serves GET /api/config/raw and PUT /api/config/raw.
func (h *handlers) handleConfigRaw(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		h.getConfigRaw(w, r)
	case http.MethodPut:
		h.putConfigRaw(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

// getConfigRaw returns the active config as text/yaml.
func (h *handlers) getConfigRaw(w http.ResponseWriter, _ *http.Request) {
	data := h.mgr.GetActiveConfigYAML()
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// putConfigRaw validates, writes to disk, and soft-rebuilds the runtime.
func (h *handlers) putConfigRaw(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	if err := h.mgr.ApplyConfig(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleReload re-reads the config file, validates, and soft-rebuilds.
func (h *handlers) handleReload(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.mgr.ReloadFromDisk(); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.WriteHeader(http.StatusOK)
}

// handleRestart returns 200 then soft-restarts the replication engine after a short delay.
func (h *handlers) handleRestart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	w.WriteHeader(http.StatusOK)

	go func() {
		time.Sleep(100 * time.Millisecond)
		if err := h.mgr.ReloadFromDisk(); err != nil {
			log.Printf("aegis: soft restart failed: %v", err)
		} else {
			log.Printf("aegis: soft restart completed successfully")
		}
	}()
}

// handleRuntimeDevices serves GET /api/runtime/devices.
// It returns per-device status as a JSON array.
func (h *handlers) handleRuntimeDevices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var statuses interface{}
	if h.dp != nil {
		statuses = h.dp.DeviceStatuses()
	} else {
		statuses = []struct{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(statuses)
}

// handleDeviceStatus serves GET /api/device/status.
// Query parameters: port, unit_id (status_unit_id), slot (status_slot).
// It reads and returns the decoded device status register block from the store.
func (h *handlers) handleDeviceStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.dsr == nil {
		writeError(w, http.StatusServiceUnavailable, "device status not available")
		return
	}

	parseU16 := func(key string) (uint16, bool) {
		s := r.URL.Query().Get(key)
		if s == "" {
			return 0, false
		}
		var v uint16
		if _, err := fmt.Sscanf(s, "%d", &v); err != nil {
			return 0, false
		}
		return v, true
	}

	port, okPort := parseU16("port")
	unitID, okUnit := parseU16("unit_id")
	slot, okSlot := parseU16("slot")

	if !okPort || !okUnit || !okSlot {
		writeError(w, http.StatusBadRequest, "port, unit_id, and slot query parameters are required")
		return
	}

	snap, err := h.dsr.ReadDeviceStatus(port, unitID, slot)
	if err != nil {
		writeError(w, http.StatusNotFound, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(snap)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// handleConfigExport serves GET /api/config/export.
// It returns the active configuration as a downloadable config.yaml file.
func (h *handlers) handleConfigExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	data := h.mgr.GetActiveConfigYAML()
	w.Header().Set("Content-Type", "text/yaml; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="config.yaml"`)
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

// handleConfigImport serves POST /api/config/import.
// It accepts raw YAML bytes, validates, writes to disk, and reloads the runtime.
func (h *handlers) handleConfigImport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	if err := h.mgr.ApplyConfig(body); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "imported"})
}

// handleRuntimeStatus serves GET /api/runtime/status.
// It returns the current RuntimeState as JSON.
// If no StatusProvider is available it returns the zero state (running=false).
func (h *handlers) handleRuntimeStatus(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var state interface{}
	if h.sp != nil {
		s := h.sp.RuntimeStatus()
		state = s
	} else {
		state = runtime.RuntimeState{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(state)
}

// handleRuntimeStart serves POST /api/runtime/start.
// It starts the runtime engine using the currently active config.
// Returns 409 if the runtime is not in STOPPED state.
func (h *handlers) handleRuntimeStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.mgr.StartRuntime(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "started"})
}

// handleRuntimeStop serves POST /api/runtime/stop.
// It stops the running runtime engine without changing the config.
// Returns 409 if the runtime is not in RUNNING state.
func (h *handlers) handleRuntimeStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if err := h.mgr.StopRuntime(); err != nil {
		writeError(w, http.StatusConflict, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "stopped"})
}

// handleRuntimeListeners serves GET /api/runtime/listeners.
// It returns per-port listener status as a JSON array.
func (h *handlers) handleRuntimeListeners(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	var statuses interface{}
	if h.lp != nil {
		statuses = h.lp.ListenerStatuses()
	} else {
		statuses = []struct{}{}
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(statuses)
}

// loginRequest is the JSON body accepted by POST /api/login.
type loginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// handleLogin serves POST /api/login.
// It validates the supplied credentials and, on success, sets a session cookie
// and returns 200 {"status":"ok"}. On failure it returns 401 {"error":"..."}.
func (h *handlers) handleLogin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var req loginRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if !checkCredentials(h.auth, req.Username, req.Password) {
		writeError(w, http.StatusUnauthorized, "invalid credentials")
		return
	}

	h.authMu.RLock()
	requireChange := h.auth.DefaultPassword
	h.authMu.RUnlock()

	token, err := h.sessions.create(requireChange)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "session error")
		return
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    token,
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   int(sessionTTL / time.Second),
	})

	resp := map[string]interface{}{"status": "ok"}
	if requireChange {
		resp["password_change_required"] = true
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// changePasswordRequest is the JSON body accepted by POST /api/change-password.
type changePasswordRequest struct {
	NewPassword     string `json:"new_password"`
	ConfirmPassword string `json:"confirm_password"`
}

// handleChangePassword serves POST /api/change-password.
// It validates the new password, hashes it, writes it to config, and clears
// the password-change-required flag on the current session.
func (h *handlers) handleChangePassword(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxLoginBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var req changePasswordRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.NewPassword == "" {
		writeError(w, http.StatusBadRequest, "new password is required")
		return
	}
	if req.NewPassword != req.ConfirmPassword {
		writeError(w, http.StatusBadRequest, "passwords do not match")
		return
	}

	if h.pu == nil {
		writeError(w, http.StatusInternalServerError, "password update not supported")
		return
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "hash error")
		return
	}

	if err := h.pu.UpdatePasswordHash(string(hash)); err != nil {
		writeError(w, http.StatusInternalServerError, "update failed: "+err.Error())
		return
	}

	// Clear the password-change-required flag on the current session.
	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		h.sessions.clearPasswordChangeRequired(cookie.Value)
	}

	// Update the in-memory auth config so future logins use the new hash.
	h.authMu.Lock()
	h.auth.PasswordHash = string(hash)
	h.auth.DefaultPassword = false
	h.authMu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

// handleLogout serves POST /api/logout.
// It invalidates the current session cookie and redirects to /login.
func (h *handlers) handleLogout(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if cookie, err := r.Cookie(sessionCookieName); err == nil {
		h.sessions.delete(cookie.Value)
	}

	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName,
		Value:    "",
		Path:     "/",
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		MaxAge:   -1,
	})

	http.Redirect(w, r, "/login", http.StatusFound)
}
