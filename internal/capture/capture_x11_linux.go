//go:build linux

package capture

import (
	"fmt"
	"image"
	"sync"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/xproto"
)

// X11 captures the screen with GetImage on the root window. It works on Xorg
// sessions and on Xwayland surfaces owned by X clients; it cannot see native
// Wayland surfaces (the compositor does not expose them to X clients).
type X11 struct {
	c *xgb.Conn

	// closeOnce makes Close idempotent: xgb.Conn.Close sends on a channel, so a
	// second call after the send goroutine is gone would block forever waiting
	// for doneSend. A deferred Close plus an explicit Close is therefore not
	// safe without this guard.
	closeOnce sync.Once
}

// New returns the X11 capturer for the current DISPLAY.
func New() (Capturer, error) {
	c, err := xgb.NewConn()
	if err != nil {
		return nil, fmt.Errorf("connect to X: %w", err)
	}
	x := &X11{c: c}
	// Validate the screen table now rather than panicking on the first
	// Screen() call: xproto.Setup never returns nil, and DefaultScreen does an
	// unchecked index into Roots.
	if _, err := x.screen(); err != nil {
		x.Close()
		return nil, err
	}
	return x, nil
}

func (x *X11) Describe() string { return "capture: X11 GetImage on the root window" }

// screen resolves the default screen, converting the two ways a malformed or
// unusual setup block would otherwise panic into errors.
func (x *X11) screen() (*xproto.ScreenInfo, error) {
	setup := xproto.Setup(x.c)
	if setup == nil || len(setup.Roots) == 0 {
		return nil, fmt.Errorf("X server reported no screens")
	}
	if int(x.c.DefaultScreen) >= len(setup.Roots) {
		return nil, fmt.Errorf("X server default screen %d is out of range (%d screens)",
			x.c.DefaultScreen, len(setup.Roots))
	}
	return setup.DefaultScreen(x.c), nil
}

// Screen returns the root window contents.
func (x *X11) Screen() (*image.RGBA, error) {
	screen, err := x.screen()
	if err != nil {
		return nil, err
	}
	w, h := int(screen.WidthInPixels), int(screen.HeightInPixels)
	if w <= 0 || h <= 0 {
		return nil, fmt.Errorf("X server reports an empty screen (%dx%d)", w, h)
	}
	// Protocol limits: GetImage takes u16 width/height.
	if w > 0xffff || h > 0xffff {
		return nil, fmt.Errorf("screen %dx%d exceeds the X11 GetImage limit of %d", w, h, 0xffff)
	}
	rep, err := xproto.GetImage(x.c, xproto.ImageFormatZPixmap, xproto.Drawable(screen.Root),
		0, 0, uint16(w), uint16(h), 0xffffffff).Reply()
	if err != nil {
		return nil, err
	}
	// Each pixel must be a full 32 bits for the BGRA shuffle below. A depth-16
	// or depth-8 root would make this loop read across pixel boundaries and
	// emit half-width garbage rows, so reject it instead of guessing.
	need := w * h * 4
	if len(rep.Data) < need {
		return nil, fmt.Errorf("short X11 image: %d bytes for a %dx%d 32-bit screen (want %d)",
			len(rep.Data), w, h, need)
	}
	return decodeBGRA(rep.Data, w, h), nil
}

// decodeBGRA converts the server's little-endian 32-bit pixels to RGBA.
func decodeBGRA(data []byte, w, h int) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for i := 0; i+3 < len(data) && i/4 < w*h; i += 4 {
		p := i / 4
		img.Pix[p*4+0] = data[i+2] // R
		img.Pix[p*4+1] = data[i+1] // G
		img.Pix[p*4+2] = data[i+0] // B
		img.Pix[p*4+3] = 0xff
	}
	return img
}

// Close releases the X connection. Safe to call more than once.
func (x *X11) Close() {
	if x == nil || x.c == nil {
		return
	}
	x.closeOnce.Do(func() { x.c.Close() })
}
