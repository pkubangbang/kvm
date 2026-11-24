//go:build linux

package ch9329

// Linux platform layer: the pointer witness, over X11 via github.com/jezek/xgb
// (pure Go, no cgo).
//
// This file implements exactly the contract documented in platform_other.go:
// CursorPoint / ScreenSize / SetCursor / ButtonState are the feedback witness
// that every motion primitive in ctl.go trusts. Everything else (the window
// helpers) is intentionally minimal and lives here too so darwin and the
// remaining GOOSes keep the stubs in platform_other.go.
//
// # The mapping (Xlib equivalent -> xgb)
//
//	XQueryPointer        -> xproto.QueryPointer(root)   -> RootX/RootY, Mask
//	XDisplayWidth/Height -> Setup().DefaultScreen().Root
//	                      + ScreenInfo.WidthInPixels/HeightInPixels
//	XWarpPointer         -> xproto.WarpPointer(...)
//	button state         -> QueryPointerReply.Mask bit (KeyButMaskButton1..3)
//
// # XWayland caveat (read this before trusting a measurement)
//
// Under XWayland, XQueryPointer reports the X server's idea of the pointer,
// which the Wayland compositor may not honour. The witness MUST be the same
// input stack that receives the injected HID reports, or the readback proves
// nothing (the same class of bug as reading the cursor from a different
// process). On a pure-X session this is trustworthy; on XWayland treat a
// reading as provisional and prefer a real X session (Xorg) for verification.
// See PORTING.md for the Wayland-native (uinput) alternative.

import (
	"os"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// X11 button-mask bits in QueryPointerReply.Mask (xproto.KeyButMaskButtonN).
const (
	x11Button1Mask = 256  // left
	x11Button2Mask = 512  // middle
	x11Button3Mask = 1024 // right
)

// A single lazily-opened connection is enough: the witness is read-only
// (QueryPointer) except for SetCursor's WarpPointer, and xgb.Conn is safe for
// concurrent use. Reusing it keeps the cost out of the per-frame readback loop.
var (
	xOnce  sync.Once
	xConn  *xgb.Conn
	xRoot  xproto.Window
	xW, xH int
	xErr   error
)

// xdisplay returns the X display to connect to, defaulting like Xlib.
func xdisplay() string {
	if d := os.Getenv("DISPLAY"); d != "" {
		return d
	}
	return ":0"
}

// xinit opens the connection once and caches the root window and its size.
func xinit() (*xgb.Conn, xproto.Window, error) {
	xOnce.Do(func() {
		xConn, xErr = xgb.NewConnDisplay(xdisplay())
		if xErr != nil {
			return
		}
		setup := xproto.Setup(xConn)
		if setup == nil || len(setup.Roots) == 0 {
			xErr = ErrUnsupported
			return
		}
		screen := setup.DefaultScreen(xConn)
		xRoot = screen.Root
		xW = int(screen.WidthInPixels)
		xH = int(screen.HeightInPixels)
	})
	return xConn, xRoot, xErr
}

// CursorPoint returns the real pointer position in root-window pixels. This is
// the feedback witness: it reads the OS's current pointer in the same process
// that sends the frames, never a cached or requested value.
func CursorPoint() (int, int, error) {
	conn, root, err := xinit()
	if err != nil {
		return 0, 0, err
	}
	reply, err := xproto.QueryPointer(conn, root).Reply()
	if err != nil {
		return 0, 0, err
	}
	if reply == nil {
		return 0, 0, ErrUnsupported
	}
	return int(reply.RootX), int(reply.RootY), nil
}

// ScreenSize reports the full coordinate space the 0x04 grid maps onto: the
// root window's dimensions, which on Xinerama/RandR is the union of all
// monitors composed into one space — the same meaning as ScreenSize on Windows.
func ScreenSize() (int, int) {
	if _, _, err := xinit(); err != nil {
		return 1920, 1080
	}
	if xW > 0 && xH > 0 {
		return xW, xH
	}
	return 1920, 1080
}

// SetCursor warps the pointer without the chip. Used only by selftest as the
// control condition that proves the witness works at all.
func SetCursor(x, y int) {
	conn, root, err := xinit()
	if err != nil {
		return
	}
	// SrcWindow = None (0) with zero src rect = "warp from anywhere".
	_ = xproto.WarpPointer(conn, 0, root, 0, 0, 0, 0, int16(x), int16(y))
}

// ButtonState reports whether a mouse button is currently down. VK codes match
// the Windows build: left=0x01, right=0x02, middle=0x04. The X11 pointer mask is
// the source. This is the witness that separates "the press never reached the
// host" from "the press landed somewhere that ignores it".
//
// The return mirrors the Windows signature (value, 0, err): the value's low bit
// is 1 when the button is down, 0 otherwise (callers historically test the
// high bit of GetAsyncKeyState; on Linux they must test the low bit — see
// cmd/pointerprobe, which is windows-tagged for that reason).
func ButtonState(vk uintptr) (uintptr, uintptr, error) {
	conn, root, err := xinit()
	if err != nil {
		return 0, 0, err
	}
	reply, err := xproto.QueryPointer(conn, root).Reply()
	if err != nil {
		return 0, 0, err
	}
	if reply == nil {
		return 0, 0, ErrUnsupported
	}
	var mask uint16
	switch vk {
	case 0x01: // left
		mask = x11Button1Mask
	case 0x02: // right
		mask = x11Button3Mask
	case 0x04: // middle
		mask = x11Button2Mask
	default:
		return 0, 0, ErrUnsupported
	}
	if reply.Mask&mask != 0 {
		return 1, 0, nil
	}
	return 0, 0, nil
}

// ---------------------------------------------------------------------------
// Window helpers. X11 window management is possible but verbose; the CLI's
// `win`/`focus` commands are usability, not correctness, so they are stubbed
// here exactly as in platform_other.go. The one non-negotiable behaviour is
// that Focus reports failure rather than claiming success.
// ---------------------------------------------------------------------------

// Window is a top-level visible window. Kept identical to the Windows type.
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
// claiming success.
func Focus(h uintptr) (uintptr, bool) { return 0, false }

// Foreground reports no foreground window on this platform.
func Foreground() (uintptr, string) { return 0, "" }

// WindowRect returns an empty rectangle on this platform.
func WindowRect(h uintptr) rect { return rect{} }

// ClientOrigin returns a zero origin on this platform.
func ClientOrigin(h uintptr) (int, int) { return 0, 0 }

// ClientSize returns a zero size on this platform.
func ClientSize(h uintptr) (int, int) { return 0, 0 }

// lower is a tiny ASCII lower-caser.
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
