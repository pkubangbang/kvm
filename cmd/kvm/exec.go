package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"ch9329"
	"github.com/pkubangbang/kvm/internal/cli"
	"github.com/pkubangbang/kvm/internal/mouse"
)

// runMouse is the whole surface: send one sequence.
func runMouse(argv []string, origin bool, portFlag string, baudFlag int) {
	runFire(argv, origin, portFlag, baudFlag)
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

// relHandle is the live controller for the duration of one device session, so
// the end-of-life funnel (fail / signal handler) can neutralize the wire even
// from a code path that has no *Rel in scope. It is set once per open and
// cleared by neutralize.
var relHandle *mouse.Rel

// neutralize drives the wire to a neutral state — all buttons up, no motion in
// flight — and closes the device. It is the ONE owner of end-of-life: normal
// completion, every fail() error path, and the SIGINT/SIGTERM handler all end
// here.
//
// os.Exit does not run deferred functions, so cleanup must be explicit at every
// exit. This used to be spread across fail(), a deferred dv.Release(), scattered
// rel.ForceRelease() calls and a belt-and-braces check at the end of runFire —
// four places, each of which could be bypassed. The button state is then left
// DOWN on the wire: a physically latched drag on a real HID device. The visible
// consequences were a desktop dragged under the pointer, and every later run
// committing nothing because its press was not a press: the button was already
// down from the previous failure.
//
// Rel.Neutralize is idempotent (sync.Once), so calling it here and again from a
// racing signal handler emits the release/zero-delta sequence exactly once.
func neutralize(dv *mouse.Device) {
	if rel := relHandle; rel != nil {
		rel.Neutralize()
		relHandle = nil
	} else if dv != nil {
		// No controller (e.g. the failure happened before NewRel): the device
		// release frame is still the right neutral state.
		dv.Release()
	}
	if dv != nil {
		dv.Close()
	}
}

// fail neutralizes the wire BEFORE exiting, and is the only way out of a path
// that may have a button held or motion in flight.
//
// Release is called explicitly and its error is ignored on purpose: this is the
// last thing the process does, and an error here cannot be reported usefully.
func fail(dv *mouse.Device, code int, format string, args ...any) {
	neutralize(dv)
	fmt.Fprintf(os.Stderr, format, args...)
	os.Exit(code)
}

func runFire(argv []string, origin bool, portFlag string, baudFlag int) {
	cfg := settings(portFlag, baudFlag)
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
		fail(dv, 3, "kvm: %v\n", err)
	}
	// End-of-life is owned by neutralize (called by fail and the signal handler,
	// and at the end of this function on the success path). The old bare
	// defers (dv.Close / dv.Release) never ran on os.Exit and left the button
	// latched — see neutralize's comment.
	rel := mouse.NewRel(dv, mouse.HeldStepPx, mouse.DefaultGap,
		time.Duration(cfg.ClickGap)*time.Millisecond)
	relHandle = rel
	if err := rel.SetButton(buttonMask()); err != nil {
		fail(dv, 2, "kvm: %v\n", err)
	}
	abs := mouse.NewAbs(dv, rel)

	if origin && len(segs) == 1 && segs[0][1] == "rel" {
		fmt.Println("homing to the top-left corner")
		if err := rel.Home(cfg.ScreenW, cfg.ScreenH); err != nil {
			fail(dv, 3, "kvm: home: %v\n", err)
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
		//
		// prevButton/rightSpan track a RIGHT span's button mask and restore it
		// when the span ends. They must live at segment scope (not inside the
		// verb case) so the `up`/`rup` arm can see what the `rdown` arm set.
		var prevButton byte
		rightSpan := false
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
			//
			// THE GATE IS rel.Held(), NOT (rel.Held() && v.Kind == Drag). A
			// single `drag` arrives as verbs[Down, Drag, Up]: `down` is
			// dispatched below and sets rel.Held(), and the very next verb IS a
			// drag, so the run is claimed from there. Requiring rel.Held() at the
			// TOP of the loop instead looked equivalent and was not — it is only
			// reached for verbs after the first, and it also silently turned any
			// unclaimed held verb into "unhandled rel verb". Measured: with the
			// old gate `mouse rel -human=false down drag 120 0 up` printed
			//   kvm: segment 0 verb 1 (drag): unhandled rel verb "drag"
			// as soon as the first drag was a leg of length 0 or the run was
			// entered from the wrong index, and exited 3 while the pointer was
			// still pressed.
			if seq.Rel && v.Kind == cli.Drag {
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
						fail(dv, 3, "kvm: segment %d human path: %v\n", s, err)
					}
					continue
				}
				if err := abs.StrokeHeldPaced(path, gaps); err != nil {
					fail(dv, 3, "kvm: segment %d held path: %v\n", s, err)
				}
				// The flat span consumed the run exactly as the human arm does,
				// so the cursor must jump past it too. This was missing: with no
				// post-statement on the loop, `i` stayed on the FIRST drag,
				// heldRun re-planned the same run every pass, and the stroke was
				// redrawn forever -- pressing and releasing the button each time.
				// Measured: `mouse rel -human=false down drag ... up` with a long
				// run never returned. Advancing here is the whole fix.
				i = end
				continue
			}
			switch {
			case !seq.Rel:
				if err := exec(abs, rel, seq.Rel, v); err != nil {
					fail(dv, 3, "kvm: segment %d verb %d (%s): %v\n", s, i, v.Kind, err)
				}
			case v.Kind == cli.Down, v.Kind == cli.RDown:
				// The verb decides WHICH button is pressed; -button only sets the
				// default that `down` carries. Switching here means one sequence can
				// right-click without changing the mode of the whole command.
				if v.Kind == cli.RDown {
					// A right span must not leak the RIGHT button into a later
					// `down`: the mask lives on the controller, so it is restored
					// when the span ENDS — at its `up`/`rup`, or at the end of the
					// loop if the span never closes.
					//
					// This used to be `defer rel.SetButton(prev)` HERE, which is a
					// defer inside a loop: it fires when runFire RETURNS, not when
					// the span ends. Every `rdown` therefore leaked BtnRight into
					// every later verb, so
					//   kvm mouse rel rdown rup down drag 40 0 up
					// right-clicked and then RIGHT-dragged (a context menu instead
					// of ink) while the comment claimed the opposite. The restore is
					// now explicit state, applied where the span ends.
					prevButton = rel.Button()
					if err := rel.SetButton(ch9329.BtnRight); err != nil {
						fail(dv, 3, "kvm: segment %d verb %d (rdown): %v\n", s, i, err)
					}
					rightSpan = true
				}
				if err := rel.Down(); err != nil {
					fail(dv, 3, "kvm: segment %d verb %d (down): %v\n", s, i, err)
				}
			case v.Kind == cli.Up, v.Kind == cli.RUp:
				// Restore the carried button as the span CLOSES, whatever verb
				// closes it: an explicit `up`/`rup`, or the synthesized terminator
				// Parse appends. Doing it here (rather than in a deferred call at
				// function exit) is what keeps `rdown ... up` from colouring the
				// rest of the command line right.
				if rightSpan {
					if err := rel.SetButton(prevButton); err != nil {
						fmt.Fprintf(os.Stderr, "kvm: segment %d verb %d (restore button): %v\n", s, i, err)
					} else {
						rightSpan = false
					}
				}
				// rel.Up() releases unconditionally, so an `up` after a span
				// that already released (the normal case) is a harmless
				// redundant release rather than a state change.
				if err := rel.Up(); err != nil {
					fail(dv, 3, "kvm: segment %d verb %d (up): %v\n", s, i, err)
				}
			default:
				if err := exec(abs, rel, seq.Rel, v); err != nil {
					fail(dv, 3, "kvm: segment %d verb %d (%s): %v\n", s, i, v.Kind, err)
				}
			}
			i++
		}
	}
	// Belt and braces: the grammar always terminates with `up` (cli.Parse
	// appends one), so arriving here pressed means the grammar and this loop
	// disagree. Never exit with the button down — a stuck HID press turns the
	// user's next click into a drag on their real desktop. neutralize() below
	// is what actually guarantees the neutral wire; this check only reports the
	// disagreement.
	if rel.Held() {
		fmt.Fprintln(os.Stderr, "kvm: button was still held at end of sequence; released")
	}
	// Success path: the same end-of-life funnel as every error path — all
	// buttons up, no motion in flight, device closed. Exactly once (Rel's
	// sync.Once), even if the signal handler fires concurrently.
	neutralize(dv)
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

// buttonOn is the -button switch: which button the held/inking reports carry.
// Left is the default, so every existing invocation is byte-identical.
var buttonOn = "left"

// buttonMask maps buttonOn onto the HID button bits. An unknown name is an
// error, not a silent left-click: a typo must not click the wrong thing.
func buttonMask() byte {
	switch strings.ToLower(buttonOn) {
	case "", "left", "l", "1":
		return ch9329.BtnLeft
	case "right", "r", "2":
		return ch9329.BtnRight
	case "middle", "m", "3":
		return ch9329.BtnMiddle
	}
	fmt.Fprintf(os.Stderr, "kvm: unknown -button %q (want left, right or middle)\n", buttonOn)
	os.Exit(2)
	return 0
}

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
		fail(dv, 3, "kvm: %v\n", err)
	}
	rel := mouse.NewRel(dv, mouse.HeldStepPx, mouse.DefaultGap,
		time.Duration(cfg.ClickGap)*time.Millisecond)
	relHandle = rel
	if err := rel.SetButton(buttonMask()); err != nil {
		fail(dv, 2, "kvm: %v\n", err)
	}
	if err := rel.DrawStep([2]int{nums[0], nums[1]}, [2]int{nums[2], nums[3]}); err != nil {
		fail(dv, 3, "kvm: holdseg: %v\n", err)
	}
	neutralize(dv)
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
func exec(abs *mouse.Abs, rel *mouse.Rel, relative bool, v cli.Verb) error {
	if !relative {
		// absSpecs holds exactly one verb (Move): the namespace is
		// placement-only, so anything else here is a grammar/executor
		// disagreement, not a user error.
		if v.Kind == cli.Move {
			if err := abs.GoTo(v.A, v.B); err != nil {
				return err
			}
			// REPORT WHAT LANDED, NOT WHAT WAS ASKED.
			//
			// Placement used to be a one-way write: the caller asked for a
			// point, the report went out, and nothing said whether the pointer
			// arrived. Everything downstream then assumed it had — and when a
			// later step came up short, the shortfall was attributed to that
			// later step while the real error was an unnoticed miss at the
			// vertex.
			//
			// One read after the move closes that: absolute placement is not in
			// flight, so the witness is honest here in a way it never is
			// mid-stroke (the observer publishes at ~16 ms). A residual is
			// printed whenever it is non-zero, and the requested point is always
			// printed, so a caller can diff target against measured instead of
			// trusting the request.
			if mx, my, ok := mouse.ShellPoint(); ok {
				fmt.Printf("abs move %d,%d -> %d,%d", v.A, v.B, mx, my)
				if rx, ry := mx-v.A, my-v.B; rx != 0 || ry != 0 {
					fmt.Printf("  residual %+d,%+d", rx, ry)
				}
				fmt.Println()
			}
			return nil
		}
		return fmt.Errorf("unhandled abs verb %q", v.Kind)
	}

	switch v.Kind {
	case cli.Move:
		if err := rel.Move(v.A, v.B); err != nil {
			return err
		}
		// Report the measured position, same as the abs path. A relative move
		// is the one place a caller most needs it: the delivered distance
		// depends on report size and pacing, so "asked +20, moved +20" versus
		// "asked +20, moved +15" is the difference between a working loop and
		// a silent shortfall.
		if mx, my, ok := mouse.ShellPoint(); ok {
			fmt.Printf("rel move %+d,%+d -> %d,%d\n", v.A, v.B, mx, my)
		}
		return nil
	case cli.Wait:
		// Grammar-legal in both states (relSpecs[Wait].state == stateAny) but
		// previously executor-illegal: it fell through to "unhandled verb", so
		// `rel down wait 50 drag ...` and `... up wait 50 ...` exited 3 —
		// the first of them with the button already pressed on the wire.
		// A wait never touches the button; it is a pure sleep.
		return rel.Wait(v.A)
	case cli.Scroll:
		// The wheel byte rides the RELATIVE report (0x05), so this is a rel-only
		// verb and absSpecs keeps it out of the abs namespace.
		//
		// SIGN: positive = UP, matching ch9329's wire convention
		// (MouseRelFrame's wheel byte is positive for wheel-up). The executor
		// does not flip it, so `scroll -3` is three notches DOWN — the reading a
		// user expects from a scroll container, where downward travel is the
		// common case and therefore the negative number.
		if err := rel.Wheel(v.A); err != nil {
			return err
		}
		fmt.Printf("scroll %+d notches\n", v.A)
		return nil
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

func absOf(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
