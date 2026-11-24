package ch9329

// Tests for the human-motion shaper. The property under test is the one that
// makes the feature safe: however the randomness falls, the tremor is zero at
// both ends of the path and the landing point is the one that was asked for.

import (
	"testing"
	"time"
)

func TestEasedEndpoints(t *testing.T) {
	for _, ease := range []float64{0, 0.3, 0.75, 1} {
		if got := eased(0, ease); got != 0 {
			t.Errorf("eased(0, %v) = %v, want 0", ease, got)
		}
		if got := eased(1, ease); got != 1 {
			t.Errorf("eased(1, %v) = %v, want 1", ease, got)
		}
	}
	// A non-zero easing must be monotonic, otherwise a frame could move backwards.
	prev := 0.0
	for i := 0; i <= 100; i++ {
		f := float64(i) / 100
		v := eased(f, 0.75)
		if v < prev-1e-9 {
			t.Fatalf("eased not monotonic at f=%v: %v < %v", f, v, prev)
		}
		prev = v
	}
}

func TestSinPiValues(t *testing.T) {
	cases := []struct{ x, want float64 }{
		{0, 0}, {0.5, 1}, {1, 0}, {1.5, -1}, {2, 0}, {-0.5, -1},
	}
	for _, c := range cases {
		if got := sinPi(c.x); abs2(got-c.want) > 1e-6 {
			t.Errorf("sinPi(%v) = %v, want %v", c.x, got, c.want)
		}
	}
}

func abs2(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// The tremor must be exactly zero at both ends: at f=0 and f=1 the returned
// point must be the un-tremored one, and the along-line coordinate must equal
// the travel fraction. This is what keeps the graded landing point intact.
func TestPathEndpointsAreExact(t *testing.T) {
	from := Point{439, 534}
	to := Point{685, 534} // the graded 246 px slider travel
	h := HumanOptions{Enabled: true}.WithDefaults()
	h.Seed = 7
	if h.Jitter <= 0 {
		t.Fatal("defaults produced no jitter; the test would be vacuous")
	}
	for _, f := range []float64{0, 1} {
		got := h.path(from, to, f, 1.7, 3.9)
		want := Point{from.X + int(float64(to.X-from.X)*f), from.Y + int(float64(to.Y-from.Y)*f)}
		if got != want {
			t.Fatalf("path(f=%v) = %v, want %v (tremor leaked into an endpoint)", f, got, want)
		}
	}
}

// Over many random phases the lateral excursion must stay bounded by Jitter and
// average out to (near) zero, and the horizontal arrival must never drift: a
// horizontal drag stays horizontal at both ends.
func TestPathTremorIsBoundedAndZeroMean(t *testing.T) {
	from := Point{0, 0}
	to := Point{246, 0}
	h := HumanOptions{Enabled: true}.WithDefaults()
	sum := 0.0
	n := 0
	for phase := 0.0; phase < 6.28; phase += 0.05 {
		for i := 1; i < 100; i++ {
			f := float64(i) / 100
			p := h.path(from, to, f, phase, phase*1.9)
			if p.Y < -int(h.Jitter)-1 || p.Y > int(h.Jitter)+1 {
				t.Fatalf("lateral offset %d exceeds jitter bound %v at f=%v", p.Y, h.Jitter, f)
			}
			sum += float64(p.Y)
			n++
		}
	}
	if mean := sum / float64(n); abs2(mean) > 0.25 {
		t.Errorf("lateral tremor mean = %.3f px, want ~0", mean)
	}
}

// A human path must still deliver the full horizontal travel at the final
// frame, because that frame is what the widget measures.
func TestPathDeliversFullTravel(t *testing.T) {
	from := Point{439, 534}
	to := Point{685, 534}
	h := HumanOptions{Enabled: true, Seed: 99}.WithDefaults()
	near := h.path(from, to, 0.999, 2.2, 5.1)
	if near.X != to.X {
		t.Fatalf("penultimate frame x = %d, want %d", near.X, to.X)
	}
	if near.Y < from.Y-int(h.Jitter)-1 || near.Y > from.Y+int(h.Jitter)+1 {
		t.Fatalf("penultimate frame y = %d escaped the tremor band", near.Y)
	}
}

func TestNextGapStatistics(t *testing.T) {
	h := HumanOptions{Enabled: true, Seed: 3}.WithDefaults()
	r := h.rng()
	nominal := 16 * time.Millisecond
	const n = 20000
	var sum time.Duration
	min, max := time.Duration(1<<62), time.Duration(0)
	for i := 0; i < n; i++ {
		g := h.nextGap(r, nominal)
		sum += g
		if g < min {
			min = g
		}
		if g > max {
			max = g
		}
	}
	mean := sum / n
	// The mean gap must stay close to nominal: the pause is rare enough and the
	// jitter symmetric enough that a 2.5 s slide does not become a 4 s slide.
	if mean < nominal*95/100 || mean > nominal*115/100 {
		t.Errorf("mean gap %v drifted from nominal %v", mean, nominal)
	}
	if min < nominal/4 {
		t.Errorf("minimum gap %v below the floor %v", min, nominal/4)
	}
	if max <= nominal+20*time.Millisecond {
		t.Logf("note: no pause drawn in %d samples (max %v)", n, max)
	}
}

func TestHumanDisabledIsIdentity(t *testing.T) {
	var h HumanOptions // Enabled == false
	h = h.WithDefaults()
	if h.Jitter != 0 || h.TimingSigma != 0 || h.Ease != 0 {
		t.Fatalf("disabled options were filled in: %+v", h)
	}
	from := Point{10, 10}
	to := Point{110, 60}
	if got, want := h.path(from, to, 0.5, 1, 2), (Point{60, 35}); got != want {
		t.Fatalf("disabled path = %v, want the straight-line %v", got, want)
	}
	if got := h.nextGap(h.rng(), 16*time.Millisecond); got != 16*time.Millisecond {
		t.Fatalf("disabled gap = %v, want the nominal 16ms", got)
	}
}
