// internal/config/loader_test.go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalYAML is a minimal valid YAML body (no authority_mode) used to test Load defaults.
const minimalYAML = `
server:
  listeners:
    - id: "main"
      listen: ":502"
      memory:
        - unit_id: 1
          holding_registers:
            start: 0
            count: 10
replicator:
  units:
    - id: "plc1"
      source:
        endpoint: "192.168.1.1:502"
        timeout_ms: 1000
      reads:
        - fc: 3
          address: 0
          quantity: 10
          interval_ms: 1000
      target:
        listener_id: "main"
        unit_id: 1
`

// writeTemp writes content to a temp file and returns its path.
func writeTemp(t *testing.T, content string) string {
	t.Helper()
	f, err := os.CreateTemp(t.TempDir(), "aegis-*.yaml")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	f.Close()
	return filepath.Clean(f.Name())
}

// TestLoadDefaultAuthorityModeIsBuffer verifies that Load() sets authority_mode to
// "buffer" when the field is absent from the YAML.
func TestLoadDefaultAuthorityModeIsBuffer(t *testing.T) {
	path := writeTemp(t, minimalYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.AuthorityMode != AuthorityModeBuffer {
		t.Errorf("expected default authority_mode = %q, got %q", AuthorityModeBuffer, cfg.AuthorityMode)
	}
}

// TestLoadAliasA verifies that authority_mode: "a" is normalised to "standalone".
func TestLoadAliasA(t *testing.T) {
	path := writeTemp(t, "authority_mode: \"a\"\n"+minimalYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.AuthorityMode != AuthorityModeStandalone {
		t.Errorf("expected alias 'a' → %q, got %q", AuthorityModeStandalone, cfg.AuthorityMode)
	}
}

// TestLoadAliasB verifies that authority_mode: "b" is normalised to "buffer".
func TestLoadAliasB(t *testing.T) {
	path := writeTemp(t, "authority_mode: \"b\"\n"+minimalYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.AuthorityMode != AuthorityModeBuffer {
		t.Errorf("expected alias 'b' → %q, got %q", AuthorityModeBuffer, cfg.AuthorityMode)
	}
}

// TestLoadAliasC verifies that authority_mode: "c" is normalised to "strict".
func TestLoadAliasC(t *testing.T) {
	path := writeTemp(t, "authority_mode: \"c\"\n"+minimalYAML)
	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: unexpected error: %v", err)
	}
	if cfg.AuthorityMode != AuthorityModeStrict {
		t.Errorf("expected alias 'c' → %q, got %q", AuthorityModeStrict, cfg.AuthorityMode)
	}
}
