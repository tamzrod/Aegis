// internal/adapter/webui/view.go
package webui

import (
	"encoding/json"
	"net/http"

	"github.com/tamzrod/Aegis/internal/config"
)

// configView is the top-level response for GET /api/config/view.
type configView struct {
	Devices     []deviceView `json:"devices"`
	SelectedKey string       `json:"selected_key"`
}

// deviceView is a UI-specific representation of a single replicator unit.
type deviceView struct {
	Key         string     `json:"key"`
	DisplayName string     `json:"display_name"`
	Source      sourceView `json:"source"`
	Reads       []readView `json:"reads"`
	Target      targetView `json:"target"`
}

// sourceView holds the fields shown in the Source panel.
type sourceView struct {
	Endpoint   string `json:"endpoint"`
	UnitID     uint8  `json:"unit_id"`
	TimeoutMs  int    `json:"timeout_ms"`
	DeviceName string `json:"device_name"`
}

// readView holds the fields shown in one Reads row.
type readView struct {
	FC         uint8  `json:"fc"`
	Address    uint16 `json:"address"`
	Quantity   uint16 `json:"quantity"`
	IntervalMs int    `json:"interval_ms"`
}

// targetView holds the fields shown in the Target panel.
type targetView struct {
	Port         uint16 `json:"port"`
	UnitID       uint16 `json:"unit_id"`
	StatusUnitID uint16 `json:"status_unit_id"`
	StatusSlot   uint16 `json:"status_slot"`
	Mode         string `json:"mode"`
}

// buildConfigView converts the active config into the UI view model.
func buildConfigView(cfg *config.Config) configView {
	devices := make([]deviceView, 0, len(cfg.Replicator.Units))
	for _, u := range cfg.Replicator.Units {
		reads := make([]readView, 0, len(u.Reads))
		for _, r := range u.Reads {
			reads = append(reads, readView{
				FC:         r.FC,
				Address:    r.Address,
				Quantity:   r.Quantity,
				IntervalMs: r.IntervalMs,
			})
		}

		name := u.Source.DeviceName
		if name == "" {
			name = u.ID
		}

		var statusUnitID, statusSlot uint16
		if u.Target.StatusUnitID != nil {
			statusUnitID = *u.Target.StatusUnitID
		}
		if u.Target.StatusSlot != nil {
			statusSlot = *u.Target.StatusSlot
		}

		devices = append(devices, deviceView{
			Key:         u.ID,
			DisplayName: name,
			Source: sourceView{
				Endpoint:   u.Source.Endpoint,
				UnitID:     u.Source.UnitID,
				TimeoutMs:  u.Source.TimeoutMs,
				DeviceName: u.Source.DeviceName,
			},
			Reads: reads,
			Target: targetView{
				Port:         u.Target.Port,
				UnitID:       u.Target.UnitID,
				StatusUnitID: statusUnitID,
				StatusSlot:   statusSlot,
				Mode:         u.Target.Mode,
			},
		})
	}

	selectedKey := ""
	if len(devices) > 0 {
		selectedKey = devices[0].Key
	}

	return configView{
		Devices:     devices,
		SelectedKey: selectedKey,
	}
}

// handleConfigView serves GET /api/config/view.
func (h *handlers) handleConfigView(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	yamlBytes := h.mgr.GetActiveConfigYAML()
	cfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse config: "+err.Error())
		return
	}

	view := buildConfigView(cfg)

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(view)
}
