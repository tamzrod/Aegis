// cmd/aegis/snapshot.go — data transformation domain
// Responsibility: constructing and updating the StatusSnapshot from PollResult
// and TransportCounters values.
// These functions are pure transforms: they accept a snapshot by value, return
// an updated copy, and report whether any field actually changed.
package main

import (
	"github.com/tamzrod/Aegis/internal/engine"
)

// applyPollResult derives health and error fields from res and writes them
// into a copy of snap.
// Returns the updated snapshot and whether any field changed.
func applyPollResult(snap engine.StatusSnapshot, res engine.PollResult) (engine.StatusSnapshot, bool) {
	changed := false

	if res.Err == nil {
		if snap.Health != engine.HealthOK {
			snap.Health = engine.HealthOK
			changed = true
		}
		if snap.LastErrorCode != 0 {
			snap.LastErrorCode = 0
			changed = true
		}
		if snap.SecondsInError != 0 {
			snap.SecondsInError = 0
			changed = true
		}
	} else {
		if snap.Health != engine.HealthError {
			snap.Health = engine.HealthError
			changed = true
		}
		code := engine.ErrorCode(res.Err)
		if snap.LastErrorCode != code {
			snap.LastErrorCode = code
			changed = true
		}
	}

	return snap, changed
}

// applyCounters syncs transport counter fields from c into a copy of snap.
// Returns the updated snapshot and whether any field changed.
func applyCounters(snap engine.StatusSnapshot, c engine.TransportCounters) (engine.StatusSnapshot, bool) {
	changed := false

	if snap.RequestsTotal != c.RequestsTotal {
		snap.RequestsTotal = c.RequestsTotal
		changed = true
	}
	if snap.ResponsesValidTotal != c.ResponsesValidTotal {
		snap.ResponsesValidTotal = c.ResponsesValidTotal
		changed = true
	}
	if snap.TimeoutsTotal != c.TimeoutsTotal {
		snap.TimeoutsTotal = c.TimeoutsTotal
		changed = true
	}
	if snap.TransportErrorsTotal != c.TransportErrorsTotal {
		snap.TransportErrorsTotal = c.TransportErrorsTotal
		changed = true
	}
	if snap.ConsecutiveFailCurr != c.ConsecutiveFailCurr {
		snap.ConsecutiveFailCurr = c.ConsecutiveFailCurr
		changed = true
	}
	if snap.ConsecutiveFailMax != c.ConsecutiveFailMax {
		snap.ConsecutiveFailMax = c.ConsecutiveFailMax
		changed = true
	}

	return snap, changed
}
