//go:build windows

package ch9329

// Pointer readback - the feedback channel.
//
// The previous attempt at this driver failed because nothing ever checked
// whether an injected report had any effect: an ACK from the chip only proves
// the frame parsed, and the chip happily ACKs a report the host then throws
// away. GetCursorPos is the cheapest trustworthy witness: it cannot go stale
// the way a logged event list can, and it is readable in the same process that
// sent the frame, which removes the cross-process staleness trap.

import (
	"syscall"
	"unsafe"
)

// CursorPoint returns the current pointer position in screen pixels.
func CursorPoint() (int, int, error) {
	var p point
	ok, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		if err == nil {
			err = syscall.EINVAL
		}
		return 0, 0, err
	}
	return int(p.X), int(p.Y), nil
}

// ButtonState asks Windows whether a virtual key is currently down. For the
// mouse buttons this is the only witness that separates "the press never
// reached the host" from "the press landed somewhere that ignores it": an
// application cannot tell those apart, because both look like nothing
// happened. VK_LBUTTON = 0x01, VK_RBUTTON = 0x02, VK_MBUTTON = 0x04.
func ButtonState(vk uintptr) (uintptr, uintptr, error) {
	v, _, err := procGetAsyncKeyState.Call(vk)
	return v, 0, err
}
