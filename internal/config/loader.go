// internal/config/loader.go
package config

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// Load reads a YAML configuration file from path and returns the parsed Config.
// This function applies defaults (e.g. authority_mode defaults to "strict") and
// normalises field values (e.g. authority_mode is lowercased) after YAML parsing.
// Structural validation is performed by Validate().
// If loading fails, the process should exit immediately.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("parse config yaml: %w", err)
	}

	// Normalise authority_mode to lowercase; apply default if not specified.
	cfg.AuthorityMode = strings.ToLower(strings.TrimSpace(cfg.AuthorityMode))
	if cfg.AuthorityMode == "" {
		cfg.AuthorityMode = AuthorityModeStrict
	}

	return &cfg, nil
}
