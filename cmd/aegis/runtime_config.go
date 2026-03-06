// cmd/aegis/runtime_config.go — config management domain
// Responsibility: applying, reloading, and persisting configuration changes.
// ApplyConfig validates new YAML, writes it atomically to disk, and rebuilds
// the runtime. ReloadFromDisk re-reads the persisted file and rebuilds.
// UpdatePasswordHash patches only the auth section without triggering a
// full runtime rebuild. GetActiveConfigYAML returns the in-memory YAML copy.
package main

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/tamzrod/Aegis/internal/config"
)

// GetActiveConfigYAML returns a copy of the active config YAML bytes.
func (r *RuntimeManager) GetActiveConfigYAML() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]byte, len(r.activeConfigYAML))
	copy(out, r.activeConfigYAML)
	return out
}

// ApplyConfig parses yamlBytes, validates, writes to disk, then atomically rebuilds the runtime.
// The new YAML becomes the active config.
func (r *RuntimeManager) ApplyConfig(yamlBytes []byte) error {
	cfg, err := config.LoadBytes(yamlBytes)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if err := atomicWriteConfig(r.configPath, yamlBytes); err != nil {
		return err
	}

	return r.rebuild(cfg, yamlBytes)
}

// ReloadFromDisk re-reads the config file, validates it, then atomically rebuilds the runtime.
func (r *RuntimeManager) ReloadFromDisk() error {
	r.mu.Lock()
	path := r.configPath
	r.mu.Unlock()

	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read config file: %w", err)
	}

	cfg, err := config.LoadBytes(data)
	if err != nil {
		return err
	}
	if err := config.Validate(cfg); err != nil {
		return err
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	return r.rebuild(cfg, data)
}

// UpdatePasswordHash writes a new bcrypt password hash to the auth section of
// config.yaml without triggering a runtime rebuild. It updates the active config
// YAML in memory so subsequent reloads use the new credentials.
func (r *RuntimeManager) UpdatePasswordHash(hash string) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Read the current raw config from disk.
	rawYAML, err := os.ReadFile(r.configPath)
	if err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	// Parse as a generic map to preserve the existing structure.
	var root map[string]interface{}
	if err := yaml.Unmarshal(rawYAML, &root); err != nil {
		return fmt.Errorf("parse config: %w", err)
	}
	if root == nil {
		root = make(map[string]interface{})
	}

	// Update (or create) the auth section.
	authMap, _ := root["auth"].(map[string]interface{})
	if authMap == nil {
		authMap = make(map[string]interface{})
	}
	// Preserve existing username; default to "admin" if not set.
	if _, ok := authMap["username"]; !ok {
		authMap["username"] = "admin"
	}
	authMap["password_hash"] = hash
	root["auth"] = authMap

	updated, err := yaml.Marshal(root)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := atomicWriteConfig(r.configPath, updated); err != nil {
		return err
	}

	r.activeConfigYAML = updated
	return nil
}

// atomicWriteConfig writes data to a temp file in the same directory as dst,
// sets permissions to 0600, then renames it into place.
func atomicWriteConfig(dst string, data []byte) error {
	dir := filepath.Dir(dst)
	tmpFile, err := os.CreateTemp(dir, "config.*.tmp")
	if err != nil {
		return fmt.Errorf("create temp config file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, werr := tmpFile.Write(data); werr != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write temp config file: %w", werr)
	}
	if cerr := tmpFile.Close(); cerr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close temp config file: %w", cerr)
	}
	if cherr := os.Chmod(tmpPath, 0600); cherr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("chmod temp config file: %w", cherr)
	}
	if rerr := os.Rename(tmpPath, dst); rerr != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename config file: %w", rerr)
	}
	return nil
}
