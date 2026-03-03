// internal/core/layout.go
package core

type AreaLayout struct {
	Start uint16
	Size  uint16 // bits for bit-areas; registers for reg-areas
}

func (l AreaLayout) Validate() error {
	if l.Size == 0 {
		return ErrSizeZero
	}
	end := uint32(l.Start) + uint32(l.Size)
	if end > 0x10000 {
		return ErrStartOverflow
	}
	return nil
}

func (l AreaLayout) Contains(address uint16, count uint16) bool {
	if count == 0 {
		return false
	}

	start := uint32(l.Start)
	end := uint32(l.Start) + uint32(l.Size) // exclusive

	a := uint32(address)
	reqEnd := a + uint32(count)

	return a >= start && reqEnd <= end
}

func (l AreaLayout) Offset(address uint16) uint16 {
	return uint16(uint32(address) - uint32(l.Start))
}
