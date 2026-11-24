package mouse

import (
	"ch9329"
	"time"
)

// absSettle bounds the wait for the pointer to reach an absolute target before
// the position is read back. The chip delivers an absolute frame in one host
// event, so this only needs to outlast one compositor frame plus the read.
const absSettle = 120 * time.Millisecond

// GoTo places the pointer at (x,y), closed loop when the feed is live.
//
// It NEVER homes on its own. The old fallback called Home() when the feed had
// no position, and Home is 24 real -127,-127 reports: on any 300 ms gap in the
// observer feed that SLAMMED the user's pointer into the top-left corner and
// then failed anyway (TravelTo needs the same feed Home just proved absent).
// A placement that cannot be measured is an error to report, not a corner to
// visit: goPoint already has its own bounded relative walk when the feed is
// merely stale, and only reports ErrNoPosition when nothing can be trusted.
//
// It uses the chip's REAL absolute report (0x04, grid 4096), not a relative
// walk. Measured on this host: AbsGoto(1500,500) lands (1501,500) and
// AbsGoto(300,800) lands (301,800) — sub-pixel, deterministic, one frame.
// The relative walk this used to call could not place anything: it read the
// frame-quantised ring, converted the request into an absolute target, and then
// had the HOST apply its pointer-acceleration curve to the rapid relative
// stream it synthesised. Measured: `abs move 1500 500` landed (1920,822)
// (x pinned at the right edge, y 1.64x over), and `rel move 300 0` travelled
// +478 (1.59x) while an isolated single report travelled exactly +100. Neither
// failure exists in the absolute path, which encodes the position instead of
// accumulating deltas. Relative reports remain for STROKES, where the ink must
// be a path rather than a jump.
func (a *Abs) GoTo(x, y int) error {
	x, y = a.clamp(x, y)
	if a.rel.Held() {
		// An absolute report carries its own button mask; sending one mid-stroke
		// would replace a press this call does not own. Placement is button-up.
		return ErrAbsWhileHeld
	}
	if a.dv.d == nil {
		return ErrNoPosition
	}
	if _, err := a.dv.d.AbsGoto(ch9329.Point{X: x, Y: y}, absSettle); err != nil {
		return err
	}
	a.dv.px, a.dv.py = x, y
	noteGoto(x, y)
	return nil
}

// DragTo moves by (dx,dy) with the button held, closed loop.
func (a *Abs) DragTo(dx, dy int) error {
	cx, cy, ok := ShellPoint()
	if !ok {
		return ErrNoPosition
	}
	return a.Stroke(LinePath(cx, cy, cx+dx, cy+dy))
}

// Circle draws a closed circle, closed loop, with the button held.
func (a *Abs) Circle(cx, cy, r, n int) error {
	return a.Stroke(CirclePath(cx, cy, r, n))
}

// Press places the pointer on (x,y) and presses WITHOUT releasing.
func (a *Abs) Press(x, y int) error {
	x, y = a.clamp(x, y)
	if mx, my, ok := ShellPoint(); !ok || absOf(mx-x) > loopTolerance || absOf(my-y) > loopTolerance {
		if err := a.rel.goPoint(x, y); err != nil {
			return err
		}
	}
	if err := a.rel.Down(); err != nil {
		return err
	}
	if mx, my, ok := ShellPoint(); ok {
		a.dv.px, a.dv.py = mx, my
		noteGoto(mx, my)
	}
	return nil
}

// Leg continues the stroke pressed by Press by (dx,dy).
func (a *Abs) Leg(dx, dy int) error {
	cx, cy, ok := ShellPoint()
	if !ok {
		return ErrNoPosition
	}
	return a.StrokeFrom(cx, cy, cx+dx, cy+dy, true)
}

// Release ends the stroke with one confirmed release.
func (a *Abs) Release() error {
	if err := a.rel.Up(); err != nil {
		clearTracked()
		return err
	}
	if mx, my, ok := ShellPoint(); ok {
		a.dv.px, a.dv.py = mx, my
		noteGoto(mx, my)
	}
	return nil
}

// StrokeHeld follows a path with the button ALREADY held.
func (a *Abs) StrokeHeld(path Path) error {
	if len(path) < 2 {
		return nil
	}
	return a.followLeg(path, true)
}

// StrokeFrom follows a path with the button already held (pressOwned) or not.
func (a *Abs) StrokeFrom(ox, oy, dx, dy int, pressOwned bool) error {
	end := a.clampVals([2]int{ox + dx, oy + dy})
	return a.followLeg([][2]int{{ox, oy}, end}, pressOwned)
}

func (a *Abs) clampVals(p [2]int) [2]int {
	x, y := a.clamp(p[0], p[1])
	return [2]int{x, y}
}

// clampPath keeps EVERY waypoint inside the display. Stroke records its last
// waypoint in the tracked ledger, so a path that ran off-screen would otherwise
// write an endpoint the pointer cannot have reached (the pointer saturates at
// the edge while the ledger keeps integrating).
//
// It returns a COPY: Path is a slice, and clamping the caller's backing array
// would silently rewrite a path the caller may still be holding (LinePath and
// CirclePath both hand out slices they own).
func (a *Abs) clampPath(path Path) Path {
	out := make(Path, len(path))
	for i, p := range path {
		x, y := a.clamp(p[0], p[1])
		out[i] = [2]int{x, y}
	}
	return out
}

func (a *Abs) followLeg(path Path, pressOwned bool) error {
	if !pressOwned {
		if err := a.rel.Down(); err != nil {
			return err
		}
	}
	if err := a.rel.followPath(path); err != nil {
		// A failed leg never records an endpoint, so the tracked ledger cannot
		// claim a point the pointer may not have reached. Release only if WE
		// pressed: with pressOwned the caller owns the button and releasing it
		// here would truncate a stroke it is still drawing.
		if !pressOwned {
			a.rel.ForceRelease()
		}
		clearTracked()
		return err
	}
	return nil
}

// Stroke draws an arbitrary path with the button held.
func (a *Abs) Stroke(path Path) error {
	if len(path) == 0 {
		return nil
	}
	// Clamp the WHOLE path, not just its start: the endpoint is what the ledger
	// records below, and a path that leaves the screen would otherwise write a
	// point the pointer provably never reached.
	path = a.clampPath(path)
	if _, _, ok := ShellPoint(); !ok {
		return ErrNoPosition
	}
	start := path[0]
	if mx, my, ok := ShellPoint(); !ok {
		return ErrNoPosition
	} else if absOf(mx-start[0]) > loopTolerance || absOf(my-start[1]) > loopTolerance {
		if err := a.rel.goPoint(start[0], start[1]); err != nil {
			return err
		}
	}
	if err := a.rel.Down(); err != nil {
		return err
	}
	if err := a.rel.followPath(path); err != nil {
		// The stroke failed mid-path: release what we pressed, and never let the
		// tracked ledger claim an endpoint. followPath already walks with the
		// button held, so it may have inked part of the path; the honest record
		// is "unknown", which is what clearTracked writes (`pos` then refuses
		// instead of reporting a point the pointer may not be at).
		a.rel.ForceRelease()
		clearTracked()
		return err
	}
	if err := a.rel.Up(); err != nil {
		clearTracked()
		return err
	}
	last := path[len(path)-1]
	a.dv.px, a.dv.py = last[0], last[1]
	noteGoto(last[0], last[1])
	return nil
}

// ClickAt places the pointer at (x,y) and clicks the left button there.
func (a *Abs) ClickAt(x, y int) error {
	x, y = a.clamp(x, y)
	if err := a.GoTo(x, y); err != nil {
		return err
	}
	if err := a.rel.Down(); err != nil {
		clearTracked()
		return err
	}
	if err := a.rel.Up(); err != nil {
		clearTracked()
		return err
	}
	noteGoto(x, y)
	return nil
}

// clamp keeps a screen point inside the display.
func (a *Abs) clamp(x, y int) (int, int) {
	if x < 0 {
		x = 0
	}
	if y < 0 {
		y = 0
	}
	if a.dv.W > 0 && x >= a.dv.W {
		x = a.dv.W - 1
	}
	if a.dv.H > 0 && y >= a.dv.H {
		y = a.dv.H - 1
	}
	return x, y
}
