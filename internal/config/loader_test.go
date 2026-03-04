// internal/config/loader_test.go
package config

import (
	"testing"
)

func TestLoadBytesWebUIDefaults(t *testing.T) {
	// When webui section is absent, Enabled should be false and Listen should be ":8080".
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.1:502"
        timeout_ms: 1000
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
`)
	cfg, err := LoadBytes(yaml)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	if cfg.WebUI.Enabled {
		t.Error("WebUI.Enabled: want false (default), got true")
	}
	if cfg.WebUI.Listen != ":8080" {
		t.Errorf("WebUI.Listen: want %q, got %q", ":8080", cfg.WebUI.Listen)
	}
}

func TestLoadBytesDebugDefault(t *testing.T) {
	// When debug section is absent, AdapterRouting must default to false.
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.1:502"
        timeout_ms: 1000
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
`)
	cfg, err := LoadBytes(yaml)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	if cfg.Debug.AdapterRouting {
		t.Error("Debug.AdapterRouting: want false (default), got true")
	}
}

func TestLoadBytesDebugExplicit(t *testing.T) {
	// When debug.adapter_routing is set to true, it must be preserved.
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.1:502"
        timeout_ms: 1000
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
debug:
  adapter_routing: true
`)
	cfg, err := LoadBytes(yaml)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	if !cfg.Debug.AdapterRouting {
		t.Error("Debug.AdapterRouting: want true, got false")
	}
}

func TestLoadBytesWebUIExplicit(t *testing.T) {
	// When webui section is set, values should be preserved.
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      source:
        endpoint: "192.168.1.1:502"
        timeout_ms: 1000
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        port: 502
        unit_id: 1
webui:
  enabled: true
  listen: ":9090"
`)
	cfg, err := LoadBytes(yaml)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	if !cfg.WebUI.Enabled {
		t.Error("WebUI.Enabled: want true, got false")
	}
	if cfg.WebUI.Listen != ":9090" {
		t.Errorf("WebUI.Listen: want %q, got %q", ":9090", cfg.WebUI.Listen)
	}
}
