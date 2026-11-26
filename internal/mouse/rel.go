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
	// btn is the button bit every inking/stroke report carries: BtnLeft by
	// default, BtnRight or BtnMiddle via SetButton. A zero or multi-bit mask
	// is NOT accepted — see SetButton.
	btn byte
	// io is the byte sink. nil means "use dv.d.Port"; the relative emitter goes
	// through port() so report pacing can be unit-tested without a chip.
	io portIO
	// stallFrom / stallSilent / stallSeen are the stall-prevention ledger: the
	// last witness position, how many consecutive reports have failed to move
	// it, and whether a baseline has been taken. See stallGuard.
	stallFrom   [2]int
	stallSilent int
	stallSeen   bool
	// witness is where the stall guard reads the pointer from. nil means the
	// real platform witness (WirePoint); tests substitute a frozen or walking
	// sequence, because the rule being tested is arithmetic over consecutive
	// samples and must not depend on a live cursor.
	witness func() ([2]int, bool)
}

// NewRel builds the relative controller.
func NewRel(dv *Device, stepPx int, gap, clickGap time.Duration) *Rel {
	return &Rel{dv: dv, StepPx: stepPx, Gap: gap, ClickGap: clickGap, btn: ch9329.BtnLeft}
}

// SetButton chooses which button the held reports carry: ch9329.BtnLeft (the
// default), BtnRight or BtnMiddle. Exactly one bit must be set, because the
// button byte is mirrored straight into the HID report and a chord would make
// every inking frame a multi-button one — not what any caller here asked for.
func (r *Rel) SetButton(btn byte) error {
	switch btn {
	case ch9329.BtnLeft, ch9329.BtnRight, ch9329.BtnMiddle:
		r.btn = btn
		return nil
	}
	return fmt.Errorf("button mask 0x%02x is not a single button (left=0x01 right=0x02 middle=0x04)", btn)
}

// Button reports the mask the held reports currently carry.
func (r *Rel) Button() byte { return r.btn }

// send emits one relative report, waits for the chip's ACK and paces the next
//
// It goes through r.port(), NOT dv.d.Port: a press carries dx=dy=0, so it is
// the one frame that has no motion to reveal a nil device through, and calling
// dv.d.MouseRelRaw directly made it the only emitter that could neither be
// paced nor unit-tested (it panicked on a nil Device in the seam tests). The
// ACK is drained through the same seam, so a NAK is visible here too.
func (r *Rel) send(btn byte, dx, dy int) error {
	r.drain()
	frame := ch9329.MouseRelFrame(btn, dx, dy, 0)
	if device := r.device(); device != nil {
		device.Frames++
		if device.Verbose && device.Trace != nil {
			device.Trace(fmt.Sprintf("-> %s (send)", ch9329.Hex(frame)))
		}
	}
	if _, err := r.port().Write(frame); err != nil {
		return err
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
	if pressRidesFirstMove && btn != 0 && dx == 0 && dy == 0 {
		// A zero-motion button frame is NOT a usable press on this host: the
		// page that observes it reports pointermove with buttons===0 and
		// closes the stroke before any ink arrives. Deferring is the whole
		// fix: `r.down` is already true (the caller sets it), so the next
		// non-zero frame carries BtnLeft and IS the press the host sees.
		// See press_windows.go / press_other.go for the per-OS choice.
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
	if device := r.device(); device != nil {
		device.Frames++
		if device.Verbose && device.Trace != nil {
			device.Trace(fmt.Sprintf("-> %s (release)", ch9329.Hex(frame)))
		}
	}
	// Drain first: a streaming walk may leave an unread ACK that would make the
	// release look like it failed. Not fatal — the release must still be sent.
	r.drain()
	if _, err := r.port().Write(frame); err != nil {
		return err
	}
	r.down = false
	return nil
}

// press writes the button-down frame and reports whether the wire now holds a
// button, so the caller's `r.down` flag is derived from bytes rather than
// intent.
//
// THE DEFECT THIS EXISTS TO KILL: on Windows, streamAt DROPS a zero-motion
// button frame (pressRidesFirstMove — the inputpad page reads such a frame as a
// lift). Calling streamAt(btn,0,0,0) and then setting `r.down = true` regardless
// therefore set the flag after writing NOTHING. Two callers did exactly that
// (strokeSeg, downRaw), so on Windows any btn!=0 zero-delta report produced a
// "held" ledger entry with no press on the wire, and a zero-motion span reached
// release() having never pressed.
//
// The deferral is kept — it is a real property of the host — but it is now
// EXPLICIT: pressed=false means "the press rides the next moving report", which
// the caller may rely on but must not mistake for held.
func (r *Rel) press() error {
	if err := r.streamAt(r.btn, 0, 0, 0); err != nil {
		return err
	}
	return nil
}

// pressHeld is press plus the flag, for the callers whose span WILL move.
//
// It returns nil and leaves r.down true either way, because the deferred press
// is a promise the emitter keeps: the next non-zero report carries the button.
// A span that never moves must NOT use this — it would claim a hold the host
// never saw. Dot() and other zero-length spans go through strokeSeg, whose
// first moving report (if any) is the press.
func (r *Rel) pressHeld() error {
	if err := r.press(); err != nil {
		return err
	}
	r.down = true
	return nil
}

// pressNow writes the button-down frame WITH NO DEFERRAL, and reports whether
// the wire holds a button.
//
// It exists for spans that will never move: a zero-motion button frame is
// dropped by streamAt on hosts where pressRidesFirstMove is true (the inputpad
// page reads it as a lift), and the deferral's promise — "the press rides the
// first moving report" — has nothing to ride. Without this, Dot() emitted only
// a RELEASE for a press the host never saw: a click that never moves was a
// silent no-op that reported success.
//
// On a deferring host this deliberately re-introduces the zero-motion frame the
// generic path suppresses, because the alternative is losing the click
// entirely. It is the one place where "the page might misread this frame" is
// the lesser evil than "there is no frame at all".
// (Defined here in rel.go, beside release(), because it writes a frame: the
// wire-owning file is the only one that should reach for ch9329 directly.)

// pressNow writes the button-down frame WITH NO DEFERRAL and sets r.down.
//
// It exists for spans that will NEVER move. streamAt drops a zero-motion button
// frame on hosts where pressRidesFirstMove is true (the inputpad page reads such
// a frame as a lift), deferring the press to the first moving report — but a
// zero-motion span has no moving report, so the press vanished and Dot() emitted
// only a RELEASE for a button the host never saw: a click that never moves was a
// silent no-op that reported success.
//
// Re-introducing the frame here is deliberate: "the page might misread it" is a
// lesser evil than "there is no frame at all". Callers: Dot(), and HumanStroke
// when its path has a single point.
func (r *Rel) pressNow() error {
	frame := ch9329.MouseRelFrame(r.btn, 0, 0, 0)
	if device := r.device(); device != nil {
		device.Frames++
		if device.Verbose && device.Trace != nil {
			device.Trace(fmt.Sprintf("-> %s (press, zero-motion span)", ch9329.Hex(frame)))
		}
	}
	r.drain()
	if _, err := r.port().Write(frame); err != nil {
		return err
	}
	r.down = true
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
	return nil
}

// drain discards any replies a streaming walk left unread, so the next ACK the
func (r *Rel) drain() {
	r.port().FlushInput()
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
	if err := r.fastTravel(r.btn, dx, dy); err != nil {
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
	// pressHeld() OWNS the flag. Setting r.down here instead was wrong twice
	// over: on Windows streamAt drops a zero-motion button frame
	// (pressRidesFirstMove), so the flag claimed "held" while nothing had been
	// written; and on ANY host a failed press left the flag true with no button
	// down on the wire. pressHeld is safe for a span that WILL move, because
	// the deferred press is then carried by the first moving report below.
	if err := r.pressHeld(); err != nil {
		return err
	}
	steps := evenReports(dx, dy)
	stepGap := time.Duration(dragGapMS) * time.Millisecond
	for _, st := range steps {
		if err := r.streamAt(r.btn, st[0], st[1], stepGap); err != nil {
			// Abandon the span through release(), which clears r.down only
			// AFTER a successful write. Clearing the flag here first was a
			// wire/flag desync: on a failed release the physical button stayed
			// DOWN while Held() reported up, so nothing later knew a release
			// was owed and the next "button-up" correction moved the pointer
			// under a held button (sim-b CRITICAL-2).
			_ = r.release()
			return err
		}
	}
	// release(), NOT streamAt(0,0,0,0): that frame is all-zero and streamAt
	// suppresses it, so the chip kept the button DOWN while this type believed
	// it was up — the same defect release() documents at length. See D2.
	// The flag is cleared BY release(), after the write, never before it.
	return r.release()
}

// travel is the long-distance loop: the SAME closed loop that draws, with the
func (r *Rel) travel(dx, dy int) error {
	return r.fastTravel(0, dx, dy)
}

// downRaw presses without the ACK round trip, for use inside a single streamed
// span (strokePath): the span must not stall between press and first report.
func (r *Rel) downRaw() error {
	// pressHeld() owns the flag — see its comment. Setting it here after a
	// streamAt that may have written NOTHING (a dropped zero-motion press on
	// Windows) was the ledger claiming a button the wire never saw. The caller
	// (strokePath) always follows with moving reports, so the deferral is kept.
	return r.pressHeld()
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
	if err := r.send(r.btn, 0, 0); err != nil {
		return err
	}
	r.down = true
	return nil
}

// Up releases the button where the pointer is.
//
// It goes through release(), NOT send(): send() writes the frame through a path
// that does not clear r.down, and the old body wrote the same all-zero frame
// TWICE for no reason. The wire protocol is cross-platform — a release is one
// `57 AB 00 05 05 01 00 00 00 00 0D` frame on every host — so the emitter's job
// is to put exactly that frame on the wire, once, and only then believe the
// button is up.
func (r *Rel) Up() error {
	return r.release()
}

// MoveHeld travels with the LEFT BUTTON HELD, without touching the press state.
func (r *Rel) MoveHeld(dx, dy int) error {
	return r.fastTravel(r.btn, dx, dy)
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
	// Steps of at most heldStepPx of TRAVEL, not 127 per axis. The old form
	// emitted (-127,-127) = 179.6 px of travel per report — 9x the law and the
	// exact "slam the pointer into the corner" this package condemns elsewhere
	// (sim-b CRITICAL-4). Button-up, so it is placement, but placement still
	// may not leave the acceleration-free band.
	need := screenW + screenH
	step := HeldStepPx
	if step < 1 {
		step = 1
	}
	reports := (need + step - 1) / step
	for i := 0; i < reports; i++ {
		if err := r.stream(0, -step, -step); err != nil {
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
