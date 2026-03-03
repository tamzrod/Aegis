// internal/core/state_sealing.go
package core

// StateSealingDef describes where the sealing flag lives in memory.
// Semantics:
//
//	0 = sealed
//	1 = unsealed
type StateSealingDef struct {
	Area    Area
	Address uint16
}

// SetStateSealing attaches a state sealing definition to this memory.
// Metadata only — no behavior is enforced here.
func (m *Memory) SetStateSealing(def StateSealingDef) {
	m.stateSealing = &def
}

// StateSealing returns the sealing definition, if present.
func (m *Memory) StateSealing() *StateSealingDef {
	return m.stateSealing
}
