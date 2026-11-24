//go:build darwin

package ch9329

// macOS platform layer: the pointer witness, over CoreGraphics (cgo).
//
// This implements the contract in platform_other.go: CursorPoint / ScreenSize /
// SetCursor / ButtonState are the feedback witness every motion primitive in
// ctl.go trusts. The window helpers are stubbed (usability, not correctness),
// exactly as on Linux and Windows.
//
// # The mapping (PORTING.md)
//
//	CursorPoint  -> CGEventGetLocation(CGEventCreate(nil))
//	SetCursor    -> CGWarpMouseCursorPosition
//	ScreenSize   -> CGDisplayBounds(CGMainDisplayID())
//	ButtonState  -> CGEventSourceButtonState
//
// # Coordinate space (flipped Y)
//
// CoreGraphics' global display space has its origin at the TOP-LEFT of the
// main display and Y grows DOWNWARD (Quartz/Cartesian-ish screen space). This
// is the same convention PixelsToChip maps onto the 0x04 grid (pixel (0,0) is
// top-left, Y down), so CGEventGetLocation's value is used verbatim — do NOT
// flip Y here. (AppKit's NSEvent.mouseLocation IS flipped, origin bottom-left;
// that is the trap PORTING.md warns about. We avoid AppKit entirely and read
// CGEventGetLocation directly, so no flip is required.)
//
// # Accessibility gate (injection, not readback)
//
// Reading the pointer (CGEventGetLocation) needs no permission. Injecting HID
// reports through the chip is done in hardware and also needs none — but if a
// future port were to synthesize events via CGEventPost it would require the
// Accessibility permission (AXIsProcessTrustedWithOptions) or injection would
// silently do nothing. SetCursor uses CGWarpMouseCursorPosition, which moves
// the cursor WITHOUT generating events and does not require that trust.

/*
#cgo LDFLAGS: -framework CoreGraphics -framework ApplicationServices
#include <ApplicationServices/ApplicationServices.h>
#include <stdlib.h>

// cursorPoint reads the real pointer position via a transient CGEvent.
static void ch_cursorPoint(int *x, int *y) {
    CGEventRef ev = CGEventCreate(NULL);
    CGPoint p = CGEventGetLocation(ev);   // global display space, origin top-left
    CFRelease(ev);
    *x = (int)p.x;
    *y = (int)p.y;
}

// ch_screenSize returns the main display bounds (the 0x04 grid maps onto the
// full desktop; on a single display this is its pixel size).
static void ch_screenSize(int *w, int *h) {
    CGRect b = CGDisplayBounds(CGMainDisplayID());
    *w = (int)b.size.width;
    *h = (int)b.size.height;
}

// ch_setCursor warps the pointer without generating events (selftest control).
static void ch_setCursor(int x, int y) {
    CGWarpMouseCursorPosition(CGPointMake((CGFloat)x, (CGFloat)y));
}

// ch_buttonState reports whether a mouse button is currently down. vk follows
// the Windows convention: 0x01 left, 0x02 right, 0x04 middle. Returns 1/0.
static int ch_buttonState(unsigned long vk) {
    CGMouseButton btn;
    switch (vk) {
        case 0x01: btn = kCGMouseButtonLeft;   break;
        case 0x02: btn = kCGMouseButtonRight;  break;
        case 0x04: btn = kCGMouseButtonCenter; break;
        default:   return -1;
    }
    if (CGEventSourceButtonState(kCGEventSourceStateHIDSystemState, btn)) {
        return 1;
    }
    return 0;
}
*/
import "C"

// CursorPoint returns the real pointer position in global display pixels
// (origin top-left, Y down) — the same space PixelsToChip maps onto. This is
// the feedback witness; it is read fresh every call, never cached.
func CursorPoint() (int, int, error) {
	var x, y C.int
	C.ch_cursorPoint(&x, &y)
	return int(x), int(y), nil
}

// ScreenSize reports the coordinate space the 0x04 grid maps onto.
func ScreenSize() (int, int) {
	var w, h C.int
	C.ch_screenSize(&w, &h)
	if int(w) <= 0 || int(h) <= 0 {
		return 1920, 1080
	}
	return int(w), int(h)
}

// SetCursor warps the pointer without the chip. Used only by selftest's control
// condition to prove the witness works at all.
func SetCursor(x, y int) { C.ch_setCursor(C.int(x), C.int(y)) }

// ButtonState reports whether a mouse button is currently down. VK codes match
// the Windows build: left=0x01, right=0x02, middle=0x04. The return mirrors the
// Windows signature (value, 0, err); the value is 1 when down, 0 otherwise, and
// ErrUnsupported for an unknown VK.
func ButtonState(vk uintptr) (uintptr, uintptr, error) {
	v := C.ch_buttonState(C.ulong(vk))
	if v < 0 {
		return 0, 0, ErrUnsupported
	}
	return uintptr(v), 0, nil
}

// ---------------------------------------------------------------------------
// Window helpers. Stubbed on macOS exactly as on Linux: `win`/`focus` are
// usability, not correctness. Focus reports failure rather than claiming
// success (the one non-negotiable behaviour).
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
