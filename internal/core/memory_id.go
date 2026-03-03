// internal/core/memory_id.go
package core

// MemoryID uniquely identifies a memory instance within the store.
// Architectural rule (LOCKED):
//
//	MemoryID = (Port:uint16, UnitID:uint16)
type MemoryID struct {
	Port   uint16
	UnitID uint16
}

func (id MemoryID) Validate() error {
	if id.Port == 0 {
		return ErrEmptyPort
	}
	if id.UnitID == 0 {
		return ErrUnitIDZero
	}
	return nil
}
