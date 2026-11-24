package ch9329

// The control layer: everything that turns "move the pointer" into something a
// human would call pointer motion.
//
// Design notes that are load-bearing, not decoration:
//
//   * Absolute, not relative, for destination ("goto"). The 0x04 report maps
//     pixel-exactly onto this host's screen (measured: 6/6 targets within 1 px
//     once data[0] carried the mandatory 0x02 marker). It needs exactly one
//     frame, so no accumulation error, no dependence on Windows pointer
//     ballistics, and its destination is independent of where the pointer
//     happens to be - which matters because the pointer may be anywhere after
//     the user touched the real mouse.
//
//   * The host applies the 0x04 report asynchronously. Reading the cursor 60 ms
//     after the write can still return the PREVIOUS position on a loaded
//     desktop. So the readback is "wait until it stops changing", anchored on
//     the last known position: a stale reading equals that anchor and is
//     therefore never mistaken for the result.
//
//   * Smoothness is a path, not a jump. Glide walks a straight line in small
//     absolute steps (or relative nudges when asked) spaced by a fixed frame
//     gap, which is what the eye reads as a hand moving rather than a cursor
//     teleporting. Each step is a real HID report the host sees.

import (
	"fmt"
	"math/rand"
	"time"
)

// Point is a pixel coordinate on the primary screen.
type Point struct{ X, Y int }

func (p Point) String() string { return fmt.Sprintf("(%d,%d)", p.X, p.Y) }

// Sub returns p - q.
func (p Point) Sub(q Point) Point { return Point{p.X - q.X, p.Y - q.Y} }

// Len returns the Euclidean length in pixels.
func (p Point) Len() float64 {
	dx, dy := float64(p.X), float64(p.Y)
	return sqrt(dx*dx + dy*dy)
}

func sqrt(v float64) float64 {
	// Newton's method: avoids pulling in math just for one call and is exact
	// enough for pixel distances.
	if v <= 0 {
		return 0
	}
	x := v
	for i := 0; i < 40; i++ {
		nx := 0.5 * (x + v/x)
		if nx == x {
			break
		}
		x = nx
	}
	return x
}

// AbsGoto moves the pointer to a pixel coordinate with one absolute report and
// verifies it in the same process.
//
// The chip's grid is 4096x4096 over the whole screen, so the achievable target
// resolution is screen/4096: on a 1920x1080 screen one grid step is 0.47 px
// horizontally and 0.26 px vertically, i.e. sub-pixel - anything claimed below
// that is measurement noise, not accuracy.
//
// The readback loop tolerates the host's asynchronous application of the
// report: it waits until the cursor has stopped moving AND at least one frame
// gap has passed since the write, then reports the settled position together
// with the residual against the request. It never retries silently - the caller
// sees the actual landing point.
func (d *Device) AbsGoto(p Point, settle time.Duration) (Point, error) {
	w, h := ScreenSize()
	return d.AbsGotoOn(p, w, h, settle)
}

// AbsGotoOn is AbsGoto with an explicit screen geometry (used by tests and by
// callers that already know the screen size).
func (d *Device) AbsGotoOn(p Point, w, h int, settle time.Duration) (Point, error) {
	return d.absGotoOnBtn(p, w, h, settle, 0)
}

// absGotoOnBtn is AbsGotoOn carrying a button mask, so a drag can land exactly
// without lifting the button on the way.
func (d *Device) absGotoOnBtn(p Point, w, h int, settle time.Duration, btn byte) (Point, error) {
	px, py, _ := CursorPoint()
	prev := Point{px, py}
	gx, gy := PixelsToChip(p.X, p.Y, w, h)
	if _, err := d.send(MouseAbsFrame(btn, gx, gy, 0)); err != nil {
		return prev, err
	}
	deadline := time.Now().Add(settle)
	if settle <= 0 {
		deadline = time.Now().Add(120 * time.Millisecond)
	}
	last := prev
	stableFor := 0
	for time.Now().Before(deadline) {
		time.Sleep(12 * time.Millisecond)
		cx, cy, err := CursorPoint()
		if err != nil {
			return last, err
		}
		cur := Point{cx, cy}
		if cur == last {
			stableFor++
			// Two quiet samples after at least one frame gap means the host has
			// finished applying the report. The initial sample is the pre-move
			// anchor, so stableFor only accumulates after a real settle.
			if stableFor >= 2 {
				return cur, nil
			}
		} else {
			stableFor = 0
		}
		last = cur
	}
	return last, nil
}

// RelNudge sends one relative report and returns where the pointer actually
// ended up. Relative motion on this host is subject to the OS acceleration
// curve, so the delivered delta is NOT the requested one - the caller must use
// the returned point (this is why closed-loop motion, not dead reckoning, is
// the only way to hit a target with 0x05).
func (d *Device) RelNudge(dx, dy int, wait time.Duration) (Point, error) {
	return d.RelNudgeBtn(0, dx, dy, wait)
}

// RelNudgeBtn is RelNudge with an explicit button mask.
//
// THE BUG THIS EXISTS TO KILL: RelNudge used to call MouseRelFrame(0, ...) - a
// hard zero in the button byte. Every relative report sent during a "drag" was
// therefore a button-UP report: the pointer moved while no button was held, the
// slider handle never grabbed, and the widget (correctly) refused to verify.
// The absolute frames looked fine because Drag presses with MouseDown first, so
// only the in-between reports were wrong - a silent, perfectly self-consistent
// trace that ACKed 100% of the time. Same failure mode as the 0x02 marker bug:
// the chip cannot tell you the payload is semantically wrong.
func (d *Device) RelNudgeBtn(btn byte, dx, dy int, wait time.Duration) (Point, error) {
	if _, err := d.send(MouseRelFrame(btn, dx, dy, 0)); err != nil {
		return Point{}, err
	}
	if wait <= 0 {
		wait = 25 * time.Millisecond
	}
	time.Sleep(wait)
	x, y, err := CursorPoint()
	return Point{x, y}, err
}

// GlideOptions shapes a smooth pointer move.
type GlideOptions struct {
	// Duration is the wall-clock time the move should take. Zero means
	// "compute from Distance with Speed".
	Duration time.Duration
	// Speed in pixels per second used when Duration is zero (default 1400).
	Speed float64
	// MaxStep clamps the per-step pixel distance (default 18 px). Small steps
	// are what make motion look like a trackpoint rather than a teleport; too
	// small and the 20 ms frame gap makes the move crawl.
	MaxStep float64
	// FrameGap is the spacing between reports (default FrameGap = 20 ms).
	FrameGap time.Duration
	// Mode selects the report used per step.
	Mode GlideMode
	// MinDuration is the floor for extremely short moves (default 60 ms).
	MinDuration time.Duration
	// Rate is the minimum spacing between reports during a DRAG. Human capture
	// (sigcap.exe, 2026-11-19) measured 173 pointer events over 2872 ms = 60
	// reports/s, ~17 ms apart. The old default (FrameGap = 20 ms, 18 px steps)
	// produced SIX reports for a 300 px drag, each a 18 px teleport: to the page
	// - and to any behaviour heuristic - that is not a drag at all. When Buttons
	// is non-zero the step size is reduced to what a human hand actually emits
	// per report (~2 px) and the frame spacing defaults to the measured cadence.
	Rate time.Duration
	// Buttons is the mouse-button mask carried by EVERY report of the glide.
	// Zero (the default) is a plain move. Drag sets it so the whole path is a
	// hold: a glide that drops the button mid-path - or ends with an absolute
	// frame at buttons=0 - releases the handle and no slider will ever verify.
	Buttons byte
	// Human shapes the path (never the landing point) when Enabled is set. See
	// human.go: the tremor envelope is zero at both ends and the final frame is
	// an exact absolute report, so a graded landing point does not move.
	Human HumanOptions
}

// GlideMode picks which HID report carries each step of a glide.
type GlideMode int

const (
	// GlideRelative nudges with 0x05 reports and re-reads the cursor after each
	// step, steering toward the target (closed loop). This is the mode that
	// actually looks like a hand: every step is a small relative movement.
	GlideRelative GlideMode = iota
	// GlideAbsolute steps the absolute 0x04 report along the line. Deterministic
	// and pixel-exact, but the host treats repeated absolute jumps as teleports
	// and some applications coalesce them.
	GlideAbsolute
)

func (m GlideMode) String() string {
	if m == GlideAbsolute {
		return "absolute"
	}
	return "relative"
}

// GlideReport is one completed smooth move.
type GlideReport struct {
	From, To, Target Point
	Steps            int
	Took             time.Duration
	Residual         float64 // remaining distance to Target at the end, in px
}

func (r GlideReport) String() string {
	return fmt.Sprintf("glide %s -> %s (target %s) steps=%d took=%s residual=%.1fpx",
		r.From, r.To, r.Target, r.Steps, r.Took.Round(time.Millisecond), r.Residual)
}

// Glide moves the pointer from where it is to target along a straight line in a
// sequence of small HID steps, and returns the measured outcome.
//
// The motion is eased slightly (a smoothstep on step size: short steps at the
// ends, longer in the middle) because constant-velocity motion reads as
// mechanical. Every step is a genuine report the host sees, spaced by
// FrameGap, so a 400 px move takes roughly 0.3-0.5 s at the default settings -
// the same order as a finger flick on a trackpoint.
func (d *Device) Glide(target Point, o GlideOptions) (GlideReport, error) {
	w, h := ScreenSize()
	return d.GlideOn(target, w, h, o)
}

// GlideOn is Glide with an explicit screen geometry.
func (d *Device) GlideOn(target Point, w, h int, o GlideOptions) (GlideReport, error) {
	if o.Speed <= 0 {
		o.Speed = 1400
	}
	if o.MaxStep <= 0 {
		o.MaxStep = 18
	}
	if o.FrameGap <= 0 {
		o.FrameGap = FrameGap
	}
	if o.MinDuration <= 0 {
		o.MinDuration = 60 * time.Millisecond
	}
	if o.Buttons != 0 {
		// Drag profile: many tiny reports. A human emits ~2 px per 16 ms report.
		if o.Rate <= 0 {
			o.Rate = 16 * time.Millisecond
		}
		if o.MaxStep <= 0 || o.MaxStep > 3 {
			o.MaxStep = 2
		}
		if o.FrameGap <= 0 || o.FrameGap > o.Rate {
			o.FrameGap = o.Rate
		}
	}
	start := time.Now()
	fx, fy, err := CursorPoint()
	if err != nil {
		return GlideReport{Target: target}, err
	}
	cur := Point{fx, fy}
	delta := target.Sub(cur)
	dist := delta.Len()
	if dist < 1 {
		return GlideReport{From: cur, To: cur, Target: target, Residual: dist}, nil
	}

	steps := int(dist / o.MaxStep)
	if steps < 1 {
		steps = 1
	}
	// The duration only shapes pacing for the absolute mode (relative steps are
	// paced by FrameGap because each one needs a readback); keep the two modes'
	// step counts independent of it.
	total := o.Duration
	if total <= 0 {
		total = time.Duration(dist / o.Speed * float64(time.Second))
	}
	if total < o.MinDuration {
		total = o.MinDuration
	}

	report := GlideReport{From: cur, Target: target}
	// A human glide draws its tremor phases once per gesture, so the wobble is
	// coherent along the path instead of re-randomising at every frame.
	human := o.Human.WithDefaults()
	var rng *rand.Rand
	var phase, phase2 float64
	if human.Enabled {
		rng = human.rng()
		phase = rng.Float64() * 2
		phase2 = rng.Float64() * 2
	}
	switch o.Mode {
	case GlideAbsolute:
		for i := 1; i <= steps; i++ {
			f := float64(i) / float64(steps)
			p := Point{
				X: cur.X + int(float64(delta.X)*f),
				Y: cur.Y + int(float64(delta.Y)*f),
			}
			if human.Enabled {
				p = human.path(cur, target, eased(f, human.Ease), phase, phase2)
			}
			gx, gy := PixelsToChip(p.X, p.Y, w, h)
			if _, err := d.send(MouseAbsFrame(o.Buttons, gx, gy, 0)); err != nil {
				return report, err
			}
			report.Steps++
			report.To = p
			gap := o.FrameGap
			if human.Enabled {
				gap = human.nextGap(rng, o.FrameGap)
			}
			time.Sleep(gap)
		}
	default: // GlideRelative
		// A relative move of `dist` takes about `steps` nudge rounds. Windows
		// scales small deltas (a single dx=1 frequently delivers 0 px), so each
		// round is re-aimed from the MEASURED position rather than from the
		// requested one; the loop stops when close enough or when the budget of
		// rounds runs out, and reports the shortfall.
		budget := steps*3 + 20
		prevErr := dist
		stalled := 0
		for round := 0; round < budget; round++ {
			cur = report.To
			rem := target.Sub(cur)
			left := rem.Len()
			if left < 1.5 {
				break
			}
			step := o.MaxStep
			if left < step {
				step = left
			}
			// Ease: shrink the first and last few rounds.
			if float64(round) < float64(steps)*0.25 {
				step *= 0.5 + 0.5*(float64(round)/(float64(steps)*0.25))
			}
			sx := int(step * float64(rem.X) / left)
			sy := int(step * float64(rem.Y) / left)
			if sx == 0 && rem.X != 0 {
				if rem.X > 0 {
					sx = 1
				} else {
					sx = -1
				}
			}
			if sy == 0 && rem.Y != 0 {
				if rem.Y > 0 {
					sy = 1
				} else {
					sy = -1
				}
			}
			np, err := d.RelNudgeBtn(o.Buttons, sx, sy, o.FrameGap)
			if err != nil {
				return report, err
			}
			report.Steps++
			report.To = np
			if np == cur {
				stalled++
				if stalled > 12 {
					break // the OS is swallowing deltas; stop lying about progress
				}
			} else {
				stalled = 0
			}
			// Guard against overshoot oscillation: never aim past the target.
			if np.Sub(target).Len() > prevErr+2 && round > 4 {
				break
			}
			prevErr = np.Sub(target).Len()
			if total > 0 && time.Since(start) > total*3 {
				break
			}
		}
	}

	// Final convergence: absolute is exact, so one 0x04 finishes the job. Doing
	// this for every mode means "glide" ends on the requested pixel, which is
	// what a caller typing `--x 900 --y 400` expects.
	land, err := d.absGotoOnBtn(target, w, h, 200*time.Millisecond, o.Buttons)
	if err != nil {
		return report, err
	}
	report.To = land
	report.Took = time.Since(start)
	report.Residual = report.To.Sub(target).Len()
	return report, nil
}

// ModeNeedsSettle reports whether a glide in this mode should finish with an
// absolute frame. It always should: the point of the option is where the
// pointer ends, and only 0x04 puts it on an exact pixel.
func (r GlideReport) ModeNeedsSettle(GlideMode) bool { return true }

// Drag presses a button at from, glides to to with it held, and releases it.
//
// This is the primitive slider-style CAPTCHAs need: a sustained press, a
// human-shaped path, and a release at the far end. The steps are deliberately
// small and uneven (a small random walk perpendicular to the line) because a
// perfectly straight constant-speed drag is the classic bot fingerprint.
func (d *Device) Drag(from, to Point, btn byte, o GlideOptions) (GlideReport, error) {
	w, h := ScreenSize()
	if _, err := d.AbsGotoOn(from, w, h, 200*time.Millisecond); err != nil {
		return GlideReport{Target: to}, err
	}
	start := time.Now()
	gx, gy := PixelsToChip(from.X, from.Y, w, h)
	if err := d.MouseDown(btn, gx, gy); err != nil {
		return GlideReport{Target: to}, err
	}
	// Give the target time to arm its drag handler before the pointer moves.
	time.Sleep(60 * time.Millisecond)
	o.Buttons |= btn // hold it through every frame of the path, absolute or relative
	rep, err := d.GlideOn(to, w, h, o)
	if err != nil {
		return rep, err
	}
	// Glide's own landing frame carries o.Buttons, so the press survives it.
	// Releasing here is the first and only button-up report of the gesture.
	// Release at the measured landing point, not the requested one.
	ex, ey := PixelsToChip(rep.To.X, rep.To.Y, w, h)
	if err := d.MouseUp(ex, ey); err != nil {
		return rep, err
	}
	rep.From = from
	rep.Took = time.Since(start)
	return rep, nil
}

// RepeatOptions describes a repeated input: N presses with spacing.
type RepeatOptions struct {
	Count int
	Every time.Duration
}

// Slide moves the pointer along a human-shaped (or plain) path with `btn` held
// for the whole path, and releases at the measured landing point.
//
// This is the primitive a slider CAPTCHA actually needs: one sustained press,
// N reports of small steps, one release. Two properties are load-bearing:
//
//  1. EVERY report on the path carries the button mask. An absolute 0x04 report
//     whose button byte is 0 releases the press, so a path built from plain
//     "goto" steps is seen by the page as pointerdown followed by button-up
//     moves: the handle never grabs, translateX stays 0, and the chip ACKs every
//     frame the whole time. That is the bug this function exists to make
//     impossible - the mask is a parameter of the frame, not a separate call.
//
//  2. The path is where the randomness goes; the LANDING POINT is not random.
//     The final frame is an exact absolute report at `to`, so the release
//     coordinate is the requested one whatever the tremor drew.
//
// The step count is taken from the wall-clock duration and the nominal rate, so
// `-duration 2500ms -rate 16ms` is ~156 reports - the cadence a hand produces.
func (d *Device) Slide(from, to Point, opts SlideOptions) (SlideReport, error) {
	w, h := ScreenSize()
	return d.SlideOn(from, to, opts, w, h)
}

// SlideOn is Slide with an explicit screen geometry.
func (d *Device) SlideOn(from, to Point, opts SlideOptions, w, h int) (SlideReport, error) {
	if opts.Button == 0 {
		opts.Button = BtnLeft
	}
	if opts.Duration <= 0 {
		opts.Duration = 2500 * time.Millisecond
	}
	if opts.Rate <= 0 {
		opts.Rate = 16 * time.Millisecond
	}
	human := opts.Human.WithDefaults()

	start := time.Now()
	if _, err := d.AbsGotoOn(from, w, h, 150*time.Millisecond); err != nil {
		return SlideReport{}, err
	}
	gx, gy := PixelsToChip(from.X, from.Y, w, h)
	if err := d.MouseDown(opts.Button, gx, gy); err != nil {
		return SlideReport{}, err
	}
	// Let the target arm its drag handler before anything moves. A widget that
	// arms on pointerdown and the next frame arrives in the same millisecond can
	// legitimately decide the gesture was a click.
	time.Sleep(60 * time.Millisecond)

	steps := int(opts.Duration / opts.Rate)
	if steps < 2 {
		steps = 2
	}
	rep := SlideReport{From: from, Target: to}

	// A ticker cannot express jittered spacing, so the human path waits out each
	// drawn gap; the plain path keeps the exact frame clock it always had.
	if !human.Enabled {
		tick := time.NewTicker(opts.Rate)
		defer tick.Stop()
		for i := 1; i <= steps; i++ {
			<-tick.C
			p := human.path(from, to, float64(i)/float64(steps), 0, 0)
			if err := d.slideFrame(opts.Button, p, w, h); err != nil {
				return rep, err
			}
			rep.Steps++
			rep.To = p
		}
	} else {
		rng := human.rng()
		phase := rng.Float64() * 2
		phase2 := rng.Float64() * 2
		for i := 1; i <= steps; i++ {
			time.Sleep(human.nextGap(rng, opts.Rate))
			f := eased(float64(i)/float64(steps), human.Ease)
			p := human.path(from, to, f, phase, phase2)
			if i == steps {
				// The last path frame is the destination itself: the tremor
				// envelope is zero at f=1 and easing maps 1 to 1, so this is
				// exact, and it leaves the release coordinate non-random.
				p = to
			}
			if err := d.slideFrame(opts.Button, p, w, h); err != nil {
				return rep, err
			}
			rep.Steps++
			rep.To = p
		}
	}

	// Release at the measured landing point, not the requested one: the OS is
	// the only witness worth trusting here.
	lx, ly, err := CursorPoint()
	if err != nil {
		return rep, err
	}
	land := Point{lx, ly}
	ex, ey := PixelsToChip(land.X, land.Y, w, h)
	if err := d.MouseUp(ex, ey); err != nil {
		return rep, err
	}
	rep.Landed = land
	rep.Took = time.Since(start)
	rep.Residual = land.Sub(to).Len()
	return rep, nil
}

// slideFrame sends one absolute step of a held path: a zero relative report
// (keeps the host's motion pipeline warm) followed by the absolute report that
// carries both the position and the button mask.
func (d *Device) slideFrame(btn byte, p Point, w, h int) error {
	px, py := PixelsToChip(p.X, p.Y, w, h)
	if err := d.MouseRelRaw(btn, 0, 0); err != nil {
		return err
	}
	_, err := d.MouseAbsHeld(btn, px, py)
	return err
}

// SlideOptions shapes a Slide gesture.
type SlideOptions struct {
	// Button is the mask held for the whole path (default BtnLeft).
	Button byte
	// Duration is the wall-clock time the path should take (default 2.5 s).
	Duration time.Duration
	// Rate is the nominal spacing between reports (default 16 ms, ~62/s).
	Rate time.Duration
	// Human turns on tremor, jittered timing, easing and hesitation.
	Human HumanOptions
}

// SlideReport is one completed held gesture.
type SlideReport struct {
	From, To, Target, Landed Point
	Steps                    int
	Took                     time.Duration
	Residual                 float64 // distance from Landed to Target, px
}

func (r SlideReport) String() string {
	return fmt.Sprintf("slide %s -> %s (target %s) steps=%d took=%s residual=%.1fpx",
		r.From, r.Landed, r.Target, r.Steps, r.Took.Round(time.Millisecond), r.Residual)
}

// RepeatKey presses the same key Count times, Every apart.
func (d *Device) RepeatKey(usage, mods byte, o RepeatOptions) error {
	if o.Count < 1 {
		o.Count = 1
	}
	if o.Every <= 0 {
		o.Every = PressGap
	}
	for i := 0; i < o.Count; i++ {
		if err := d.KeyPress(usage, mods); err != nil {
			return err
		}
		if i != o.Count-1 {
			time.Sleep(o.Every)
		}
	}
	return nil
}

// RepeatClick clicks at the current position Count times, Every apart. The
// absolute report needs a coordinate with every button event, so the position
// is passed in.
func (d *Device) RepeatClick(at Point, btn byte, o RepeatOptions, hold time.Duration) error {
	if o.Count < 1 {
		o.Count = 1
	}
	if o.Every <= 0 {
		o.Every = PressGap
	}
	w, h := ScreenSize()
	gx, gy := PixelsToChip(at.X, at.Y, w, h)
	for i := 0; i < o.Count; i++ {
		if err := d.Click(btn, gx, gy, hold); err != nil {
			return err
		}
		if i != o.Count-1 {
			time.Sleep(o.Every)
		}
	}
	return nil
}

// MouseRelRaw sends one relative report and returns after the reply, with no
// readback, no settle and no extra sleep: the pure cost of one HID frame.
func (d *Device) MouseRelRaw(btn byte, dx, dy int) error {
	_, err := d.send(MouseRelFrame(btn, dx, dy, 0))
	return err
}

// MouseAbsHeld sends one absolute report with a button mask held and waits for the
// chip's reply. Used by the slide gesture so every step carries the press.
func (d *Device) MouseAbsHeld(btn byte, gx, gy int) (Reply, error) {
	return d.send(MouseAbsFrame(btn, gx, gy, 0))
}
