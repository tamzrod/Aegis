// internal/engine/status_test.go
package engine

import "testing"

// TestEncodeDecodeRoundTrip verifies that DecodeStatusBlock is the inverse of encodeStatusBlock.
func TestEncodeDecodeRoundTrip(t *testing.T) {
	original := StatusSnapshot{
		Health:               HealthOK,
		LastErrorCode:        7,
		SecondsInError:       0,
		RequestsTotal:        1234,
		ResponsesValidTotal:  1200,
		TimeoutsTotal:        30,
		TransportErrorsTotal: 4,
		ConsecutiveFailCurr:  0,
		ConsecutiveFailMax:   3,
	}

	regs := encodeStatusBlock(original, "plc1", 0)
	got := DecodeStatusBlock(regs)

	if got.Health != original.Health {
		t.Errorf("Health: want %d, got %d", original.Health, got.Health)
	}
	if got.LastErrorCode != original.LastErrorCode {
		t.Errorf("LastErrorCode: want %d, got %d", original.LastErrorCode, got.LastErrorCode)
	}
	if got.SecondsInError != original.SecondsInError {
		t.Errorf("SecondsInError: want %d, got %d", original.SecondsInError, got.SecondsInError)
	}
	if got.RequestsTotal != original.RequestsTotal {
		t.Errorf("RequestsTotal: want %d, got %d", original.RequestsTotal, got.RequestsTotal)
	}
	if got.ResponsesValidTotal != original.ResponsesValidTotal {
		t.Errorf("ResponsesValidTotal: want %d, got %d", original.ResponsesValidTotal, got.ResponsesValidTotal)
	}
	if got.TimeoutsTotal != original.TimeoutsTotal {
		t.Errorf("TimeoutsTotal: want %d, got %d", original.TimeoutsTotal, got.TimeoutsTotal)
	}
	if got.TransportErrorsTotal != original.TransportErrorsTotal {
		t.Errorf("TransportErrorsTotal: want %d, got %d", original.TransportErrorsTotal, got.TransportErrorsTotal)
	}
	if got.ConsecutiveFailCurr != original.ConsecutiveFailCurr {
		t.Errorf("ConsecutiveFailCurr: want %d, got %d", original.ConsecutiveFailCurr, got.ConsecutiveFailCurr)
	}
	if got.ConsecutiveFailMax != original.ConsecutiveFailMax {
		t.Errorf("ConsecutiveFailMax: want %d, got %d", original.ConsecutiveFailMax, got.ConsecutiveFailMax)
	}
}

// TestDecodeStatusBlockEmpty verifies that DecodeStatusBlock returns a zero
// StatusSnapshot when given a too-short register slice.
func TestDecodeStatusBlockEmpty(t *testing.T) {
	got := DecodeStatusBlock(nil)
	if got != (StatusSnapshot{}) {
		t.Errorf("expected zero snapshot, got %+v", got)
	}
	got = DecodeStatusBlock(make([]uint16, 5))
	if got != (StatusSnapshot{}) {
		t.Errorf("expected zero snapshot for short slice, got %+v", got)
	}
}
