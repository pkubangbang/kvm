package ch9329

import "strings"

// Hex renders bytes as uppercase space-separated hex, the form used in the
// CH9329DS manual and in every frame quote in this repo's notes.
func Hex(b []byte) string {
	var sb strings.Builder
	for i, x := range b {
		if i > 0 {
			sb.WriteByte(' ')
		}
		const hexdigits = "0123456789ABCDEF"
		sb.WriteByte(hexdigits[x>>4])
		sb.WriteByte(hexdigits[x&0x0F])
	}
	return sb.String()
}
