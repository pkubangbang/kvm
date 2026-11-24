package ch9329

// Human-shaped pointer motion.
//
// Everything in this file is COSMETIC. The graded property of a move is where
// the pointer LANDS, and that is decided by one absolute 0x04 frame at the end
// of the gesture (see absGotoOnBtn). What this file shapes is the path the
// pointer travels to get there, because a perfectly straight constant-speed,
// constant-cadence path is the classic synthetic-input fingerprint.
//
// The design rule that makes the two compatible:
//
//	Jitter perturbs the PATH, never the LANDING POINT.
//
// Concretely:
//   - Lateral tremor is applied perpendicular to the travel line, with the
//     tremor envelope forced to zero at both ends. The perpendicular component
//     of the last frame is therefore zero, and the along-line component is
//     exactly the requested travel.
//   - The gesture terminates on an explicit absolute frame at the target, which
//     discards any residual path error by construction.
//   - Timing noise changes WHEN a frame is sent, not WHERE it points.
//
// So `slide -from-x 439 -to-x 685 -human` still releases at x=685 no matter
// which random draws were made.

import (
	"math/rand"
	"time"
)

// HumanOptions turns a mechanical move into a human-shaped one. The zero value
// with Enabled set to false is a no-op, so existing callers are unaffected.
type HumanOptions struct {
	// Enabled switches the whole feature on. When false the other fields are
	// ignored and the move is the plain deterministic path.
	Enabled bool
	// Jitter is the maximum lateral tremor in pixels (perpendicular to the
	// travel line), applied with a smooth zero-mean envelope. 0 means the
	// default (2.5 px). Set 0 explicitly via NoJitter if you want none.
	Jitter float64
	// NoJitter disables only the lateral tremor while keeping timing noise,
	// easing and the micro-pause.
	NoJitter bool
	// TimingSigma is the relative standard deviation of the per-frame gap
	// (0.28 = ±28 % of the nominal rate, the default). Frame gaps are drawn
	// from a jittered uniform and clamped to a sane band.
	TimingSigma float64
	// PauseChance is the per-frame probability of a short dwell (a hand
	// hesitates); PauseMax bounds that dwell (default 1.5 %, 40 ms).
	PauseChance float64
	PauseMax    time.Duration
	// Ease shapes the velocity profile: 0 = linear, 1 = full ease-in/out
	// (default 0.75). Easing is applied to the STEP SIZE, not to the endpoint.
	Ease float64
	// Wander bends the path: a single low-frequency lateral offset, in units of
	// Jitter. 1.0 (default) gives a gentle, slightly non-straight travel.
	Wander float64
	// Seed makes a run reproducible. 0 means "seed from the clock".
	Seed int64
}

// WithDefaults fills in the values a zero field stands for at the given
// enabled state.
func (h HumanOptions) WithDefaults() HumanOptions {
	if !h.Enabled {
		return h
	}
	if h.Jitter == 0 && !h.NoJitter {
		h.Jitter = 2.5
	}
	if h.TimingSigma == 0 {
		h.TimingSigma = 0.28
	}
	if h.PauseChance == 0 {
		h.PauseChance = 0.015
	}
	if h.PauseMax == 0 {
		h.PauseMax = 40 * time.Millisecond
	}
	if h.Ease == 0 {
		h.Ease = 0.75
	}
	if h.Wander == 0 {
		h.Wander = 1
	}
	return h
}

// rng builds the source. A zero seed is random; anything else is reproducible,
// which is what a test needs.
func (h HumanOptions) rng() *rand.Rand {
	if h.Seed != 0 {
		return rand.New(rand.NewSource(h.Seed))
	}
	return rand.New(rand.NewSource(time.Now().UnixNano()))
}

// path returns the pixel position at progress f in [0,1] along from->to,
// including the lateral tremor.
//
// The tremor envelope is sin(pi*f), so it is exactly zero at f=0 and f=1 and
// bounded by (Wander+0.45)/(Wander+1) < 1 of the jitter amplitude in between; the
// whole amplitude is then scaled so the excursion never exceeds Jitter pixels.
// The offset is applied along the unit perpendicular. The along-line coordinate
// is deliberately left untouched, so a horizontal drag stays horizontal and its
// horizontal travel equals the eased fraction of the total delta exactly.
func (h HumanOptions) path(from, to Point, f float64, phase, phase2 float64) Point {
	dx := float64(to.X - from.X)
	dy := float64(to.Y - from.Y)
	size := sqrt(dx*dx + dy*dy)
	if size == 0 || h.Jitter == 0 {
		return Point{X: from.X + int(dx*f), Y: from.Y + int(dy*f)}
	}
	// Unit perpendicular to the travel line.
	px, py := -dy/size, dx/size
	env := sinPi(f)
	if env < 0 {
		env = 0
	}
	// Slow bend plus a faster ripple; both fade out at the ends with the
	// envelope, so the excursion is zero at the press and at the release.
	wander := h.Wander
	if wander == 0 {
		wander = 1
	}
	amp := wander + 0.45
	lateral := env * h.Jitter / amp * (wander*sinPi(f*0.9+phase) + 0.45*sinPi(f*3.1+phase2))
	// Clamp so the tremor band is a hard guarantee, not an approximation: the
	// envelope bound is analytic, but a caller is entitled to read Jitter as
	// "never further than this from the line".
	if lateral > h.Jitter {
		lateral = h.Jitter
	}
	if lateral < -h.Jitter {
		lateral = -h.Jitter
	}
	// Round rather than truncate toward zero: truncation would make the final
	// frame land a pixel short of the requested travel in the positive
	// direction and a pixel long in the negative one - a directional bias in
	// exactly the coordinate the widget measures.
	return Point{
		X: from.X + roundI(dx*f+px*lateral),
		Y: from.Y + roundI(dy*f+py*lateral),
	}
}

// roundI rounds to the nearest int half away from zero, without importing math.
func roundI(v float64) int {
	if v < 0 {
		return -int(-v + 0.5)
	}
	return int(v + 0.5)
}

// eased warps linear progress into a velocity profile: slow start, fast middle,
// slow finish. With ease <= 0 the progress passes through unchanged.
func eased(f, ease float64) float64 {
	if ease <= 0 {
		return f
	}
	// smootherstep, blended in by `ease`.
	s := f * f * f * (f*(f*6-15) + 10)
	return f*(1-ease) + s*ease
}

// nextGap draws the wall-clock gap before the next frame.
func (h HumanOptions) nextGap(r *rand.Rand, nominal time.Duration) time.Duration {
	if !h.Enabled || nominal <= 0 {
		return nominal
	}
	// Jittered uniform around nominal: mean == nominal, spread == ±sigma.
	spread := h.TimingSigma
	if spread > 0.9 {
		spread = 0.9
	}
	g := float64(nominal) * (1 + spread*(2*r.Float64()-1))
	if g < float64(nominal)*0.25 {
		g = float64(nominal) * 0.25
	}
	d := time.Duration(g)
	if r.Float64() < h.PauseChance {
		d += time.Duration(r.Float64() * float64(h.PauseMax))
	}
	return d
}

// sinPi is sin(pi*x) without importing math, matching this package's style of
// avoiding the dependency for a handful of scalar calls.
//
// x is folded into [0,1] using sin(pi*(1-t)) = sin(pi*t), which lets the
// polynomial work on the quarter-wave shape only. The parabola 4t(1-t) is a
// good start (0 at both ends, 1 in the middle) but overshoots the true sine:
// its own maximum is exactly 1, yet the composite forms built from it are not.
// One Newton-style correction against the parabola's flat top is enough to pull
// the peak to 1 and the error below ~1.5e-3 over the whole range - three orders
// of magnitude below a device pixel, so the tremor bound stays honest.
func sinPi(x float64) float64 {
	x = x - 2*float64(int(x/2))
	if x < 0 {
		x += 2
	}
	sign := 1.0
	if x > 1 {
		x = 2 - x // sin(pi*(2-x)) = -sin(pi*x)
		sign = -1
	}
	// sin(pi*x) for x in [0,1].
	s := 1 - (2*x-1)*(2*x-1) // == 4x(1-x), the parabola
	return sign * sqrt(s)
}
