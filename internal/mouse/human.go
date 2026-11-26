package mouse

// Human-like pen motion.
//
// The relative emitter can place reports exactly, but a stream of equal reports
// at one fixed gap does not look like handwriting: it has no acceleration, no
// dwell at corners, and no variation, and the user reads it immediately as a
// machine ("do you know how human move?"). This file is the profile that makes
// a stroke plausible:
//
//   - SPEED PROFILE. Each segment is traversed with ease-in/ease-out: the pen
//     accelerates from rest, cruises, and decelerates into the next vertex. A
//     constant-speed pen has a visible kick at every corner.
//   - DWELL. A real pen slows to a near-stop AT a corner before turning. The
//     dwell is applied at the vertex, not by slowing the whole leg.
//   - VARIABLE STEP. Report size follows the instantaneous speed (small steps
//     while slow, larger while cruising), so the host sees a varied stream
//     rather than a metronome. Steps stay inside the ±127 byte field.
//   - TREMOR. A small bounded wobble perpendicular to travel. It is the one
//     part that must NOT be compensated away: without it the line is a
//     mathematically straight segment, which no hand produces.
//   - PEN-DOWN DWELL. A dot is a press, a short stay, and a release. A press
//     that is released within a report or two never becomes its own stroke in
//     the pad's trace (measured: a 6-cell dot sent as one press+move+release
//     produced no trace entry at all), so the dwell is what makes a dot exist.
//
// The pacing itself (ACK-gated, floor reportGapFloor) is NOT duplicated here:
// every report goes through streamAt, so the host still never sees a burst.

import (
	"math"
	"time"
)

// HumanProfile is the shape of one hand's motion. Zero fields take the
// defaults in withDefaults, so a caller can override only what it means to.
type HumanProfile struct {
	// StepMin/StepMax bound the report size in px along the direction of
	// travel. They are the knob for GRANULARITY: a slower hand takes smaller
	// steps and records a denser stroke.
	StepMin, StepMax int
	// GapSlow is the spacing used while the pen is slow (at the ends of a
	// segment and inside a corner), GapFast while it cruises.
	GapSlow, GapFast time.Duration
	// CornerDwell is how long the pen nearly stops at a vertex.
	CornerDwell time.Duration
	// TremorPx is the amplitude of the perpendicular wobble, in px.
	TremorPx float64
}

func (p HumanProfile) withDefaults() HumanProfile {
	// STEP SIZE MUST LIE INSIDE THE HOST'S 1:1 BAND.
	//
	// These defaults were 2 and 6 px, and that is the lossy band on this host:
	// the measured delivery table says 6 px -> 0.66x and 3 px -> ~0.01x, so a
	// held report of 2-6 px is coalesced or dropped by the host before the
	// pointer ever sees it. The visible result was a held drag that fell short
	// (20 px requested -> 12 delivered) and then tripped the stall guard
	// ("the pointer stopped advancing for 8 consecutive reports") because the
	// pointer genuinely was not advancing: the reports were being swallowed.
	//
	// The band that arrives is roughly 12-60 px per report. Defaults are set at
	// its centre, and HeldStepPx (20) bounds every split, so the step bounds
	// and the subdivision law agree instead of fighting each other.
	if p.StepMin <= 0 {
		p.StepMin = 12
	}
	if p.StepMax <= 0 {
		p.StepMax = 20
	}
	if p.StepMin < 12 {
		p.StepMin = 12 // below this the host starts dropping reports
	}
	if p.StepMax < p.StepMin {
		p.StepMax = p.StepMin
	}
	if p.GapSlow <= 0 {
		p.GapSlow = 16 * time.Millisecond
	}
	if p.GapFast <= 0 {
		p.GapFast = 9 * time.Millisecond
	}
	if p.CornerDwell <= 0 {
		p.CornerDwell = 30 * time.Millisecond
	}
	if p.TremorPx <= 0 {
		p.TremorPx = 0.6
	}
	return p
}

// Dot draws a pen-down dot: press, stay, release. A press released without a
// dwell is not observable as its own stroke in the pad's trace.
//
// The press goes through pressNow, NOT downRaw: downRaw defers the frame to the
// first moving report, and a dot by definition has none, so on a deferring host
// the click vanished entirely. See pressNow.
func (r *Rel) Dot(p HumanProfile, stay time.Duration) error {
	p = p.withDefaults()
	if err := r.pressNow(); err != nil {
		return err
	}
	// Keep the button down long enough for a sample to be taken, then release.
	if stay <= 0 {
		stay = 45 * time.Millisecond
	}
	time.Sleep(stay)
	return r.upRaw()
}

// HumanStroke draws one pen-down stroke through the pixel path and releases.
//
// The path is a polyline of SCREEN pixels (the caller has already placed the
// pen on path[0]). Corners are exactly the polyline's vertices: the profile
// decelerates into each one and dwells there, which is both what a hand does
// and what stops the corner being cut.
func (r *Rel) HumanStroke(path Path, p HumanProfile) error {
	p = p.withDefaults()
	if len(path) == 0 {
		return nil
	}
	// A SINGLE-POINT path never moves, so the deferred press has nothing to
	// ride: it must be written now or the stroke is a release-for-a-press-the-
	// host-never-saw. Multi-point paths keep the deferral (downRaw), because
	// their first moving report carries the button.
	if len(path) == 1 {
		if err := r.pressNow(); err != nil {
			return err
		}
		time.Sleep(20 * time.Millisecond)
		return r.upRaw()
	}
	if err := r.downRaw(); err != nil {
		return err
	}
	// The press itself needs a beat before the first report, or the first
	// sample of the stroke is taken before the pad knows the button is down.
	time.Sleep(20 * time.Millisecond)
	if len(path) == 1 {
		return r.upRaw()
	}
	phase := 0.0
	for seg := 1; seg < len(path); seg++ {
		if err := r.humanSegment(path[seg-1], path[seg], p, &phase); err != nil {
			_ = r.upRaw()
			return err
		}
		// Dwell AT the vertex: a hand nearly stops before it turns.
		if seg < len(path)-1 {
			time.Sleep(p.CornerDwell)
		}
	}
	return r.upRaw()
}

// humanSegment walks one straight leg with an ease-in/ease-out speed profile.
func (r *Rel) humanSegment(a, b [2]int, p HumanProfile, phase *float64) error {
	dx, dy := b[0]-a[0], b[1]-a[1]
	total := math.Hypot(float64(dx), float64(dy))
	if total < 0.5 {
		return nil
	}
	ux, uy := float64(dx)/total, float64(dy)/total
	// Perpendicular, for the tremor.
	nx, ny := -uy, ux
	travelled := 0.0
	for travelled < total {
		// t in [0,1] along the leg, then the ease curve: slow, fast, slow.
		t := travelled / total
		ease := easeInOut(t)
		// Instantaneous speed (0..1) sets both the step size and the gap.
		step := float64(p.StepMin) + float64(p.StepMax-p.StepMin)*ease
		if travelled+step > total {
			step = total - travelled
		}
		travelled += step
		gap := p.GapSlow
		if ease > 0.35 {
			gap = p.GapFast
		}
		// Tremor: a slow bounded wobble, not noise, so it never looks jittery.
		*phase += 0.55
		wob := p.TremorPx * math.Sin(*phase)
		mx := ux*step + nx*wob
		my := uy*step + ny*wob
		sx, sy := int(math.Round(mx)), int(math.Round(my))
		if sx == 0 && sy == 0 {
			// Sub-pixel step: advance without a report but do not lose it.
			continue
		}
		if err := r.streamAt(r.btn, sx, sy, gap); err != nil {
			return err
		}
	}
	return nil
}

// easeInOut is a smooth acceleration/deceleration curve on [0,1].
func easeInOut(t float64) float64 {
	if t < 0 {
		t = 0
	}
	if t > 1 {
		t = 1
	}
	return 0.5 * (1 - math.Cos(math.Pi*t))
}

// DefaultHuman is the profile the CLI uses, tuned so that a letter is written
// at a pace a hand would use rather than at the fastest rate the host accepts.
func DefaultHuman() HumanProfile {
	return HumanProfile{
		// Tuned for the 10 s word budget: the ACK floor (8 ms) dominates the
		// spacing, so the only real levers are step size and dwell. Cruise
		// steps are 8 px (still well under the 30 px where the host starts
		// accelerating a stream) and the corner dwell is 20 ms.
		StepMin: 3, StepMax: 8,
		GapSlow: 14 * time.Millisecond, GapFast: 8 * time.Millisecond,
		CornerDwell: 20 * time.Millisecond,
		TremorPx:    0.6,
	}
}
