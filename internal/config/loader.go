// internal/config/loader.go
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// MinimalConfigYAML is the default content written when no config file is found.
const MinimalConfigYAML = "replicator:\n  units: []\n"

// CreateMinimal writes a minimal valid config file to path.
// It is called at startup when no config file exists.
func CreateMinimal(path string) error {
	return os.WriteFile(path, []byte(MinimalConfigYAML), 0600)
}

// Load reads a YAML configuration file from path and returns the parsed Config.
// This function applies defaults (e.g. target.mode defaults to "B") and
// normalises field values (e.g. target.mode is uppercased) after YAML parsing.
// Structural validation is performed by Validate().
// If loading fails, the process should exit immediately.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}
	return LoadBytes(data)
}

// LoadBytes parses YAML config from in-memory bytes and returns the parsed Config.
// It applies the same defaults and normalisations as Load.
// Structural validation is performed by Validate().
func LoadBytes(data []byte) (*Config, error) {
	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}

	// Normalise per-target mode to uppercase; apply default "B" if not specified.
	// Apply default status_unit_id of 100 when not specified.
	for i := range cfg.Replicator.Units {
		m := strings.ToUpper(strings.TrimSpace(cfg.Replicator.Units[i].Target.Mode))
		if m == "" {
			m = TargetModeB
		}
		cfg.Replicator.Units[i].Target.Mode = m

		if cfg.Replicator.Units[i].Target.StatusUnitID == nil {
			defaultStatusUnitID := uint16(100)
			cfg.Replicator.Units[i].Target.StatusUnitID = &defaultStatusUnitID
		}
	}

	// Apply webui defaults.
	if cfg.WebUI.Listen == "" {
		cfg.WebUI.Listen = ":8080"
	}

	// Apply auth defaults: username=admin, password=admin (bcrypt hash).
	// Authentication is always enforced; these defaults ensure credentials are always
	// set so that a freshly created config requires explicit login via admin/admin.
	if cfg.Auth.Username == "" {
		cfg.Auth.Username = "admin"
	}
	if cfg.Auth.PasswordHash == "" {
		cfg.Auth.PasswordHash = "$2a$10$7NNFFDnya2hrHSrENsVU2exQ1dDJD/eURJ02rM2mHV716gFJh5eUi"
	}

	return &cfg, nil
}
