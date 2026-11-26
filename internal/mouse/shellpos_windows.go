//go:build windows

package mouse

// Windows shell pointer witness.
//
// This is the twin of shellpos_linux.go: on Linux the position comes from the
// GNOME extension's shared-memory ring, on Windows the OS already owns it, so a
// live point sample from GetCursorPos is the same thing without a publisher.
//
// It matters that this returns the TRACKED pointer position and not the chip's
// internal cursor: the overlay is a consumer of the same authority every motion
// primitive trusts, so the crosshair can never disagree with the tool about
// where the pointer is.

import (
	"syscall"
	"time"
	"unsafe"

	"github.com/pkubangbang/kvm/internal/cursor"
)

var procGetCursorPos = syscall.NewLazyDLL("user32.dll").NewProc("GetCursorPos")

type winPoint struct{ X, Y int32 }

// ShellPoint returns the pointer position in virtual-screen coordinates.
func ShellPoint() (int, int, bool) {
	var p winPoint
	ok, _, _ := procGetCursorPos.Call(uintptr(unsafe.Pointer(&p)))
	if ok == 0 {
		return 0, 0, false
	}
	return int(p.X), int(p.Y), true
}

// WaitPointer samples immediately: the position is live, there is no frame to
// wait for. It exists so callers written against the Linux contract compile.
func WaitPointer(timeout time.Duration) (int, int, bool) { return ShellPoint() }

// RecentPointer has no history on this platform.
func RecentPointer(within time.Duration) []cursor.Point { return nil }

// ClosePointerFeed releases the feed, if one was opened. Windows has none.
func ClosePointerFeed() {}

// WirePoint returns the live pointer sample, matching the Linux shape.
func WirePoint(maxAge time.Duration) (int, int, bool) { return ShellPoint() }

// hostPoint is the host's own reading of the pointer — the last resort in
// PointerPoint when nothing has been commanded yet. On Windows that is simply
// GetCursorPos: there is no separate "frozen under XWayland" reading to prefer
// over it, so it is the same call as ShellPoint.
func hostPoint() (int, int, bool) { return ShellPoint() }
