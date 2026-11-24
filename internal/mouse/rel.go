package mouse

import (
	"fmt"
	"time"

	"ch9329"
)

// Rel is the relative API set: signed deltas, one HID report each.
type Rel struct {
	dv       *Device
	StepPx   int
	Gap      time.Duration
	ClickGap time.Duration
	down     bool
	// io is the byte sink. nil means "use dv.d.Port"; the relative emitter goes
	// through port() so report pacing can be unit-tested without a chip.
	io portIO
}

// NewRel builds the relative controller.
func NewRel(dv *Device, stepPx int, gap, clickGap time.Duration) *Rel {
	return &Rel{dv: dv, StepPx: stepPx, Gap: gap, ClickGap: clickGap}
}

// send emits one relative report, waits for the chip's ACK and paces the next
func (r *Rel) send(btn byte, dx, dy int) error {
	r.drain()
	if err := r.dv.d.MouseRelRaw(btn, dx, dy); err != nil {
		return err
	}
	if r.Gap > 0 {
		time.Sleep(r.Gap)
	}
	return nil
}

// streamAt is stream with an explicit pacing for THIS frame: the human schedule
func (r *Rel) streamAt(btn byte, dx, dy int, gap time.Duration) error {
	dx = clampInt(dx, 127)
	dy = clampInt(dy, 127)
	if dx == 0 && dy == 0 && btn == 0 {
		return nil
	}
	frame := ch9329.MouseRelFrame(btn, dx, dy, 0)
	// Device bookkeeping is skipped when there is no real device: the pacing
	// seam (r.io) is set in tests, where r.dv is nil and a frame counter would
	// only be a nil dereference.
	if device := r.device(); device != nil {
		device.Frames++
		if device.Verbose && device.Trace != nil {
			device.Trace(fmt.Sprintf("-> %s (streamed %.0fms)", ch9329.Hex(frame), float64(gap)/float64(time.Millisecond)))
		}
	}
	// ACK-gated pacing, not "sleep after the write". See writeReport: three
	// reports written back-to-back in one UART burst reach the host as one fast
	// flick and land with pointer acceleration applied (measured +100 alone vs
	// +478 for three in 1 ms), and a `-gap 0` caller used to skip the wait
	// entirely. The wait now happens BEFORE the next write, which is the only
	// place it can prevent a burst.
	return r.writeReport(frame, gap)
}

// release writes the button-up report UNCONDITIONALLY.
//
// It must not go through streamAt: streamAt's all-zero early return exists to
// suppress redundant zero-motion NON-button frames, but a release is exactly
// btn=0 dx=0 dy=0, so it was swallowed there and the host kept the button
// DOWN while this type believed it was up. Wire log of the defect: the last
// inking frame 01 01 00 FF ... is followed with NO btn=00 frame by a re-press
// and nine btn=1 reports drifting 8.5 cells past the glyph's end.
//
// The frame is written here directly, in the same shape as dv.Release(): a
// zero-mask relative release cannot teleport the pointer (an absolute frame at
// 0,0 would). r.down is derived from what was actually written.
func (r *Rel) release() error {
	frame := ch9329.MouseRelFrame(0, 0, 0, 0)
	device := r.dv.d
	device.Frames++
	if device.Verbose && device.Trace != nil {
		device.Trace(fmt.Sprintf("-> %s (release)", ch9329.Hex(frame)))
	}
	// Drain first: a streaming walk may leave an unread ACK that would make the
	// release look like it failed. Not fatal — the release must still be sent.
	r.drain()
	if _, err := r.port().Write(frame); err != nil {
		return err
	}
	if r.Gap > 0 {
		time.Sleep(r.Gap)
	}
	r.down = false
	return nil
}

// stream emits one relative report WITHOUT waiting for its reply.
//
// It clamps exactly like streamAt: the chip's dx/dy fields are signed int8, so a
// delta outside ±127 would WRAP and move the pointer backwards. The only caller
// today (Home, ±127) is already inside the range; the clamp is here so that a
// future caller cannot reintroduce the wrap. A clamped frame is not an error:
// motion is bounded and the caller's loop accounts for the remainder.
func (r *Rel) stream(btn byte, dx, dy int) error {
	dx = clampInt(dx, 127)
	dy = clampInt(dy, 127)
	device := r.dv.d
	frame := ch9329.MouseRelFrame(btn, dx, dy, 0)
	device.Frames++
	if device.Verbose && device.Trace != nil {
		device.Trace(fmt.Sprintf("-> %s (streamed)", ch9329.Hex(frame)))
	}
	if _, err := device.Port.Write(frame); err != nil {
		return err
	}
	if r.Gap > 0 {
		time.Sleep(r.Gap)
	}
	return nil
}

// drain discards any replies a streaming walk left unread, so the next ACK the
func (r *Rel) drain() {
	r.dv.d.Port.FlushInput()
}

// Move travels by (dx,dy) in bounded steps, button up.
func (r *Rel) Move(dx, dy int) error {
	if err := r.travel(dx, dy); err != nil {
		clearTracked()
		return err
	}
	noteMove(dx, dy)
	return nil
}

// Drag travels by (dx,dy) with the button held. Only legal while Down.
// It uses the SAME damped controller as Move so the two verbs share dynamics.
func (r *Rel) Drag(dx, dy int) error {
	if !r.down {
		return ErrNotDown
	}
	if err := r.fastTravel(ch9329.BtnLeft, dx, dy); err != nil {
		clearTracked()
		return err
	}
	noteMove(dx, dy)
	return nil
}

// DrawStep presses, streams ONE short segment in a straight line and releases,
// then lands the pointer on the segment end with the button UP.
//
// It does not go through Drag. Drag is a CLOSED-LOOP controller: it reads
// ShellPoint() — a position captured ~16 ms earlier, i.e. before the previous
// report had been absorbed — and glides the residual. Inside a held span the
// pointer is already moving, so that stale origin injects a perpendicular
// component into the motion: a 100 % horizontal 300 px drag recorded ~15 cells
// of vertical drift. Chained, those drift legs are the extra corners a
// handwriting reproduction must not have ("你多了一个折角").
//
// drawStep therefore emits an OPEN-LOOP straight stream (the dx,dy are split
// into equal reports of at most stepPx along one line) between two known
// waypoints, and removes any accumulated error only after the release, so the
// correction is pointer motion and never ink.
func (r *Rel) DrawStep(a, c [2]int) error {
	if err := r.strokeSeg(a, c); err != nil {
		return err
	}
	return r.landOn(c)
}

// strokeSeg is the held part of DrawStep: press, equal small reports along the
// straight a->c, release. Reports are sized from the distance so the page (one
// sample per ~16.7 ms frame) sees a glide rather than a single leap.
func (r *Rel) strokeSeg(a, c [2]int) error {
	dx, dy := c[0]-a[0], c[1]-a[1]
	if err := r.streamAt(ch9329.BtnLeft, 0, 0, 0); err != nil { // press
		return err
	}
	r.down = true
	longest := absOf(dx)
	if ay := absOf(dy); ay > longest {
		longest = ay
	}
	n := (longest + reportMaxPx - 1) / reportMaxPx
	if n < 1 {
		n = 1
	}
	if n > maxVerbs {
		n = maxVerbs
	}
	stepGap := time.Duration(dragGapMS) * time.Millisecond
	fx, fy := 0, 0
	for i := 1; i <= n; i++ {
		tx, ty := dx*i/n, dy*i/n
		if tx == fx && ty == fy {
			continue
		}
		if err := r.streamAt(ch9329.BtnLeft, tx-fx, ty-fy, stepGap); err != nil {
			_ = r.streamAt(0, 0, 0, 0)
			r.down = false
			return err
		}
		fx, fy = tx, ty
	}
	r.down = false
	return r.streamAt(0, 0, 0, 0) // release
}

// travel is the long-distance loop: the SAME closed loop that draws, with the
func (r *Rel) travel(dx, dy int) error {
	return r.fastTravel(0, dx, dy)
}

// downRaw presses without the ACK round trip, for use inside a single streamed
// span (strokePath): the span must not stall between press and first report.
func (r *Rel) downRaw() error {
	if err := r.streamAt(ch9329.BtnLeft, 0, 0, 0); err != nil {
		return err
	}
	r.down = true
	return nil
}

// upRaw releases without the ACK round trip; it always ends the span.
//
// It goes through release(), NOT streamAt: streamAt swallows an all-zero frame
// and the release IS an all-zero frame (see release's comment for the wire-log
// proof). This path is the one the paced held stroke uses, so the swallow left
// the physical button DOWN for every finished stroke.
func (r *Rel) upRaw() error {
	return r.release()
}

// ForceRelease writes the button-up report now, ignoring the tracked state: it
// is the error-path guarantee, for the case where the failure IS that the state
// cannot be trusted (a span that died halfway through its reports).
func (r *Rel) ForceRelease() { _ = r.release() }

// Held reports whether this controller believes the button is now held.
func (r *Rel) Held() bool { return r.down }

// DownAndHold presses the button and leaves it held for the caller's own span.
func (r *Rel) DownAndHold() error {
	if err := r.Down(); err != nil {
		return err
	}
	return nil
}

// Down presses the button where the pointer is.
func (r *Rel) Down() error {
	if err := r.send(ch9329.BtnLeft, 0, 0); err != nil {
		return err
	}
	r.down = true
	return nil
}

// Up releases the button where the pointer is.
func (r *Rel) Up() error {
	if err := r.send(0, 0, 0); err != nil {
		return err
	}
	if err := r.send(0, 0, 0); err != nil {
		return err
	}
	r.down = false
	return nil
}

// MoveHeld travels with the LEFT BUTTON HELD, without touching the press state.
func (r *Rel) MoveHeld(dx, dy int) error {
	return r.fastTravel(ch9329.BtnLeft, dx, dy)
}

// Wheel scrolls by notches.
func (r *Rel) Wheel(n int) error {
	if n > 0 {
		return r.dv.d.Scroll(n)
	}
	return r.dv.d.Scroll(-n)
}

// Wait pauses.
func (r *Rel) Wait(ms int) error {
	time.Sleep(time.Duration(ms) * time.Millisecond)
	return nil
}

// Home pins the pointer to the screen's top-left corner using the MINIMUM
func (r *Rel) Home(screenW, screenH int) error {
	need := screenW + screenH
	reports := (need + 126) / 127
	for i := 0; i < reports; i++ {
		if err := r.stream(0, -127, -127); err != nil {
			return err
		}
	}
	r.drain()
	time.Sleep(20 * time.Millisecond)
	noteHome()
	return nil
}

// TravelTo moves from a known origin (0,0) to (x,y) in bounded steps.
func (r *Rel) TravelTo(x, y int) error {
	if err := r.travel(x, y); err != nil {
		clearTracked()
		return err
	}
	noteGoto(x, y)
	return nil
}

// TravelFrom moves from a KNOWN current point (cx,cy) — published by the
func (r *Rel) TravelFrom(cx, cy, x, y int) error {
	if err := r.travel(x-cx, y-cy); err != nil {
		clearTracked()
		return err
	}
	noteGoto(x, y)
	return nil
}

func clamp(v, n int) int {
	if v > n {
		return n
	}
	if v < -n {
		return -n
	}
	return v
}

// clampInt is clamp with its own name for the closed-loop travel path: it
func clampInt(v, n int) int { return clamp(v, n) }

// fastTravel places the pointer at the CURRENT position + (dx,dy) with the
func (r *Rel) fastTravel(btn byte, dx, dy int) error {
	cx, cy, ok := ShellPoint()
	if !ok {
		return ErrNoPosition
	}
	return r.walkTo(btn, [2]int{cx + dx, cy + dy})
}
