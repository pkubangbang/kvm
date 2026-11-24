//go:build linux

package main

// X11 override-redirect renderer — ONE STATIC FULL-SCREEN WINDOW, depth 24,
// shaped every tick by the X Shape extension.
//
// The crosshair is an overlay: two 2-pixel red lines that live inside a single
// window covering the whole screen. The window is created once on the default
// (depth-24) root visual and then NEVER moves, never resizes and never
// re-maps. What changes each tick is the window's BOUNDING shape and the two
// arm rectangles painted inside it.
//
// Why this and not ARGB:
//
//   - A depth-32 ARGB window with per-pixel alpha composites correctly only
//     when the compositor (mutter on XWayland) honours the alpha channel;
//     under mutter-on-XWayland the same pixel stream ended up as an opaque
//     black canvas, hiding the desktop, and the "erase with alpha 0" trick
//     never actually erased anything.
//   - XShape bounding is honoured by every X server and by every compositor:
//     pixels OUTSIDE the bounding region are not drawn and not composited,
//     giving real transparency without any alpha channel. The desktop shows
//     through naturally.
//
// Why this is cheap:
//
//   - The bounding region is just two thin rectangles (screen-w x 2, 2 x
//     screen-h). Mutter's compositor only has to re-validate that tiny region
//     each tick, not a full-screen ARGB plane. No full-screen damage, no
//     full-screen repaint, no window move.
//   - Per update we issue exactly: one ShapeRectangles(SoSet, SkBounding, ...)
//     with the two new arm rects, one PolyFillRectangle with the same two
//     rects, and nothing else. No Sync, no Check on the hot path.
//
// Earlier designs and why they failed (kept for the record):
//
//  1. Depth-32 ARGB canvas, erase-by-alpha-0: opaque black + accumulating
//     arms + laggy. See the three-defect report.
//  2. Depth-24 with full-screen refill + Sync every tick: smooth arms but the
//     compositor stalled because every tick was a full-screen damage.
//  3. Small shaped window moved with ConfigureWindow: only shows arms in a
//     small box, not full-screen.
//  4. Two thin strip windows moved: XWayland decorated them as title bars.
//  5. Depth-24 with ParentRelative: opaque black, no real transparency.
//
// This design keeps the window completely stationary AND genuinely
// see-through: pixels outside the two arms are not part of the window at all.

import (
	"fmt"
	"math"
	"os"

	"github.com/jezek/xgb"
	"github.com/jezek/xgb/shape"
	"github.com/jezek/xgb/xproto"
)

const (
	// armThickness is the width of each arm, in pixels.
	armThickness = 2
	// armPixel is an opaque red in the depth-24 TrueColor default visual:
	// 0x00RRGGBB in the high 24 bits of the pixel word.
	armPixel = 0x00FF2A2A
)

type x11Renderer struct {
	c    *xgb.Conn
	wid  xproto.Window
	gc   xproto.Gcontext
	w, h int

	havePrev bool
	prevX    int
	prevY    int
}

func newX11Renderer() (renderer, error) {
	if os.Getenv("DISPLAY") == "" {
		return nil, fmt.Errorf("DISPLAY is empty: the x11 mode needs XWayland (or an Xorg session); " +
			"use -mode window instead")
	}
	c, err := xgb.NewConnDisplay(os.Getenv("DISPLAY"))
	if err != nil {
		return nil, fmt.Errorf("connect to %s: %w", os.Getenv("DISPLAY"), err)
	}
	// From here on every early return must release the connection, or the
	// process leaks one X11 socket AND xgb's four background goroutines
	// (generateXIds, generateSeqIds, sendRequests, readResponses) for good.
	// owned flips to false once the renderer takes responsibility for c.
	owned := true
	defer func() {
		if owned {
			c.Close()
		}
	}()
	if err := shape.Init(c); err != nil {
		return nil, fmt.Errorf("init the X shape extension: %w", err)
	}
	setup := xproto.Setup(c)
	if setup == nil || len(setup.Roots) == 0 {
		return nil, fmt.Errorf("X server reported no screens")
	}
	if int(c.DefaultScreen) >= len(setup.Roots) {
		return nil, fmt.Errorf("X server default screen %d is out of range (%d screens)",
			c.DefaultScreen, len(setup.Roots))
	}
	screen := setup.DefaultScreen(c)
	w, h := int(screen.WidthInPixels), int(screen.HeightInPixels)

	wid, err := xproto.NewWindowId(c)
	if err != nil {
		return nil, err
	}
	// Depth-24 window on the default root visual: no colormap games, no ARGB.
	// override-redirect so mutter does not manage/decorate it, no background
	// pixmap (we paint the two arms ourselves; pixels outside the bounding
	// shape are not visible at all).
	//
	// Parent MUST be the screen ROOT in the top-level Xwayland connection,
	// not the connection's own root (which inside Xwayland may be a
	// mutter-x11-frames wrapper):
	//   - as a direct root child the window is an unmanaged OR window at
	//     exactly +0+0, so window coords == pointer coords (no 50px
	//     decoration offset that made the arms miss the pointer);
	//   - the frame wrapper around app windows would otherwise own the input
	//     region and swallow clicks, so click-through would never work.
	root := screen.Root
	// The value list below MUST be in ascending bit order of the mask
	// (X11 CreateWindow: least-significant bit first). xgb writes the value
	// list verbatim in caller order, so a list in the "obvious" reading order
	// silently assigns the wrong values: with the old order the server read
	// back-pixel=1, border-pixel=armPixel and override-redirect=0, i.e. the
	// window got a near-black background pixel and was NOT override-redirect
	// (so mutter managed it) — which is exactly why the crosshair came up
	// black while every constant looked right.
	//   CwBackPixel(2) < CwBorderPixel(8) < CwOverrideRedirect(512) < CwEventMask(2048)
	mask := uint32(xproto.CwBackPixel) |
		uint32(xproto.CwBorderPixel) |
		uint32(xproto.CwOverrideRedirect) |
		uint32(xproto.CwEventMask)
	if err := xproto.CreateWindowChecked(c,
		screen.RootDepth, wid, root,
		0, 0, uint16(w), uint16(h), 0,
		xproto.WindowClassInputOutput, screen.RootVisual,
		mask, []uint32{
			// Background = the arm colour, NOT black. The bounding region is
			// empty at map time, so the compositor may paint any exposed part
			// of the window from the background before the first Draw fills
			// the arms.
			armPixel,
			0, // border pixel
			1, // override-redirect
			xproto.EventMaskExposure,
		}).Check(); err != nil {
		return nil, fmt.Errorf("create the crosshair window: %w", err)
	}

	// Ask to be treated as a dock (above normal windows in mutter's stacking
	// model) and explicitly above, with no decorations. The peer probed this
	// host: an override-redirect window IS present in XQueryTree on root (the
	// `xwininfo -root -tree` view hides it because it has no WM_STATE), and
	// these hints are what mutter reads when it decides the layer.
	setWindowHints(c, wid)

	gc, err := xproto.NewGcontextId(c)
	if err != nil {
		return nil, err
	}
	if err := xproto.CreateGCChecked(c, gc, xproto.Drawable(wid),
		uint32(xproto.GcForeground), []uint32{armPixel}).Check(); err != nil {
		return nil, fmt.Errorf("create the arm GC: %w", err)
	}

	r := &x11Renderer{c: c, wid: wid, gc: gc, w: w, h: h}

	// The overlay is a display-only layer: no pixel of it may take input.
	// Setting the input shape to the empty region makes every click fall
	// through to whatever is below, regardless of where the arms are.
	if err := shape.RectanglesChecked(c, shape.SoSet, shape.SkInput, 0, wid, 0, 0,
		[]xproto.Rectangle{}).Check(); err != nil {
		return nil, fmt.Errorf("clear the input region: %w", err)
	}

	// Start with an EMPTY bounding region so nothing of the full-screen
	// canvas shows until the first Draw. (Without this, mapping a full-screen
	// depth-24 window would briefly flash an opaque rectangle.)
	if err := shape.RectanglesChecked(c, shape.SoSet, shape.SkBounding, 0, wid, 0, 0,
		[]xproto.Rectangle{}).Check(); err != nil {
		return nil, fmt.Errorf("clear the bounding region: %w", err)
	}

	if err := xproto.MapWindowChecked(c, wid).Check(); err != nil {
		return nil, fmt.Errorf("map the crosshair: %w", err)
	}
	// One round trip at startup so we know the window is mapped before the
	// caller starts issuing per-tick updates.
	c.Sync()

	// Keep ourselves on top: every time another client maps, moves or raises a
	// window we are pushed down the X stacking order, so subscribe to root's
	// substructure notifications and re-raise on each one. This is one X
	// request per external window event — not per frame — and it is the
	// canonical mechanism a WM itself would use.
	go restackLoop(c, root, wid)
	owned = false // the renderer's Close() now owns c
	return r, nil
}

// setWindowHints publishes the "always on top, no decoration" hints:
// _NET_WM_WINDOW_TYPE_DOCK + _NET_WM_STATE{ABOVE, SKIP_TASKBAR, SKIP_PAGER} and
// _MOTIF_WM_HINTS with decorations disabled.
func setWindowHints(c *xgb.Conn, wid xproto.Window) {
	set := func(name string, typ string, format byte, data []byte) {
		prop, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
		if err != nil || prop == nil {
			return
		}
		t, err := xproto.InternAtom(c, false, uint16(len(typ)), typ).Reply()
		if err != nil || t == nil {
			return
		}
		xproto.ChangeProperty(c, xproto.PropModeReplace, wid, prop.Atom, t.Atom, format,
			uint32(len(data)/(int(format)/8)), data)
	}
	atom := func(name string) xproto.Atom {
		r, err := xproto.InternAtom(c, false, uint16(len(name)), name).Reply()
		if err != nil || r == nil {
			return 0
		}
		return r.Atom
	}
	// _NET_WM_WINDOW_TYPE = _NET_WM_WINDOW_TYPE_DOCK
	if t := atom("_NET_WM_WINDOW_TYPE"); t != 0 {
		if d := atom("_NET_WM_WINDOW_TYPE_DOCK"); d != 0 {
			xproto.ChangeProperty(c, xproto.PropModeReplace, wid, t, xproto.AtomAtom, 32, 1,
				[]byte{byte(d), byte(d >> 8), byte(d >> 16), byte(d >> 24)})
		}
	}
	// _NET_WM_STATE = { ABOVE, SKIP_TASKBAR, SKIP_PAGER }
	if st := atom("_NET_WM_STATE"); st != 0 {
		var vals []byte
		for _, n := range []string{"_NET_WM_STATE_ABOVE", "_NET_WM_STATE_SKIP_TASKBAR", "_NET_WM_STATE_SKIP_PAGER"} {
			if a := atom(n); a != 0 {
				vals = append(vals, byte(a), byte(a>>8), byte(a>>16), byte(a>>24))
			}
		}
		if len(vals) > 0 {
			xproto.ChangeProperty(c, xproto.PropModeReplace, wid, st, xproto.AtomAtom, 32,
				uint32(len(vals)/4), vals)
		}
	}
	// _MOTIF_WM_HINTS: flags = DECORATIONS (2), decorations = 0.
	if m := atom("_MOTIF_WM_HINTS"); m != 0 {
		xproto.ChangeProperty(c, xproto.PropModeReplace, wid, m, m, 32, 5,
			[]byte{
				2, 0, 0, 0, // flags: decorations
				0, 0, 0, 0, // functions
				0, 0, 0, 0, // decorations: none
				0, 0, 0, 0, // input mode
				0, 0, 0, 0, // status
			})
	}
	_ = set
}

// restackLoop keeps wid on top of the X stacking order.
//
// Xwayland composites clients in the X server's stacking order, so raising our
// window is what puts the crosshair above application windows. Anything the
// compositor keeps above X clients (the GNOME panel, OSDs) stays above us —
// that is the Shell's own layer, not something an X client can address.
func restackLoop(c *xgb.Conn, root, wid xproto.Window) {
	// SubstructureNotify ONLY. Adding SubstructureRedirect would ask to be the
	// window manager for every map request on this display — an infinite
	// "re-raise on every event" loop that makes the whole session sluggish while
	// the crosshair is running (measured: the canvas got slower and slower as
	// strokes were drawn). SubstructureNotify bounds the loop by real external
	// events.
	if err := xproto.ChangeWindowAttributesChecked(c, root,
		uint32(xproto.CwEventMask),
		[]uint32{xproto.EventMaskSubstructureNotify}).Check(); err != nil {
		return
	}
	// Raise only when we are NOT already topmost. An unconditional raise on
	// every event feeds itself — our own ConfigureNotify is an external event to
	// the next iteration — and a self-feeding raise loop is exactly what makes a
	// session feel slower and slower. Ported from the overlay package, which
	// measured this; the two implementations must not drift apart again.
	raiseIfNeeded := func() {
		rep, err := xproto.QueryTree(c, root).Reply()
		if err != nil {
			return
		}
		if n := len(rep.Children); n > 0 && rep.Children[n-1] == wid {
			return // already topmost
		}
		xproto.ConfigureWindow(c, wid,
			uint16(xproto.ConfigWindowStackMode), []uint32{xproto.StackModeAbove})
	}
	raiseIfNeeded()
	for {
		ev, err := c.WaitForEvent()
		// xgb signals a closed connection by returning (nil, nil), NOT an
		// error: readResponses closes eventChan and a receive on a closed
		// channel never blocks, so checking only `err != nil` turns this loop
		// into a permanent busy-spin at 100% CPU once the conn is closed.
		if err != nil || ev == nil {
			return
		}
		switch e := ev.(type) {
		case xproto.MapNotifyEvent:
			if e.Window != wid {
				raiseIfNeeded()
			}
		case xproto.ConfigureNotifyEvent:
			if e.Window != wid {
				raiseIfNeeded()
			}
		case xproto.CirculateNotifyEvent:
			if e.Window != wid {
				raiseIfNeeded()
			}
		}
	}
}

// Draw repaints the crosshair arms through (x, y). It reshapes the window's
// bounding region to the two new arm rectangles and fills exactly those two
// rectangles with red. Pixels outside the bounding region are not drawn, so
// the previous arms disappear on their own — no explicit erase step.
func (r *x11Renderer) Draw(x, y int) error {
	// Clamp so the arms always lie on-screen.
	if x < 0 {
		x = 0
	}
	if x > r.w-armThickness {
		x = r.w - armThickness
	}
	if y < 0 {
		y = 0
	}
	if y > r.h-armThickness {
		y = r.h - armThickness
	}
	if r.havePrev && x == r.prevX && y == r.prevY {
		return nil
	}

	// X11 rectangles carry int16 coordinates, so anything past 32767 would wrap
	// NEGATIVE and draw the arm at the opposite edge instead of at the pointer.
	// The clamp above is against the screen size, which is normally far below
	// the limit, but a virtual root wider/taller than int16 is representable in
	// X and would silently mis-draw. Clamp to the protocol range as well.
	rect := func(v int) int16 {
		if v > math.MaxInt16 {
			return math.MaxInt16
		}
		if v < math.MinInt16 {
			return math.MinInt16
		}
		return int16(v)
	}
	// Widths/heights are u16 by protocol; the arm length is the screen edge, so
	// the same reasoning applies in the other direction.
	span := func(v int) uint16 {
		if v > math.MaxUint16 {
			return math.MaxUint16
		}
		if v < 0 {
			return 0
		}
		return uint16(v)
	}

	arms := []xproto.Rectangle{
		{X: 0, Y: rect(y), Width: span(r.w), Height: armThickness},
		{X: rect(x), Y: 0, Width: armThickness, Height: span(r.h)},
	}

	// SoSet replaces the bounding region wholesale: old arms vanish because
	// they are no longer part of the window's visible region. Unsorted order
	// (0) is fine for two disjoint rects.
	shape.Rectangles(r.c, shape.SoSet, shape.SkBounding, 0, r.wid, 0, 0, arms)

	// Fill only the two arm rectangles. No full-screen fill, no clear of the
	// previous arms: the new bounding region already excludes them.
	xproto.PolyFillRectangle(r.c, xproto.Drawable(r.wid), r.gc, arms)

	r.prevX, r.prevY, r.havePrev = x, y, true
	return nil
}

func (r *x11Renderer) Describe() string {
	return fmt.Sprintf("x11 static %dx%d depth-24 shaped overlay, two %dpx arms, window never "+
		"moves, per-tick SkBounding = two arm rects, fill those rects only",
		r.w, r.h, armThickness)
}

func (r *x11Renderer) Close() {
	_ = xproto.DestroyWindowChecked(r.c, r.wid).Check()
	xproto.FreeGC(r.c, r.gc)
	r.c.Close()
}
