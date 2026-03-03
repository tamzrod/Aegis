// internal/core/bits.go
package core

// bytesForBits returns the number of bytes required to store n bits (LSB-first).
func bytesForBits(n uint16) int {
	if n == 0 {
		return 0
	}
	return int((n + 7) / 8)
}

// copyBits copies packed bit data from src into dst (LSB-first).
// srcStart is the starting bit offset in src.
// count is the number of bits to copy.
// Destination always starts at bit 0.
func copyBits(dst []byte, src []byte, srcStart uint16, count uint16) {
	for i := uint16(0); i < count; i++ {
		srcBit := srcStart + i
		dstBit := i

		srcByte := srcBit / 8
		srcMask := byte(1 << (srcBit % 8))

		dstByte := dstBit / 8
		dstMask := byte(1 << (dstBit % 8))

		if int(srcByte) >= len(src) || int(dstByte) >= len(dst) {
			return
		}

		if src[srcByte]&srcMask != 0 {
			dst[dstByte] |= dstMask
		} else {
			dst[dstByte] &^= dstMask
		}
	}
}

// writeBits writes packed bits from src into dst (LSB-first).
// dstStart is the starting bit offset in dst.
// count is the number of bits to write.
// Source is interpreted starting at bit 0.
func writeBits(dst []byte, dstStart uint16, count uint16, src []byte) {
	for i := uint16(0); i < count; i++ {
		dstBit := dstStart + i

		dstByte := dstBit / 8
		dstMask := byte(1 << (dstBit % 8))

		srcByte := i / 8
		srcMask := byte(1 << (i % 8))

		if int(dstByte) >= len(dst) || int(srcByte) >= len(src) {
			return
		}

		if src[srcByte]&srcMask != 0 {
			dst[dstByte] |= dstMask
		} else {
			dst[dstByte] &^= dstMask
		}
	}
}
