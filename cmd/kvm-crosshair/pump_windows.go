//go:build windows

package main

// The message pump.
//
// This is the piece whose absence hung the first overlay: the proc addresses
// were bound but never called, so the window's queue filled and Windows marked
// the process "Not Responding" (a window that will not answer WM_NULL is
// unresponsive by definition). The overlay itself was drawing correctly the
// whole time — the OS had simply given up asking it anything, and the layered
// surface it had already blitted stayed on screen looking like a black sheet.
//
// The rule: whoever creates a window on a thread MUST drain that thread's queue,
// even when the window does nothing with its messages.

import "unsafe"

// msg mirrors the Win32 MSG struct. The fields are only ever written by
// PeekMessageW and read by DispatchMessageW; the pump never inspects them, so
// the layout only has to be big enough and correctly aligned.
type msg struct {
	hwnd    uintptr
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	// POINT (two LONGs) plus a DWORD message filter, then alignment padding.
	ptX, ptY int32
	lPrivate uint32
}

// pump drains every pending message for this thread and dispatches it.
//
// It returns false when a WM_QUIT was seen, which is the signal for the
// render loop to stop — the loop must not spin on a dead window.
func pump() (alive bool) {
	var m msg
	for {
		// PeekMessageW(hwnd=nil, filterMin=0, filterMax=0, PM_REMOVE) services the
		// whole thread queue, which is what a single-window daemon wants.
		ret, _, _ := procPeekMessageW.Call(
			uintptr(unsafe.Pointer(&m)), 0, 0, 0, uintptr(pmRemove))
		if ret == 0 {
			return true // queue empty
		}
		if m.message == wmQuit {
			return false
		}
		procTranslateMessage.Call(uintptr(unsafe.Pointer(&m)))
		procDispatchMessageW.Call(uintptr(unsafe.Pointer(&m)))
	}
}
