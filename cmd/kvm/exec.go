package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/pkubangbang/kvm/internal/cli"
	"github.com/pkubangbang/kvm/internal/config"
	"github.com/pkubangbang/kvm/internal/mouse"
)

// runMouse is the whole surface: predict or send one sequence.
func runMouse(argv []string, dryRun, ease bool, stepFlag, gapFlag int, origin bool, portFlag string, baudFlag int) {
	// -dry-run splits here, ONCE. runPlan is the whole predict path and never
	// opens the device, so runFire carries no dry-run branch of its own.
	if dryRun {
		runPlan(argv, ease, stepFlag)
		fmt.Println("dry-run: nothing sent")
		return
	}
	runFire(argv, ease, stepFlag, gapFlag, origin, portFlag, baudFlag)
}

// runPlan validates the sequence and prints what it would send — including the
func runPlan(argv []string, ease bool, stepFlag int) {
	cfg := config.Load()
	step := stepOf(cfg, stepFlag)
	seq, err := cli.Parse(argv, cli.Config{ScreenW: cfg.ScreenW, ScreenH: cfg.ScreenH})
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("plan: %s mode, %d verbs, step %d px, ease=%v\n", ns(seq.Rel), len(seq.Verbs), step, ease)
	px, py := 0, 0
	for i, v := range seq.Verbs {
		switch v.Kind {
		case cli.Move:
			if seq.Rel {
				frames := framesFor(v.A, v.B, step)
				fmt.Printf("  %2d %-5s %+4d %+4d -> ~%d reports, ends %+d %+d\n",
					i, v.Kind, v.A, v.B, frames, v.A, v.B)
				px += v.A
				py += v.B
			} else {
				fmt.Printf("  %2d %-5s %4d %4d -> instant jump\n", i, v.Kind, v.A, v.B)
				px, py = v.A, v.B
			}
		case cli.Drag:
			// Only the rel branch is reachable: absSpecs holds Move alone
			// (cli/parse.go), so an abs `drag` is a parse error long before
			// this switch. The old abs arm here printed "eased glide" for a
			// verb the grammar cannot produce — removed rather than kept as
			// a plausible-looking lie about -ease.
			frames := framesFor(v.A, v.B, step)
			fmt.Printf("  %2d %-5s %+4d %+4d -> ~%d reports, ends %+d %+d\n",
				i, v.Kind, v.A, v.B, frames, px+v.A, py+v.B)
			px += v.A
			py += v.B
		case cli.Wait:
			// Printed because it is a real part of the pacing: a stroke's
			// sample density is set by time in flight, so a plan that omits
			// the waits is not the plan that will be sent.
			fmt.Printf("  %2d %-5s %d ms\n", i, v.Kind, v.A)
		case cli.Down, cli.Up:
			fmt.Printf("  %2d %-5s button %s\n", i, v.Kind, strings.ToUpper(string(v.Kind)))
		}
	}
	fmt.Printf("  ends at %+d %+d (relative to start)\n", px, py)
}

func splitOn(argv []string) [][]string {
	out := [][]string{}
	cur := []string{}
	for _, a := range argv {
		if a == ";;" {
			if len(cur) > 0 {
				out = append(out, cur)
			}
			cur = []string{}
			continue
		}
		cur = append(cur, a)
	}
	if len(cur) > 0 {
		out = append(out, cur)
	}
	return out
}

func runFire(argv []string, ease bool, stepFlag, gapFlag int, origin bool, portFlag string, baudFlag int) {
	cfg := settings(portFlag, baudFlag)
	step := stepOf(cfg, stepFlag)
	segs := splitOn(argv)
	for i := 1; i < len(segs) && len(argv) > 0; i++ {
		if len(segs[i]) > 0 && segs[i][0] != "mouse" {
			segs[i] = append([]string{argv[0]}, segs[i]...)
		}
	}
	for _, seg := range segs {
		if _, err := cli.Parse(seg, cli.Config{ScreenW: cfg.ScreenW, ScreenH: cfg.ScreenH}); err != nil {
			fmt.Fprintf(os.Stderr, "kvm: %v\n", err)
			os.Exit(1)
		}
	}

	dv, err := open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: %v\n", err)
		os.Exit(3)
	}
	defer dv.Close()
	defer dv.Release()

	gap := cfg.GapMS
	if gapFlag > 0 {
		gap = gapFlag
	}
	if os.Getenv("KVM_GAP_MS") != "" {
		if n, err := strconv.Atoi(os.Getenv("KVM_GAP_MS")); err == nil && n >= 0 {
			gap = n
		}
	}
	rel := mouse.NewRel(dv, step, time.Duration(gap)*time.Millisecond,
		time.Duration(cfg.ClickGap)*time.Millisecond)
	abs := mouse.NewAbs(dv, rel)

	if origin && len(segs) == 1 && segs[0][1] == "rel" {
		fmt.Println("homing to the top-left corner")
		if err := rel.Home(cfg.ScreenW, cfg.ScreenH); err != nil {
			fmt.Fprintf(os.Stderr, "kvm: home: %v\n", err)
			os.Exit(3)
		}
	}

	// There is deliberately NO shadow "buttonHeld" flag any more. The button's
	// state is owned in exactly one place: Rel.down, which is derived from what
	// was actually written to the wire (see Rel.release). A second copy in this
	// file could only drift from it, and a stale `true` would both (a) route an
	// independent `move` into a held span and (b) make the error path believe a
	// release was needed when it was not — or believe it was not when it was.
	// rel.Held() is that one source of truth.
	for s, seg := range segs {
		seq, err := cli.Parse(seg, cli.Config{ScreenW: cfg.ScreenW, ScreenH: cfg.ScreenH})
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvm: %v\n", err)
			os.Exit(1)
		}
		// No post-statement: the cursor is advanced deliberately at the bottom,
		// and the held-run branch jumps it past the whole run. Using `for i++`
		// and a `continue` there would double-advance and swallow the `up`.
		for i := 0; i < len(seq.Verbs); {
			v := seq.Verbs[i]
			// While the button is held, a run of drag verbs is ONE stroke: a
			// single down..up span whose reports go out back to back at the
			// requested gap, with no measurement in between. Correcting per
			// waypoint put a ~16 ms observer inside a ~2 ms report stream, and
			// every correction it produced became a visible kink in the ink.
			//
			// `move` is NOT part of this run: relSpecs[Move].state is stateUp,
			// so a held `move` is a parse error and can never reach us. Testing
			// for it here was the bug that used to swallow the terminating `up`.
			if seq.Rel && rel.Held() && v.Kind == cli.Drag {
				path, gaps, end := heldRun(seq.Verbs, i)
				// HUMAN MOTION. A held run of drags is drawn with the
				// accelerate/cruise/decelerate profile (dwell at every corner,
				// variable step size, small tremor) instead of equal reports at
				// one fixed gap. The flat stream read as machine motion: no
				// corner slows, no step varies, and the ink had no hand in it.
				//
				// -human 0 restores the old flat emitter for A/B comparison.
				if humanOn {
					// The span consumed the run, so jump the cursor to the first
					// verb NOT claimed — normally the synthesized `up`. An explicit
					// index, advanced in one place, is what keeps that `up`
					// dispatched: the old `i--; break` relied on the outer `i++`
					// cancelling it, so it would have left the button held the
					// moment Parse stopped appending its terminating `up`.
					i = end
					if err := rel.HumanStroke(path, mouse.DefaultHuman()); err != nil {
						fmt.Fprintf(os.Stderr, "kvm: segment %d human path: %v\n", s, err)
						rel.ForceRelease()
						os.Exit(3)
					}
					continue
				}
				if err := abs.StrokeHeldPaced(path, gaps); err != nil {
					fmt.Fprintf(os.Stderr, "kvm: segment %d held path: %v\n", s, err)
					rel.ForceRelease()
					os.Exit(3)
				}
				continue
			}
			switch {
			case !seq.Rel:
				if err := exec(abs, rel, seq.Rel, v, ease); err != nil {
					fmt.Fprintf(os.Stderr, "kvm: segment %d verb %d (%s): %v\n", s, i, v.Kind, err)
					if rel.Held() {
						rel.ForceRelease()
					}
					os.Exit(3)
				}
			case v.Kind == cli.Down:
				if err := rel.Down(); err != nil {
					fmt.Fprintf(os.Stderr, "kvm: segment %d verb %d (down): %v\n", s, i, err)
					os.Exit(3)
				}
			case v.Kind == cli.Up:
				// rel.Up() releases unconditionally, so an `up` after a span
				// that already released (the normal case) is a harmless
				// redundant release rather than a state change.
				if err := rel.Up(); err != nil {
					fmt.Fprintf(os.Stderr, "kvm: segment %d verb %d (up): %v\n", s, i, err)
					rel.ForceRelease()
					os.Exit(3)
				}
			default:
				if err := exec(abs, rel, seq.Rel, v, ease); err != nil {
					fmt.Fprintf(os.Stderr, "kvm: segment %d verb %d (%s): %v\n", s, i, v.Kind, err)
					if rel.Held() {
						rel.ForceRelease()
					}
					os.Exit(3)
				}
			}
			i++
		}
	}
	// Belt and braces: the grammar always terminates with `up` (cli.Parse
	// appends one), so arriving here pressed means the grammar and this loop
	// disagree. Never exit with the button down — a stuck HID press turns the
	// user's next click into a drag on their real desktop.
	if rel.Held() {
		rel.ForceRelease()
		fmt.Fprintln(os.Stderr, "kvm: button was still held at end of sequence; released")
	}
}

// heldRun turns the held run starting at i into a path, a per-report gap
// schedule, and the index of the first verb the run did NOT claim.
//
// path[0] is the newest ring sample (the span's origin); every following point
// is the running sum of the run's drag deltas, so a stroke is planned from a
// real position instead of an assumed origin. gaps[k] is the wait to apply after
// the reports that reach path[k+1]; 0 means "use the span's default pacing".
//
// A `wait` between two drags is the caller's cadence for that report (see
// analysis.md: a stroke's sample density is set by TIME IN FLIGHT, so the gap
// has to be expressed between reports). Waits are stripped from the path and
// replayed as pacing; consecutive waits ACCUMULATE, because two pauses are a
// longer pause, not the second one alone.
//
// If the ring is not publishing there is no measured origin, but the span only
// ever uses DIFFERENCES between consecutive points, so an assumed origin of
// (0,0) is still exact — every delta comes out the same. What must never happen
// is omitting the origin entirely: then path[0] is a DELTA, the first drag is
// swallowed by the differencing, and a one-drag run produces a one-point path
// that strokePathPaced no-ops on (len < 2), i.e. the stroke does not go out at
// all. Seeding unconditionally costs nothing and removes both.
// humanOn is the -human switch: true draws held runs with the human motion
// profile, false keeps the flat equal-report emitter for A/B comparison.
var humanOn = true

func heldRun(verbs []cli.Verb, i int) (mouse.Path, []time.Duration, int) {
	cx, cy := 0, 0
	if x, y, ok := mouse.ShellPoint(); ok {
		cx, cy = x, y
	}
	path := mouse.Path{{cx, cy}}
	gaps := []time.Duration{}
	for ; i < len(verbs); i++ {
		w := verbs[i]
		if w.Kind == cli.Wait {
			if len(gaps) > 0 {
				gaps[len(gaps)-1] += time.Duration(w.A) * time.Millisecond
			}
			continue
		}
		if w.Kind != cli.Drag {
			break
		}
		last := path[len(path)-1]
		path = append(path, [2]int{last[0] + w.A, last[1] + w.B})
		gaps = append(gaps, 0)
	}
	return path, gaps, i
}

// runHeldSegment draws ONE straight held segment from abs point a to abs point
// c, then lands the pointer on c with the button up: `kvm mouse abs holdseg X Y U V`.
//
// It is the certifiable unit of a handwriting stroke. A single long held span
// (down, many drags, up) is not: its reports are chained through the closed-loop
// Drag, whose origin sample is ~16 ms stale while the pointer moves, so the ink
// drifts perpendicular to the commanded leg and consecutive legs become extra
// corners. One short open-loop segment per waypoint keeps the drift inside a
// cell, and the post-release landing removes whatever the segment left behind.
func runHeldSegment(argv []string, portFlag string, baudFlag int) {
	if len(argv) != 4 {
		fmt.Fprintf(os.Stderr, "usage: kvm mouse abs holdseg X Y U V\n")
		os.Exit(2)
	}
	nums := make([]int, 4)
	for i, a := range argv {
		n, err := strconv.Atoi(a)
		if err != nil {
			fmt.Fprintf(os.Stderr, "kvm: holdseg argument %q is not an integer\n", a)
			os.Exit(2)
		}
		nums[i] = n
	}
	cfg := settings(portFlag, baudFlag)
	dv, err := open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: %v\n", err)
		os.Exit(3)
	}
	defer dv.Close()
	defer dv.Release()
	rel := mouse.NewRel(dv, stepOf(cfg, 0), time.Duration(cfg.GapMS)*time.Millisecond,
		time.Duration(cfg.ClickGap)*time.Millisecond)
	if err := rel.DrawStep([2]int{nums[0], nums[1]}, [2]int{nums[2], nums[3]}); err != nil {
		fmt.Fprintf(os.Stderr, "kvm: holdseg: %v\n", err)
		os.Exit(3)
	}
}

// exec runs ONE non-held verb. It is called only for verbs the held-run merge
// branch (runFire) did not claim.
//
// INVARIANT — rel `drag` NEVER reaches here. The grammar makes `drag` legal only
// inside a down..up span (relSpecs[Drag].state == stateDown, cli/parse.go), and
// the only verb that opens a span is `down`, which the caller executes through
// rel.Down() (which sets Rel.down). So whenever a rel `drag` is parsed,
// rel.Held() is already true and the merge branch at runFire's
// `seq.Rel && rel.Held() && v.Kind == cli.Drag` takes it. An `up` that could
// clear the latch is exactly what makes a following `drag` a parse error, so the
// two cannot diverge inside a segment — and across `;;` segments the parse latch
// restarts at false, so a cross-segment drag is also a parse error.
//
// The deleted branch used to read `return abs.DragTo(v.A, v.B)`, which would
// have been the one motion law this package documents as wrong: Abs.DragTo ->
// Abs.Stroke -> followPath -> walkTo, a CLOSED loop that reads the ring (~16 ms
// stale) between reports while the pointer is still moving, injecting the
// perpendicular drift that shows up as extra corners in the ink. It was
// unreachable, so nothing regressed by removing it; keeping it was a trap for
// whoever revived it.
func exec(abs *mouse.Abs, rel *mouse.Rel, relative bool, v cli.Verb, ease bool) error {
	if !relative {
		// absSpecs holds exactly one verb (Move): the namespace is
		// placement-only, so anything else here is a grammar/executor
		// disagreement, not a user error.
		if v.Kind == cli.Move {
			return abs.GoTo(v.A, v.B)
		}
		return fmt.Errorf("unhandled abs verb %q", v.Kind)
	}

	switch v.Kind {
	case cli.Move:
		return rel.Move(v.A, v.B)
	case cli.Wait:
		// Grammar-legal in both states (relSpecs[Wait].state == stateAny) but
		// previously executor-illegal: it fell through to "unhandled verb", so
		// `rel down wait 50 drag ...` and `... up wait 50 ...` exited 3 —
		// the first of them with the button already pressed on the wire.
		// A wait never touches the button; it is a pure sleep.
		return rel.Wait(v.A)
	}
	// cli.Down and cli.Up are handled by the caller (so Rel's held state is
	// updated by exactly the two verbs that change it), and cli.Drag is claimed
	// by the merge branch per the invariant above. Reaching here means the
	// caller's dispatch and this switch disagree.
	return fmt.Errorf("unhandled rel verb %q", v.Kind)
}

func ns(rel bool) string {
	if rel {
		return "rel"
	}
	return "abs"
}

// framesFor is the dry-run's report estimate for one leg: the leg split into
func framesFor(dx, dy, step int) int {
	if step < 1 {
		step = 1
	}
	n := (absOf(dx) + step - 1) / step
	if m := (absOf(dy) + step - 1) / step; m > n {
		n = m
	}
	if n < 1 {
		n = 1
	}
	return n
}

func absOf(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
