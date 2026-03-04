// internal/view/view.go
// Package view defines narrow read-only interfaces consumed by the WebUI HTTP adapter.
// This package must not import net/http, engine, adapter, or any other heavy package.
// It exists solely to avoid cross-layer contamination between the HTTP adapter and the core.
package view

import "time"

// RuntimeView exposes high-level runtime state for the /status endpoint.
type RuntimeView interface {
	StartTime() time.Time
	DeviceCount() int
	ReadBlockCount() int
}

// ConfigView exposes the active configuration bytes for the /config endpoint.
type ConfigView interface {
	ActiveConfigYAML() []byte
}
