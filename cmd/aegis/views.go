// cmd/aegis/views.go — RuntimeView and ConfigView implementations for the WebUI adapter.
// These types satisfy the view.RuntimeView and view.ConfigView interfaces using
// data captured at startup. They are read-only and safe for concurrent use.
package main

import "time"

// runtimeView satisfies view.RuntimeView with startup-captured static state.
type runtimeView struct {
	startTime      time.Time
	deviceCount    int
	readBlockCount int
}

func (r *runtimeView) StartTime() time.Time { return r.startTime }
func (r *runtimeView) DeviceCount() int     { return r.deviceCount }
func (r *runtimeView) ReadBlockCount() int  { return r.readBlockCount }

// configView satisfies view.ConfigView with the raw YAML bytes read at startup.
type configView struct {
	data []byte
}

func (c *configView) ActiveConfigYAML() []byte { return c.data }
