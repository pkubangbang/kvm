package main

// The dry-run report: what WOULD happen to the pointer, in screen space, with
// two boxes the caller can check against its safe zone before anything moves.
//
// Why this exists: a generated command line can be catastrophically wrong and
// still parse (a runaway leg is legal grammar). Without a report the first
// feedback is the pointer physically leaving the safe zone — measured here as
// the mouse "wandering on the screen" while lettering playback compounded a
// per-step-delta bug into drags of 10^12 px. The dry run makes that visible
// BEFORE a single frame is written.
//
// Reported:
//
//	bbox          the rectangle every planned point falls in (full extent)
//	hot85 bbox    the smallest rectangle holding the densest 85% of time
//	              spent (see hottest85): where the pointer actually dwells
//
// The hot85 box is the honest one for "did it stay in the safe zone": the
// full bbox includes single runaway legs that pass through in one report,
// while the hot box shows where the motion mass is.

import (
	"fmt"
	"os"

	"github.com/pkubangbang/kvm/internal/cli"
	"github.com/pkubangbang/kvm/internal/mouse"
)

// planPoint is one simulated pointer sample in screen space.
type planPoint struct{ x, y int }

// simulate walks the parsed verbs the way runFire would, accumulating the
// pointer's screen-space track. An abs move sets the position outright; rel
// moves and drags are deltas; the first position is seeded from the witness
// (ShellPoint) when known, else (0,0) with absolute moves anchoring it.
func simulate(segs [][]string, origin bool) []planPoint {
	track := []planPoint{}
	cx, cy := 0, 0
	havePos := false
	if x, y, ok := mouse.ShellPoint(); ok {
		cx, cy, havePos = x, y, true
	}
	if origin {
		cx, cy, havePos = 0, 0, true
	}
	seed := func() {
		if !havePos {
			track = append(track, planPoint{cx, cy})
			havePos = true
		}
	}
	relRun := false
	_ = relRun
	// Mirror runFire's reshape (exec.go:88-92): cli.Parse wants `mouse
	// abs|rel <verbs>` — ONE namespace per call. splitOn's segments are raw
	// argv fragments that can mix namespaces (`mouse abs move X Y rel down
	// ...`), which Parse rejects ("unknown verb \"rel\" for abs mode"), so
	// the whole segment was dropped silently. Split each fragment at its
	// `abs`/`rel` namespace tokens and parse each namespace run separately.
	for si, seg := range segs {
		for _, part := range splitNamespaceRuns(seg) {
			if len(part) == 0 {
				continue
			}
			if part[0] != "mouse" {
				part = append([]string{"mouse"}, part...)
			}
			seq, err := cli.Parse(part, cli.Config{ScreenW: 1 << 30, ScreenH: 1 << 30})
			if err != nil {
				fmt.Fprintf(os.Stderr, "kvm: dry-run segment %d: %v\n", si, err)
				continue
			}
		// `down`/`rdown` OPEN a span and `up`/`rup` close it — the span count
		// the report prints. Count them here (runFire's dispatch keys on the
		// same verbs), so "0 spans" for a plan made of strokes is visibly
		// wrong instead of silently accepted.
		rel := seq.Rel
		for _, v := range seq.Verbs {
			switch {
			case !rel && v.Kind == cli.Move:
				cx, cy = v.A, v.B
				havePos = true
				track = append(track, planPoint{cx, cy})
			case rel && v.Kind == cli.Move:
				seed()
				cx, cy = cx+v.A, cy+v.B
				track = append(track, planPoint{cx, cy})
			case rel && v.Kind == cli.Drag:
				// A drag run is one span, but every leg's END is a real
				// pointer position: the sum of the deltas is exactly where the
				// pointer lands. Walk each leg's endpoints — that is what the
				// emitter's reports integrate to. But a `down` is a POINT, not
				// a motion: only the move/drag arms set `havePos`, or every
				// span would be simulated relative to the process-start
				// witness instead of the span's own `abs move` anchor — the
				// exact "wandering" bug this report exists to catch.
				seed()
				if !relRun {
					relRun = true
				}
				cx, cy = cx+v.A, cy+v.B
				track = append(track, planPoint{cx, cy})
			case rel && v.Kind == cli.Up || rel && v.Kind == cli.RUp:
				relRun = false
				seed()
				track = append(track, planPoint{cx, cy})
			case rel && (v.Kind == cli.Down || v.Kind == cli.RDown):
				seed()
				track = append(track, planPoint{cx, cy})
			case rel && v.Kind == cli.Wait:
				seed()
				track = append(track, planPoint{cx, cy})
			}
		}
		}
	}
	return track
}

// splitNamespaceRuns splits a raw argv fragment at its `abs`/`rel` namespace
// tokens so each run is exactly one namespace — the shape cli.Parse accepts.
// `mouse abs move X Y rel down drag 0 10 up` becomes
// [`abs move X Y`, `rel down drag 0 10 up`]. The leading `mouse` token and
// `;;` separators (already split by splitOn) are passed through positionally.
func splitNamespaceRuns(seg []string) [][]string {
	runs := [][]string{}
	cur := []string{}
	for i := 0; i < len(seg); i++ {
		t := seg[i]
		if t == "abs" || t == "rel" {
			if len(cur) > 0 {
				runs = append(runs, cur)
			}
			cur = []string{t}
			continue
		}
		if t == "mouse" {
			// Namespace token lives at argv[1] after the caller's prepend;
			// drop any stray `mouse` inside the fragment.
			continue
		}
		cur = append(cur, t)
	}
	if len(cur) > 0 {
		runs = append(runs, cur)
	}
	return runs
}

// bbox is the axis-aligned rectangle of the track.
func bbox(track []planPoint) (x1, y1, x2, y2 int) {
	if len(track) == 0 {
		return
	}
	x1, y1, x2, y2 = track[0].x, track[0].y, track[0].x, track[0].y
	for _, p := range track[1:] {
		if p.x < x1 {
			x1 = p.x
		}
		if p.x > x2 {
			x2 = p.x
		}
		if p.y < y1 {
			y1 = p.y
		}
		if p.y > y2 {
			y2 = p.y
		}
	}
	return
}

// hottest85 is the smallest-area rectangle holding the densest 85% of the
// pointer's DWELL TIME, in sample counts. With the emitter's fixed pacing,
// samples are a time proxy: a leg is entered at every one of its subdivided
// reports, so long slow spans dominate and one runaway teleport (a single
// sample crossing half the track) cannot inflate the box.
//
// The sliding window is over the sample sequence: for each window holding
// >= 85% of the samples, take its bbox and keep the smallest. It is O(n^2) on
// the window scan but tracks here are command-line-sized (hundreds of
// samples), and the definition — "where the pointer spent 85% of its time" —
// is exactly what a safe-zone check needs.
func hottest85(track []planPoint, frac float64) (x1, y1, x2, y2 int) {
	n := len(track)
	if n == 0 {
		return
	}
	want := int(float64(n)*frac + 0.5)
	if want < 1 {
		want = 1
	}
	if want > n {
		want = n
	}
	best := -1
	for w := want; w <= n; w++ {
		for i := 0; i+w <= n; i++ {
			a, b, c, d := bbox(track[i : i+w])
			area := (b - a + 1) * (d - c + 1)
			if best < 0 || area < best {
				best = area
				x1, y1, x2, y2 = a, b, c, d
			}
		}
	}
	return
}

// reportDryRun prints the predict-mode plan: the point count, the full bbox
// and the 85%-hottest bbox, so the caller can diff them against its safe zone
// before firing. One block, parseable: the two lines are `bbox x1 y1 x2 y2`
// and `hot85 bbox x1 y1 x2 y2`.
func reportDryRun(segs [][]string, origin bool, screenW, screenH int) {
	track := simulate(segs, origin)
	if len(track) == 0 {
		fmt.Println("dry-run: no motion planned")
		return
	}
	x1, y1, x2, y2 := bbox(track)
	hx1, hy1, hx2, hy2 := hottest85(track, 0.85)
	fmt.Printf("dry-run: %d planned points, %d spans\n", len(track), countSpans(segs))
	fmt.Printf("bbox %d %d %d %d\n", x1, y1, x2, y2)
	fmt.Printf("hot85 bbox %d %d %d %d\n", hx1, hy1, hx2, hy2)
	// Safe-zone verdict against the configured screen: a single line the
	// caller can grep. "unknown" when the screen size is unknown.
	if screenW > 0 && screenH > 0 {
		if x1 < 0 || y1 < 0 || x2 >= screenW || y2 >= screenH {
			fmt.Printf("safe-zone: OUTSIDE 0 0 %d %d\n", screenW-1, screenH-1)
		} else {
			fmt.Printf("safe-zone: inside 0 0 %d %d\n", screenW-1, screenH-1)
		}
	}
}

// countSpans counts the down..up spans the parse will produce, for context.
func countSpans(segs [][]string) int {
	n := 0
	for _, seg := range segs {
		seq, err := cli.Parse(seg, cli.Config{ScreenW: 1 << 30, ScreenH: 1 << 30})
		if err != nil {
			continue
		}
		for _, v := range seq.Verbs {
			if v.Kind == cli.Down || v.Kind == cli.RDown {
				n++
			}
		}
	}
	return n
}
