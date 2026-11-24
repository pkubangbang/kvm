// Package ch9329 drives a CH9329 serial-to-HID bridge as a keyboard and a mouse.
//
// # The one rule
//
// The chip ACKs frames the host never applies. A successful serial reply proves
// the bridge parsed the frame, not that the operating system acted on it. A
// different process reading the pointer can also see a stale position. So every
// movement primitive reads the pointer back through the platform layer, in the
// same process that sent the frame, and reports what the OS actually says.
//
// # Platform layer
//
// The serial protocol, frame layout, report encoding and all motion shaping are
// platform-independent. Only two things are not, and they are isolated behind
// the functions declared in platform.go and implemented per OS:
//
//   - CursorPoint  — read the real pointer position (the feedback witness)
//   - ScreenSize   — the coordinate space the 0x04 report is mapped onto
//
// plus the optional window-management helpers. Windows is implemented
// (pointer_windows.go, window_windows.go). Every other platform builds against
// platform_other.go, which returns ErrUnsupported from the witness so a move
// fails loudly instead of silently lying about where the pointer is.
//
// Porting to a new OS means implementing platform_other.go's counterparts:
// on X11, XQueryPointer and XDisplayWidth/Height; on Wayland, there is no
// portable way to both inject and read the pointer, so a compositor-specific
// path (or a uinput + libinput split) is required.
package ch9329

import "errors"

// ErrUnsupported is returned by platform primitives that the current build
// target does not implement. It is deliberately loud: a driver that cannot see
// the pointer cannot prove that anything it sent had an effect, and pretending
// otherwise is the exact failure mode this package exists to prevent.
var ErrUnsupported = errors.New("ch9329: pointer witness is not implemented on this platform (see platform_other.go)")
