//go:build linux

package mouse

import (
	"os"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// Pointer relocation.
//
// The behaviour goto promises is "the pointer IS at (x,y) when this returns".
// The implementation is whatever the session can actually honour:
//
//   - Xorg: one X11 WarpPointer request. No motion reports at all.
//   - Wayland (this box: XDG_SESSION_TYPE=wayland, DISPLAY=:0 via XWayland):
//     WarpPointer only reaches XWayland's phantom pointer. Measured — after a warp
//     XQueryPointer reports the target, yet real relative motion leaves that
//     read-back unchanged, and no ink is drawn from the warped position. And
//     /dev/uinput is root-only here, so a Wayland-native virtual pointer is not
//     available. So the pointer is driven to the point with bounded relative
//     reports after over-travelling to a provable corner.
//
// Callers never choose; goto does the right thing for the session.

var (
	warpOnce sync.Once
	warpConn *xgb.Conn
	warpRoot xproto.Window
	warpErr  error
)

// isWayland reports whether the session is Wayland-backed, where X11 warps are
// not honoured by the compositor.
func isWayland() bool {
	return os.Getenv("XDG_SESSION_TYPE") == "wayland" || os.Getenv("WAYLAND_DISPLAY") != ""
}

func warpInit() (*xgb.Conn, xproto.Window, error) {
	warpOnce.Do(func() {
		display := os.Getenv("DISPLAY")
		if display == "" {
			display = ":0"
		}
		warpConn, warpErr = xgb.NewConnDisplay(display)
		if warpErr != nil {
			return
		}
		setup := xproto.Setup(warpConn)
		if setup == nil || len(setup.Roots) == 0 {
			warpErr = ErrNoDisplay
			return
		}
		warpRoot = setup.DefaultScreen(warpConn).Root
	})
	return warpConn, warpRoot, warpErr
}

// X11Available reports whether an X11 display is reachable at all.
func X11Available() bool {
	_, _, err := warpInit()
	return err == nil
}

// WarpSupported reports whether goto can place the pointer WITHOUT motion
// reports on this session (true on Xorg, false on Wayland).
func WarpSupported() bool { return !isWayland() && X11Available() }

// Dialect names the window system, for `kvm config` and error messages.
func Dialect() string {
	if isWayland() {
		return "wayland"
	}
	if X11Available() {
		return "x11"
	}
	return "none"
}

// walker is the relative controller goto falls back to where X11 warps are not
// honoured. It is the same Rel used for drawing, so there is one motion path.
type walker interface {
	Home(w, h int) error
	TravelTo(x, y int) error
}

// PlacePointer moves the pointer to screen pixel (x,y), using the warp when the
// session honours it and the relative walk otherwise.
func PlacePointer(x, y, w, h int, walk walker) error {
	if !isWayland() {
		conn, root, err := warpInit()
		if err != nil {
			return err
		}
		xproto.WarpPointer(conn, 0, root, 0, 0, 0, 0, int16(x), int16(y))
		conn.Sync() // turn the queued request into done-or-failed
		return nil
	}
	if walk == nil {
		return ErrWarpNeedsWalk
	}
	if err := walk.Home(w, h); err != nil {
		return err
	}
	return walk.TravelTo(x, y)
}
