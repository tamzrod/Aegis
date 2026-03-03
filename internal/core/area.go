// internal/core/area.go
package core

type Area uint8

const (
	AreaInvalid Area = 0

	AreaCoils          Area = 1
	AreaDiscreteInputs Area = 2
	AreaHoldingRegs    Area = 3
	AreaInputRegs      Area = 4
)

func (a Area) IsBitArea() bool {
	return a == AreaCoils || a == AreaDiscreteInputs
}

func (a Area) IsRegArea() bool {
	return a == AreaHoldingRegs || a == AreaInputRegs
}

func (a Area) String() string {
	switch a {
	case AreaCoils:
		return "coils"
	case AreaDiscreteInputs:
		return "discrete_inputs"
	case AreaHoldingRegs:
		return "holding_registers"
	case AreaInputRegs:
		return "input_registers"
	default:
		return "invalid"
	}
}
