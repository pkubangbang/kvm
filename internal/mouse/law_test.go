package mouse

// Property tests for docs/movement-law.md. The proof is only worth what its
// enforcement is worth: every invariant in the document is asserted here over
// randomised inputs, so a future edit that quietly breaks the law fails the
// suite instead of the user's handwriting.

import (
	"math"
	"math/rand"
	"testing"
)

// I1 — Bounded vector step. Every emitted report must be a short step of actual
// TRAVEL, not merely inside the chip's per-axis byte range.
func TestReportStepBoundedByVectorLength(t *testing.T) {
	errBound := HeldStepPx + 2 // integer truncation allows at most ~1 unit per axis
	for i := 0; i < 20000; i++ {
		dx := rand.Intn(4001) - 2000
		dy := rand.Intn(4001) - 2000
		for _, r := range evenReports(dx, dy) {
			travel := math.Hypot(float64(r[0]), float64(r[1]))
			if travel > float64(errBound) {
				t.Fatalf("evenReports(%d,%d) emitted (%d,%d) = %.1f px of travel; law I1 bound is %d",
					dx, dy, r[0], r[1], travel, errBound)
			}
		}
	}
}

// I3 — Exact sum. No report may be dropped and no distance may be invented.
//
// The expected value is the LITERAL request, never scaleByGain(dx, dy). The
// first version compared against scaleByGain — the very function evenReports
// calls first — so a mutant halving the gain inside scaleByGain moved the
// expected and the actual together and all 20000 iterations passed. A test
// whose oracle is the code under test certifies nothing; this one now states
// the law independently, and a gain mutation is caught as a real shortfall.
func TestReportSumIsExact(t *testing.T) {
	for i := 0; i < 20000; i++ {
		dx := rand.Intn(8001) - 4000
		dy := rand.Intn(8001) - 4000
		wx, wy := scaleByGain(dx, dy)
		// The gain itself must be the identity on a non-accelerating host. If
		// that premise ever changes, this fails HERE rather than silently
		// redefining "exact".
		if wx != dx || wy != dy {
			t.Fatalf("scaleByGain(%d,%d) = (%d,%d): the emitter is applying a gain, "+
				"so a report no longer moves the pointer by the distance it asks for", dx, dy, wx, wy)
		}
		sx, sy := 0, 0
		for _, r := range evenReports(dx, dy) {
			sx += r[0]
			sy += r[1]
		}
		if sx != dx || sy != dy {
			t.Fatalf("evenReports(%d,%d) sums to (%d,%d); law I3 requires exactly (%d,%d)", dx, dy, sx, sy, dx, dy)
		}
	}
}

// I4 — Sustained rate. The bound must be computed from the PRODUCTION constants,
// and it must be computed from the pacing the emitter actually applies.
//
// The first version hard-coded `const frameGapMS = 8` locally and never touched
// production code, so halving reportGapFloor (doubling the real rate) left it
// green. It now reads reportGapFloor and HeldStepPx, and asserts the rate the
// emitter can actually sustain.
func TestSustainedRateBound(t *testing.T) {
	// HeldStepPx is the largest per-report travel the splitter may emit; a
	// diagonal report is HeldStepPx in each axis, so the vector length can
	// reach HeldStepPx*sqrt(2) if the splitter ever stops splitting. Use the
	// measured worst case the splitter actually produces.
	worstTravel := 0.0
	for _, c := range [][2]int{{0, HeldStepPx}, {HeldStepPx, 0}, {300, 300}, {HeldStepPx, HeldStepPx}} {
		for _, r := range evenReports(c[0], c[1]) {
			if tr := math.Hypot(float64(r[0]), float64(r[1])); tr > worstTravel {
				worstTravel = tr
			}
		}
	}
	gapSeconds := reportGapFloor.Seconds()
	if gapSeconds <= 0 {
		t.Fatalf("reportGapFloor is %v: the emitter has no pacing floor, so reports burst", reportGapFloor)
	}
	rate := worstTravel / gapSeconds
	// The band the host does not accelerate in. A change to EITHER constant now
	// has to confront the resulting speed.
	const band = 3000.0
	if rate > band {
		t.Fatalf("sustained rate %.0f px/s (%.1f px per %.0f ms report) exceeds the acceleration-free band (~%.0f px/s)",
			rate, worstTravel, gapSeconds*1000, band)
	}
}

// A large request must be SPLIT, never emitted as one big report — the exact
// pre-fix failure, where a held (300,300) delta produced (100,100) = 141 px.
func TestDiagonalIsSplitNotLeapt(t *testing.T) {
	for _, c := range [][2]int{{300, 300}, {500, 0}, {0, 500}, {127, 127}, {1000, 1000}} {
		reps := evenReports(c[0], c[1])
		if len(reps) < 2 {
			t.Fatalf("evenReports(%v) returned %d report(s); a diagonal must be split", c, len(reps))
		}
		for _, r := range reps {
			if travel := math.Hypot(float64(r[0]), float64(r[1])); travel > float64(HeldStepPx+2) {
				t.Fatalf("evenReports(%v) emitted (%d,%d) = %.1f px in one frame", c, r[0], r[1], travel)
			}
		}
	}
}
