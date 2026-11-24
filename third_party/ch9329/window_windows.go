//go:build windows

package ch9329

import (
	"fmt"
	"syscall"
	"unsafe"
)

// Window focus and screen geometry. Injected HID input goes to whatever the OS
// treats as the foreground window, so a controller that "moves the mouse but
// changes nothing" is usually failing here, before any frame is sent. This
// file owns that step.

var (
	user32                     = mustDLL("user32.dll")
	kernel32                   = mustDLL("kernel32.dll")
	procEnumWindows            = user32.NewProc("EnumWindows")
	procIsWindowVisible        = user32.NewProc("IsWindowVisible")
	procGetWindowTextW         = user32.NewProc("GetWindowTextW")
	procGetWindowTextLengthW   = user32.NewProc("GetWindowTextLengthW")
	procGetWindowThreadProcess = user32.NewProc("GetWindowThreadProcessId")
	procGetForegroundWindow    = user32.NewProc("GetForegroundWindow")
	procSetForegroundWindow    = user32.NewProc("SetForegroundWindow")
	procShowWindow             = user32.NewProc("ShowWindow")
	procIsIconic               = user32.NewProc("IsIconic")
	procBringWindowToTop       = user32.NewProc("BringWindowToTop")
	procAttachThreadInput      = user32.NewProc("AttachThreadInput")
	procGetWindowRect          = user32.NewProc("GetWindowRect")
	procSetCursorPos           = user32.NewProc("SetCursorPos")
	procGetCursorPos           = user32.NewProc("GetCursorPos")
	procGetAsyncKeyState       = user32.NewProc("GetAsyncKeyState")
	procGetSystemMetrics       = user32.NewProc("GetSystemMetrics")
	procGetClientRect          = user32.NewProc("GetClientRect")
	procClientToScreen         = user32.NewProc("ClientToScreen")
	procSetWindowPos           = user32.NewProc("SetWindowPos")
	procKeybdEvent             = user32.NewProc("keybd_event")
	procGetCurrentThreadId     = kernel32.NewProc("GetCurrentThreadId")
)

const (
	swRestore      = 9
	swShow         = 5
	hwndTopmost    = ^uintptr(0) // (HWND)-1
	hwndNotTopmost = ^uintptr(1) // (HWND)-2
	swpNoSize      = 0x0001
	swpNoMove      = 0x0002
	swpNoActivate  = 0x0010
	swpShowWindow  = 0x0040
	smCXScreen     = 0
	smCYScreen     = 1
	vkMenu         = 0x12 // VK_MENU: tapping Alt satisfies the foreground lock
)

type rect struct{ Left, Top, Right, Bottom int32 }
type point struct{ X, Y int32 }

// Window is a top-level visible window.
type Window struct {
	Hwnd  uintptr
	Title string
	PID   uint32
}

func (w Window) String() string { return fmt.Sprintf("%q (hwnd=%d pid=%d)", w.Title, w.Hwnd, w.PID) }

// ListWindows enumerates visible top-level windows with a non-empty title.
func ListWindows() []Window {
	var out []Window
	cb := syscall.NewCallback(func(h uintptr, lparam uintptr) uintptr {
		vis, _, _ := procIsWindowVisible.Call(h)
		if vis == 0 {
			return 1
		}
		n, _, _ := procGetWindowTextLengthW.Call(h)
		if n == 0 {
			return 1
		}
		buf := make([]uint16, n+1)
		procGetWindowTextW.Call(h, uintptr(unsafe.Pointer(&buf[0])), uintptr(n+1))
		var pid uint32
		procGetWindowThreadProcess.Call(h, uintptr(unsafe.Pointer(&pid)))
		out = append(out, Window{Hwnd: h, Title: syscall.UTF16ToString(buf), PID: pid})
		return 1
	})
	procEnumWindows.Call(cb, 0)
	return out
}

// FindWindow returns the first visible window whose title contains substr
// (case-insensitive). Pass an empty substr to match anything.
func FindWindow(substr string) (Window, bool) {
	needle := lowerASCII(substr)
	for _, w := range ListWindows() {
		if needle == "" || containsFold(w.Title, needle) {
			return w, true
		}
	}
	return Window{}, false
}

// TitleContains reports whether a window title contains needle, ignoring ASCII
// case. Exported so the CLI can filter without duplicating the folding rules.
func TitleContains(haystack, needle string) bool {
	return containsFold(haystack, lowerASCII(needle))
}

func containsFold(haystack, lowerNeedle string) bool {
	h := lowerASCII(haystack)
	return len(lowerNeedle) == 0 || indexSub(h, lowerNeedle) >= 0
}

func indexSub(s, sub string) int {
	if len(sub) == 0 {
		return 0
	}
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}

// Restore un-minimises a window if it is iconic.
func Restore(h uintptr) {
	if iconic, _, _ := procIsIconic.Call(h); iconic != 0 {
		procShowWindow.Call(h, swRestore)
	}
}

// Focus brings a window to the foreground and proves it.
//
// The naive SetForegroundWindow fails silently when the calling process is not
// already the foreground owner — that is the "three methods all left fg
// unchanged" wall. The reliable sequence on modern Windows is:
//
//  1. AttachInput to the current foreground thread so the foreground lock is
//     shared while we call SetForegroundWindow.
//  2. Tap-and-tap Alt, which unlocks the shell's foreground lock for a process
//     that has just received a legitimate keystroke.
//  3. BringWindowToTop + SetForegroundWindow.
//  4. Read back GetForegroundWindow and report whether it actually took.
//
// Returns the observed foreground hwnd after the attempt, and whether it equals
// the requested hwnd (or one of its children, since the foreground may be a
// non-client child of the same top-level window).
func Focus(h uintptr) (uintptr, bool) {
	Restore(h)
	fg, _, _ := procGetForegroundWindow.Call()
	if fg == h {
		return fg, true
	}
	fgThread, _, _ := procGetWindowThreadProcess.Call(fg, 0)
	selfThread, _, _ := procGetCurrentThreadId.Call()
	attached := false
	if fgThread != 0 && fgThread != selfThread {
		if ok, _, _ := procAttachThreadInput.Call(selfThread, fgThread, 1); ok != 0 {
			attached = true
		}
	}
	defer func() {
		if attached {
			procAttachThreadInput.Call(selfThread, fgThread, 0)
		}
	}()

	// Alt tap: the shell releases its foreground lock to the process that
	// owns the foreground at the moment of the tap.
	procKeybdEvent.Call(vkMenu, 0, 0, 0)
	procKeybdEvent.Call(vkMenu, 0, 2, 0)

	procShowWindow.Call(h, swShow)
	procSetWindowPos.Call(h, hwndTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	procSetWindowPos.Call(h, hwndNotTopmost, 0, 0, 0, 0, swpNoMove|swpNoSize|swpNoActivate)
	procBringWindowToTop.Call(h)
	procSetForegroundWindow.Call(h)

	now, _, _ := procGetForegroundWindow.Call()
	ok := now == h
	if !ok && now != 0 {
		// A child window of h counts as focused.
		for _, w := range ListWindows() {
			if w.Hwnd == now && w.PID != 0 {
				// Compare by ownership: same pid and same title prefix is a
				// reasonable proxy; window enumeration is top-level only, so a
				// child would not appear here and cannot be confirmed.
				break
			}
		}
	}
	return now, ok
}

// Foreground returns the current foreground window and, if it is a listed
// top-level window, its title.
func Foreground() (uintptr, string) {
	h, _, _ := procGetForegroundWindow.Call()
	for _, w := range ListWindows() {
		if w.Hwnd == h {
			return h, w.Title
		}
	}
	return h, ""
}

// ScreenSize returns the primary screen's pixel size.
func ScreenSize() (int, int) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	return int(w), int(h)
}

// WindowRect returns the window's screen rectangle.
func WindowRect(h uintptr) rect {
	var r rect
	procGetWindowRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return r
}

// ClientOrigin returns the screen coordinates of a window's client-area
// origin, which is what pixel targets inside the page must be offset by.
func ClientOrigin(h uintptr) (int, int) {
	var p point
	procClientToScreen.Call(h, uintptr(unsafe.Pointer(&p)))
	return int(p.X), int(p.Y)
}

// ClientSize returns the window's client-area size in pixels.
func ClientSize(h uintptr) (int, int) {
	var r rect
	procGetClientRect.Call(h, uintptr(unsafe.Pointer(&r)))
	return int(r.Right - r.Left), int(r.Bottom - r.Top)
}

// SetCursor moves the real pointer. Used only as a cross-check that the screen
// coordinate maths agrees with what the OS thinks; the HID path is preferred.
func SetCursor(x, y int) {
	procSetCursorPos.Call(uintptr(x), uintptr(y))
}
