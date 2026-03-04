// internal/adapter/webui/apply.go
package webui

import (
	"encoding/json"
	"io"
	"net/http"

	"gopkg.in/yaml.v3"

	"github.com/tamzrod/Aegis/internal/config"
)

// handleConfigApply serves PUT /api/config/apply.
// It accepts a configView JSON body, merges it into the currently active config
// (preserving fields not represented in the view model, such as target offsets
// and the webui section), marshals the result back to YAML, validates it, and
// calls RuntimeManager.Rebuild to atomically apply the new config at runtime.
func (h *handlers) handleConfigApply(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPut {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20)) // 1 MiB limit
	if err != nil {
		writeError(w, http.StatusBadRequest, "read body: "+err.Error())
		return
	}

	var v configView
	if err := json.Unmarshal(body, &v); err != nil {
		writeError(w, http.StatusBadRequest, "parse body: "+err.Error())
		return
	}

	// Load the currently active config as a base so that fields not exposed
	// in the view model (e.g. target.offsets, webui settings) are preserved.
	yamlBytes := h.mgr.GetActiveConfigYAML()
	baseCfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "parse active config: "+err.Error())
		return
	}

	mergeViewIntoConfig(v, baseCfg)

	newYAML, err := yaml.Marshal(baseCfg)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "marshal config: "+err.Error())
		return
	}

	// Validate the merged config before calling Rebuild.
	cfg, err := config.LoadBytes(newYAML)
	if err != nil {
		writeError(w, http.StatusBadRequest, "parse merged config: "+err.Error())
		return
	}
	if err := config.Validate(cfg); err != nil {
		writeError(w, http.StatusBadRequest, err.Error())
		return
	}

	// Rebuild atomically restarts the runtime with the new config.
	if err := h.mgr.Rebuild(cfg, newYAML); err != nil {
		writeError(w, http.StatusInternalServerError, "runtime rebuild failed")
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "applied"})
}

// mergeViewIntoConfig writes the device list from v into base, preserving any
// fields not represented in the view model (notably TargetConfig.Offsets).
func mergeViewIntoConfig(v configView, base *config.Config) {
	// Build a lookup of existing units by ID to preserve non-view fields.
	existing := make(map[string]config.UnitConfig, len(base.Replicator.Units))
	for _, u := range base.Replicator.Units {
		existing[u.ID] = u
	}

	units := make([]config.UnitConfig, 0, len(v.Devices))
	for _, d := range v.Devices {
		// Start from the existing unit (if any) to keep Offsets etc.
		unit := config.UnitConfig{ID: d.Key}
		if prev, ok := existing[d.Key]; ok {
			unit = prev
		}

		unit.Source = config.SourceConfig{
			Endpoint:   d.Source.Endpoint,
			UnitID:     d.Source.UnitID,
			TimeoutMs:  d.Source.TimeoutMs,
			DeviceName: d.Source.DeviceName,
		}

		unit.Reads = make([]config.ReadConfig, len(d.Reads))
		for i, r := range d.Reads {
			unit.Reads[i] = config.ReadConfig{
				FC:         r.FC,
				Address:    r.Address,
				Quantity:   r.Quantity,
				IntervalMs: r.IntervalMs,
			}
		}

		unit.Target.Port = d.Target.Port
		unit.Target.UnitID = d.Target.UnitID
		unit.Target.Mode = d.Target.Mode

		if d.Target.StatusUnitID != 0 {
			uid := d.Target.StatusUnitID
			unit.Target.StatusUnitID = &uid
		} else {
			unit.Target.StatusUnitID = nil
		}

		slot := d.Target.StatusSlot
		unit.Target.StatusSlot = &slot

		units = append(units, unit)
	}
	base.Replicator.Units = units
}
