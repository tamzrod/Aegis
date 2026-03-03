// internal/core/errors.go
package core

import "errors"

var (
	ErrUnknownMemoryID = errors.New("unknown memory id")
	ErrAreaNotDefined  = errors.New("area not defined")
	ErrInvalidArea     = errors.New("invalid area")

	ErrCountZero     = errors.New("count must be > 0")
	ErrDstTooSmall   = errors.New("destination buffer too small")
	ErrSrcTooSmall   = errors.New("source buffer too small")
	ErrOutOfBounds   = errors.New("out of bounds")
	ErrStartOverflow = errors.New("start + size overflow")
	ErrSizeZero      = errors.New("size must be > 0")

	ErrNilMemory   = errors.New("nil memory")
	ErrEmptyPort   = errors.New("port must be non-empty")
	ErrUnitIDZero  = errors.New("unit id must be > 0")
	ErrNilStore    = errors.New("nil store")
)
