// cmd/aegis/health.go — state mutation domain
// Responsibility: per-read-block health state updates.
// This file owns all mutations to the blockHealth map that tracks
// the liveness of each configured Modbus read block.
package main

import (
	"time"

	"github.com/tamzrod/Aegis/internal/engine"
)

// updateBlockHealth applies a single BlockUpdate to the existing health record h,
// using at as the event timestamp.
// It returns the updated health record; the caller is responsible for storing it.
func updateBlockHealth(h engine.ReadBlockHealth, upd engine.BlockUpdate, at time.Time) engine.ReadBlockHealth {
	if upd.Success {
		h.Timeout = false
		h.ConsecutiveErrors = 0
		h.LastExceptionCode = 0
		h.LastSuccess = at
	} else {
		h.ConsecutiveErrors++
		h.LastError = at
		if upd.Timeout {
			h.Timeout = true
			h.LastExceptionCode = 0
		} else {
			h.Timeout = false
			h.LastExceptionCode = upd.ExceptionCode
		}
	}
	return h
}
