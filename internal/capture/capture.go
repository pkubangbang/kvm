// Package capture takes a screenshot of the screen.
//
// It is an INDEPENDENT capability, on purpose: nothing in the crosshair overlay
// or the input path knows how to grab pixels, and this package knows nothing
// about them. That is what lets each platform grow its own implementation
// (XGetImage on X11, BitBlt on Windows, CGWindowListCreateImage on macOS)
// without touching the rest of the tool.
package capture

import (
	"fmt"
	"image"
)

// Capturer grabs the whole primary screen.
type Capturer interface {
	// Screen returns the screen contents as an RGBA image.
	Screen() (*image.RGBA, error)
	// Describe names the implementation, for logs.
	Describe() string
	// Close releases the platform resources (on X11: the X11 connection).
	// It must be safe to call more than once, so a deferred Close after a
	// failed Screen() cannot double-free the connection.
	Close()
}

// Unsupported is the capturer for a platform without an implementation yet.
type Unsupported struct{}

func (Unsupported) Describe() string { return "capture: unsupported on this platform" }

func (Unsupported) Close() {}

func (Unsupported) Screen() (*image.RGBA, error) {
	return nil, fmt.Errorf("screen capture is not implemented on this platform yet")
}
