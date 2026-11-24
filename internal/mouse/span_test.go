package mouse

import "testing"

func maxOf(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// The motion layer's one law: a requested distance is divided by the host's
// measured gain and then sent as equal reports no longer than reportMaxPx,
// whose sum is exactly the scaled request — so the plain even move cannot drift.
//
// The gain is measured, not assumed: 60 -> 92, 100 -> 155, 160 -> 248 and
// -100 -> -155 on the wire (1.53-1.55), independent of report size and count.
func TestEvenReportsPlateauAndExactSum(t *testing.T) {
	cases := [][2]int{
		{0, 0}, {1, 0}, {-1, 0}, {0, 1}, {6, 0}, {-6, 0}, {60, 0}, {100, 0},
		{101, 0}, {-101, 0}, {160, 0}, {300, 0}, {320, 0}, {400, 0}, {600, 0},
		{0, -300}, {486, 0}, {-486, 504}, {100, 100}, {127, -127},
	}
	for _, c := range cases {
		reps := evenReports(c[0], c[1])
		sx, sy := 0, 0
		for _, r := range reps {
			if absOf(r[0]) > reportMaxPx || absOf(r[1]) > reportMaxPx {
				t.Fatalf("evenReports(%v) emitted %v beyond reportMaxPx %d", c, r, reportMaxPx)
			}
			sx += r[0]
			sy += r[1]
		}
		// The reports carry the GAIN-SCALED request, so their sum must be the
		// commanded distance divided by the host's measured gain.
		wx, wy := scaleByGain(c[0], c[1])
		if wx != 0 || wy != 0 {
			if sx != wx || sy != wy {
				t.Fatalf("evenReports(%v) sums to (%d,%d); want (%d,%d)", c, sx, sy, wx, wy)
			}
		}
		if (c[0] != 0 || c[1] != 0) && len(reps) == 0 {
			t.Fatalf("evenReports(%v) emitted nothing", c)
		}
		// Fewest reports that keep every one inside reportMaxPx.
		longest := maxOf(absOf(wx), absOf(wy))
		want := (longest + reportMaxPx - 1) / reportMaxPx
		if want < 1 && (wx != 0 || wy != 0) {
			want = 1
		}
		if len(reps) != want {
			t.Fatalf("evenReports(%v) = %d reports; want %d", c, len(reps), want)
		}
	}
}

// The pieces must be EQUAL, not "one big report plus a tail": the measured
// failure (4 x 100 px reporting 589 px for 400 commanded) came from reports the
// host accelerated, and an unequal split recreates exactly that shape.
func TestEvenReportsAreEqual(t *testing.T) {
	for _, c := range [][2]int{{160, 0}, {300, 0}, {600, 0}, {-486, 504}, {320, 0}} {
		reps := evenReports(c[0], c[1])
		if len(reps) < 2 {
			continue
		}
		lo, hi := 1<<30, -(1 << 30)
		for _, r := range reps {
			m := maxOf(absOf(r[0]), absOf(r[1]))
			if m < lo {
				lo = m
			}
			if m > hi {
				hi = m
			}
		}
		if hi-lo > 1 {
			t.Fatalf("evenReports(%v) is uneven: reports span %d..%d px", c, lo, hi)
		}
	}
}
