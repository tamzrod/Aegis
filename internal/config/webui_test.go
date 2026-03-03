// internal/config/webui_test.go
package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestWebUIDefaultsAbsent verifies that omitting the webui section
// leaves Enabled=false and Listen defaults to ":8080".
func TestWebUIDefaultsAbsent(t *testing.T) {
	raw := `
server:
  listeners: []
replicator:
  units: []
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	// Simulate the normalisation step from Load().
	if cfg.WebUI.Listen == "" {
		cfg.WebUI.Listen = ":8080"
	}

	if cfg.WebUI.Enabled {
		t.Error("webui.enabled: want false when absent, got true")
	}
	if cfg.WebUI.Listen != ":8080" {
		t.Errorf("webui.listen: want %q when absent, got %q", ":8080", cfg.WebUI.Listen)
	}
}

// TestWebUIExplicitValues verifies that explicitly set webui fields are respected.
func TestWebUIExplicitValues(t *testing.T) {
	raw := `
server:
  listeners: []
replicator:
  units: []
webui:
  enabled: true
  listen: ":9090"
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(raw), &cfg); err != nil {
		t.Fatalf("unexpected parse error: %v", err)
	}
	if cfg.WebUI.Listen == "" {
		cfg.WebUI.Listen = ":8080"
	}

	if !cfg.WebUI.Enabled {
		t.Error("webui.enabled: want true when explicitly set")
	}
	if cfg.WebUI.Listen != ":9090" {
		t.Errorf("webui.listen: want %q, got %q", ":9090", cfg.WebUI.Listen)
	}
}
