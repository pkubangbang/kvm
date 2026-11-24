//go:build !windows && !linux && !darwin

package ch9329

// Non-Windows platform layer.
//
// This file exists so the package CROSS-COMPILES cleanly everywhere, and so a
// port is a matter of replacing stubs rather than untangling build errors. The
// serial protocol, frame encoding, report layout and all motion shaping in
// ctl.go / protocol.go / reports.go / human.go are already platform-neutral.
//
// What you must implement to light up a new OS is exactly this file.
//
// # The contract
//
//   - CursorPoint() must return the REAL pointer position in the SAME
//     coordinate space that PixelsToChip maps onto the 0x04 grid, readable from
//     the same process that sent the frame. This is the feedback witness: the
//     whole package's accuracy claims rest on it. Do not return a cached or
//     requested value.
//   - ScreenSize() must return the same bounds ScreenSize reports on Windows —
//     the full virtual desktop if the OS composes multiple monitors into one
//     coordinate space.
//   - SetCursor() is used only by selftest as a control condition (it moves the
//     pointer WITHOUT the chip, to prove the witness works at all). Implement it
//     with the native API, not by sending frames.
//   - ButtonState() separates "the press never reached the host" from "the press
//     landed somewhere that ignores it". VK codes: left=0x01, right=0x02,
//     middle=0x04. On Linux, XQueryPointer's mask is the natural source.
//
// The window helpers (ListWindows, FindWindow, Foreground, Focus, Restore,
// ClientOrigin, ClientSize, TitleContains, WindowRect) back the `win` and
// `focus` CLI commands. Stubbing them is fine — that degrades usability, not
// correctness — but see the note on Focus below.
//
// # X11 sketch
//
//	XQueryPointer   -> CursorPoint, ButtonState
//	XDisplayWidth/Height -> ScreenSize
//	XWarpPointer    -> SetCursor
//	XSetInputFocus + XRaiseWindow (via _NET_ACTIVE_WINDOW EWMH) -> Focus
//
// Import github.com/jezek/xgb or use cgo with libX11. Note that under XWayland
// XQueryPointer reports only the X server's idea of the pointer, which may not
// match the compositor's — the witness must be the same stack that receives the
// injected HID reports, or the readback is meaningless.
//
// # Wayland caveat (important)
//
// Wayland deliberately denies clients the ability to read the global pointer
// and to inject it: for a Wayland client there is no global cursor position at
// all, and no warp. A CH9329 bridge does not inject "from a client" though — it
// is a hardware USB device, so the compositor delivers its events exactly like a
// real mouse. Reading the position back is the hard half. Options are libinput
// (read events from /dev/input, but that is event deltas, not absolute position,
// and requires privileges) or a compositor-specific query. Either way, the
// witness becomes a different stack from the one that consumed the event, which
// reintroduces precisely the staleness trap GetCursorPos avoids on Windows.
// Treat this as the central design question of the Linux port, not a detail.

// errUnsupported is the platform-locale error value; see ErrUnsupported.
func errUnsupported() error { return ErrUnsupported }

// CursorPoint returns ErrUnsupported on this platform.
func CursorPoint() (int, int, error) { return 0, 0, errUnsupported() }

// ScreenSize reports a nominal size so flag parsing and dry-run paths work.
// It is NOT authoritative: any command that would send a frame must fail on the
// witness first, which it does, because CursorPoint is unavailable.
func ScreenSize() (int, int) { return 1920, 1080 }

// SetCursor is unavailable; selftest's control condition cannot run here.
func SetCursor(x, y int) {}

// ButtonState returns ErrUnsupported on this platform.
func ButtonState(vk uintptr) (uintptr, uintptr, error) { return 0, 0, errUnsupported() }

// Window is a top-level visible window. Kept identical to the Windows type so
// callers compile unchanged.
type Window struct {
	Hwnd  uintptr
	Title string
	PID   uint32
}

// rect mirrors the Windows rectangle so WindowRect keeps one signature.
type rect struct{ Left, Top, Right, Bottom int32 }

// String renders a window for the `win` command.
func (w Window) String() string { return w.Title }

// ListWindows returns no windows on this platform.
func ListWindows() []Window { return nil }

// FindWindow never finds anything on this platform.
func FindWindow(substr string) (Window, bool) { return Window{}, false }

// TitleContains is pure string logic and is platform-independent.
func TitleContains(haystack, needle string) bool { return containsFold(haystack, lower(needle)) }

// Restore is a no-op on this platform.
func Restore(h uintptr) {}

// Focus cannot focus a window on this platform. It reports failure rather than
// claiming success: a controller that silently believes it focused something is
// worse than one that says it cannot.
func Focus(h uintptr) (uintptr, bool) { return 0, false }

// Foreground reports no foreground window on this platform.
func Foreground() (uintptr, string) { return 0, "" }

// WindowRect returns an empty rectangle on this platform.
func WindowRect(h uintptr) rect { return rect{} }

// ClientOrigin returns a zero origin on this platform.
func ClientOrigin(h uintptr) (int, int) { return 0, 0 }

// ClientSize returns a zero size on this platform.
func ClientSize(h uintptr) (int, int) { return 0, 0 }

// lower is a tiny ASCII lower-caser, kept local so this file needs no imports
// beyond the package itself.
func lower(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 'a' - 'A'
		}
	}
	return string(b)
}

// containsFold reports whether haystack contains needle, ignoring ASCII case.
func containsFold(haystack, lowerNeedle string) bool {
	h := lower(haystack)
	n := len(lowerNeedle)
	if n == 0 {
		return true
	}
	for i := 0; i+n <= len(h); i++ {
		if h[i:i+n] == lowerNeedle {
			return true
		}
	}
	return false
}
