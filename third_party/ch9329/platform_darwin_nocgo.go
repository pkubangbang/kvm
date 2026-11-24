//go:build darwin && !cgo

package ch9329

// Non-cgo darwin fallback. platform_darwin.go (the real CoreGraphics witness)
// is cgo-only and is dropped when CGO_ENABLED=0 — e.g. a pure cross-compile
// from Linux/CI with `GOOS=darwin CGO_ENABLED=0 go build ./...`. Without this
// file that build fails on missing symbols (CursorPoint, ScreenSize, ...).
//
// So we keep the same honest contract as platform_other.go: the witness FAILS
// LOUDLY (ErrUnsupported) rather than returning a plausible zero that would let
// every motion primitive "succeed" against a fiction. A real macOS build with
// cgo enabled uses platform_darwin.go and gets the working witness instead.

// CursorPoint is unavailable without cgo; the witness must not fake a reading.
func CursorPoint() (int, int, error) { return 0, 0, ErrUnsupported }

// ScreenSize reports a nominal size so flag parsing and dry-run paths work. Any
// command that would send a frame fails on the witness first.
func ScreenSize() (int, int) { return 1920, 1080 }

// SetCursor is unavailable without cgo; selftest's control condition cannot run.
func SetCursor(x, y int) {}

// ButtonState is unavailable without cgo.
func ButtonState(vk uintptr) (uintptr, uintptr, error) { return 0, 0, ErrUnsupported }

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
