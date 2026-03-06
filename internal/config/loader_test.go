// internal/config/loader_test.go
package config

import (
	"testing"

	"golang.org/x/crypto/bcrypt"
)

func TestLoadBytesDefaultStatusUnitID(t *testing.T) {
	// When status_unit_id is absent, it should default to 100.
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
	u := cfg.Replicator.Units[0]
	if u.Target.StatusUnitID == nil {
		t.Fatal("Target.StatusUnitID: want &100, got nil")
	}
	if *u.Target.StatusUnitID != 100 {
		t.Errorf("Target.StatusUnitID: want 100, got %d", *u.Target.StatusUnitID)
	}
}

func TestLoadBytesExplicitStatusUnitIDPreserved(t *testing.T) {
	// When status_unit_id is explicitly set, it must not be overridden.
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
        status_unit_id: 255
`)
	cfg, err := LoadBytes(yaml)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	u := cfg.Replicator.Units[0]
	if u.Target.StatusUnitID == nil {
		t.Fatal("Target.StatusUnitID: want &255, got nil")
	}
	if *u.Target.StatusUnitID != 255 {
		t.Errorf("Target.StatusUnitID: want 255, got %d", *u.Target.StatusUnitID)
	}
}

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

// TestLoadBytesAuthDefaults verifies that when the auth section is absent, LoadBytes
// defaults to username=admin and a bcrypt hash of "admin", and sets DefaultPassword=true.
func TestLoadBytesAuthDefaults(t *testing.T) {
	configYAML := []byte(`
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
	cfg, err := LoadBytes(configYAML)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	if cfg.Auth.Username != "admin" {
		t.Errorf("Auth.Username: want %q, got %q", "admin", cfg.Auth.Username)
	}
	if cfg.Auth.PasswordHash == "" {
		t.Fatal("Auth.PasswordHash: want non-empty bcrypt hash, got empty string")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(cfg.Auth.PasswordHash), []byte("admin")); err != nil {
		t.Errorf("Auth.PasswordHash: default hash does not match password %q: %v", "admin", err)
	}
	if !cfg.Auth.DefaultPassword {
		t.Error("Auth.DefaultPassword: want true when password_hash is absent, got false")
	}
}

// TestLoadBytesAuthExplicitPreserved verifies that when the auth section is explicitly
// set, those values are not overridden by defaults.
func TestLoadBytesAuthExplicitPreserved(t *testing.T) {
	configYAML := []byte(`
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
auth:
  username: myuser
  password_hash: "$2a$04$somehashvalue"
`)
	cfg, err := LoadBytes(configYAML)
	if err != nil {
		t.Fatalf("LoadBytes: unexpected error: %v", err)
	}
	if cfg.Auth.Username != "myuser" {
		t.Errorf("Auth.Username: want %q, got %q", "myuser", cfg.Auth.Username)
	}
	if cfg.Auth.PasswordHash != "$2a$04$somehashvalue" {
		t.Errorf("Auth.PasswordHash: want %q, got %q", "$2a$04$somehashvalue", cfg.Auth.PasswordHash)
	}
	if cfg.Auth.DefaultPassword {
		t.Error("Auth.DefaultPassword: want false when password_hash is present, got true")
	}
}

// TestLoadBytesGroupPreserved verifies that a group field on a unit is parsed and
// retained without modification by LoadBytes.
func TestLoadBytesGroupPreserved(t *testing.T) {
	yaml := []byte(`
replicator:
  units:
    - id: plc1
      group: "Site A"
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
	if got := cfg.Replicator.Units[0].Group; got != "Site A" {
		t.Errorf("Group: want %q, got %q", "Site A", got)
	}
}

// TestLoadBytesGroupOmitted verifies that a unit without a group field has an
// empty Group string after loading.
func TestLoadBytesGroupOmitted(t *testing.T) {
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
	if got := cfg.Replicator.Units[0].Group; got != "" {
		t.Errorf("Group: want empty string, got %q", got)
	}
}

