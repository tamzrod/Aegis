// internal/adapter/webui/dataview.go
package webui

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sync"

	"gopkg.in/yaml.v3"
)

// dataviewFileConfig is the top-level structure of dataview.yaml.
// It maps: device_key → fc_key (e.g. "fc3") → address_key (e.g. "7000") → register config.
type dataviewFileConfig struct {
	Dataview map[string]map[string]map[string]dataviewRegister `yaml:"dataview"`
}

// dataviewRegister holds the per-register viewer configuration persisted in dataview.yaml.
type dataviewRegister struct {
	Name       string `yaml:"name,omitempty"        json:"name,omitempty"`
	Type       string `yaml:"type,omitempty"        json:"type,omitempty"`
	WordOrder  string `yaml:"word_order,omitempty"  json:"word_order,omitempty"`
	AsciiCount int    `yaml:"ascii_count,omitempty" json:"ascii_count,omitempty"`
}

// dataviewMu serialises concurrent access to the dataview file.
var dataviewMu sync.Mutex

// readDataviewFile reads and parses the dataview.yaml file at path.
// Returns an empty config (not an error) if the file does not exist.
func readDataviewFile(path string) (dataviewFileConfig, error) {
	var cfg dataviewFileConfig
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		cfg.Dataview = make(map[string]map[string]map[string]dataviewRegister)
		return cfg, nil
	}
	if err != nil {
		return cfg, fmt.Errorf("read dataview: %w", err)
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse dataview: %w", err)
	}
	if cfg.Dataview == nil {
		cfg.Dataview = make(map[string]map[string]map[string]dataviewRegister)
	}
	return cfg, nil
}

// writeDataviewFile marshals cfg to YAML and writes it to path.
func writeDataviewFile(path string, cfg dataviewFileConfig) error {
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal dataview: %w", err)
	}
	if err := os.WriteFile(path, data, 0644); err != nil {
		return fmt.Errorf("write dataview: %w", err)
	}
	return nil
}

// handleDataviewGet serves GET /api/dataview.
// Returns the full dataview register configuration as JSON.
// Returns an empty config (not an error) when no dataview file exists yet.
func (h *handlers) handleDataviewGet(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.dataviewPath == "" {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"registers":{}}` + "\n"))
		return
	}

	dataviewMu.Lock()
	cfg, err := readDataviewFile(h.dataviewPath)
	dataviewMu.Unlock()

	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	resp := map[string]interface{}{"registers": cfg.Dataview}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(resp)
}

// dataviewPutRequest is the JSON body accepted by PUT /api/dataview.
type dataviewPutRequest struct {
	Device     string `json:"device"`
	FC         uint8  `json:"fc"`
	Address    uint16 `json:"address"`
	Name       string `json:"name"`
	Type       string `json:"type"`
	WordOrder  string `json:"word_order"`
	AsciiCount int    `json:"ascii_count"`
}

// handleDataviewPut serves PUT /api/dataview.
// Updates a single register entry (name, type, word_order) in the dataview config file.
// If all fields are empty after the update, the entry is removed.
func (h *handlers) handleDataviewPut(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	if h.dataviewPath == "" {
		writeError(w, http.StatusServiceUnavailable, "dataview not configured")
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, maxConfigBodyBytes))
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var req dataviewPutRequest
	if err := json.Unmarshal(body, &req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	if req.Device == "" {
		writeError(w, http.StatusBadRequest, "device is required")
		return
	}
	if req.FC < 1 || req.FC > 4 {
		writeError(w, http.StatusBadRequest, "fc must be 1–4")
		return
	}

	fcKey := fmt.Sprintf("fc%d", req.FC)
	addrKey := fmt.Sprintf("%d", req.Address)

	dataviewMu.Lock()
	defer dataviewMu.Unlock()

	cfg, err := readDataviewFile(h.dataviewPath)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	if cfg.Dataview[req.Device] == nil {
		cfg.Dataview[req.Device] = make(map[string]map[string]dataviewRegister)
	}
	if cfg.Dataview[req.Device][fcKey] == nil {
		cfg.Dataview[req.Device][fcKey] = make(map[string]dataviewRegister)
	}

	entry := cfg.Dataview[req.Device][fcKey][addrKey]
	entry.Name = req.Name
	entry.Type = req.Type
	entry.WordOrder = req.WordOrder
	entry.AsciiCount = req.AsciiCount

	// Remove the entry when all fields are cleared.
	if entry.Name == "" && entry.Type == "" && entry.WordOrder == "" && entry.AsciiCount == 0 {
		delete(cfg.Dataview[req.Device][fcKey], addrKey)
	} else {
		cfg.Dataview[req.Device][fcKey][addrKey] = entry
	}

	if err := writeDataviewFile(h.dataviewPath, cfg); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}
