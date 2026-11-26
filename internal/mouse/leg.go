package mouse

import (
	"math"
	"time"
)

// Motion, one controller for every verb.
//
// This is the PLAIN even move: a leg is split into equal deltas whose sum is
// exactly the requested displacement, and they go out back to back at r.Gap.
// There is no gain model and no learned scale: an earlier version divided the
// distance by a measured gain and folded each pass's result back in (see
// PITFALLS.md §5 and the git history of internal/mouse/gain.go). It made the
// pointer land unevenly — the visible symptom was ink that slowed and jumped —
// and it could never be certified, so it was removed on the user's instruction.
//
// A leg is now open loop. Measured on this host (kvm mouse probe): one ACKed
// `rel move 120` at gap 2 travels +120/+121, and a held span sent as a small
// number of in-range reports travels 1:1 too. Evenly spaced reports at the same
// pacing are therefore the whole model; if a host ever disagrees, the leg
// reports the wire endpoint it measured instead of silently compensating.

const (
	legBudget      = 4 * time.Second
	maxVerbs       = 3000
	arriveSlack    = 3
	maxCorrections = 4
	// wireSettle is how long a boundary read waits for the observer to publish
	// the frame that postdates the last report (observer cadence is ~16 ms).
	wireSettle = 300 * time.Millisecond
	// dragGapMS is the pinned report pacing inside a held span. See strokePath:
	// the host delivers a held-span report 1:1 at 2 ms and only ~0.65x at 12 ms.
	dragGapMS = 12
	// settlePasses bounds the post-stroke correction loop.
	settlePasses = 4
	// settleQuiet is how long the wire must hold one position before a
	// correction is planned from it, and wireSettleDeadline bounds the wait.
	settleQuiet        = 120 * time.Millisecond
	wireSettleDeadline = 1500 * time.Millisecond
	// landPasses / landSlack bound the post-segment landing glide (see drawStep):
	// the pointer is corrected with the button UP, one 1-cell slack, so the ink
	// stops where the path stops and no correction stroke is drawn.
	landPasses = 3
	landSlack  = 1
	// NO GAIN DIVISION AND NO SUBDIVISION. Both were wrong for the same
	// reason, and the measurement is unambiguous — the same 300 px commanded
	// distance, delivered, by per-report size:
	//
	//	  60 px x 5  -> 302 px     30 px x 10 -> 293 px
	//	  12 px x 25 -> 322 px      6 px x 50 -> 197 px
	//	   3 px x 100 ->   3 px      1 px x 300 ->   3 px
	//
	// The host delivers a report of ~60 px exactly; below ~12 px it coalesces
	// or drops them, and at 3 px and under it delivers essentially NOTHING.
	// So "small equal reports at a fast gap" is the opposite of what this host
	// does: subdividing a leg is what made it short and made the delivered
	// distance depend on how fast the reports were pushed (which reads as
	// "acceleration" but is really report loss). A single `rel move 0 200`
	// travels 200.
	//
	// Therefore: ONE report per requested delta, no division, no chopping.
	// The only bound left is the protocol's ±127 per report (a HID relative
	// field is one byte), which is applied by splitting a LARGER delta into
	// as few parts as the protocol requires — not into a smoothness stream.
	reportMaxPx = 127
)

// walkTo moves the pointer to target in repeated even passes.
//
// Each pass measures the wire, splits the REMAINING distance into equal reports
// (gain divided out, each report ≤ reportMaxPx) and streams them, so motion is a
// glide of equal small steps and never a single leap. A measured ratio is never
// reused between passes — the host is not perfectly 1:1 — but the residual is
// re-glided, bounded by maxCorrections and legBudget, so the pointer converges
// on the target instead of overshooting it.
func (r *Rel) walkTo(btn byte, target [2]int) error {
	from, ok := r.wire()
	if !ok {
		return ErrNoPosition
	}
	dx, dy := target[0]-from[0], target[1]-from[1]
	if dx == 0 && dy == 0 {
		noteGoto(target[0], target[1])
		return nil
	}
	// ONE emission per request. This used to be a closed loop: measure, glide,
	// measure, glide the residual — up to maxCorrections passes. Every pass
	// re-emitted a distance the host had not finished absorbing (the observer
	// publishes at ~16 ms while reports leave every 2 ms), so the pointer was
	// commanded the same distance more than once. That double-emission IS the
	// acceleration the user reports: a small request amplified far past what
	// was asked, and requests of 1-3 px silently dropped entirely (they were
	// under arriveSlack, so the loop called them "arrived" without emitting).
	// Open loop has no such failure mode: the reports sum to exactly the
	// request, and the endpoint is MEASURED afterwards for the record instead
	// of being chased.
	for _, step := range evenReports(dx, dy) {
		if err := r.streamAt(btn, step[0], step[1], r.Gap); err != nil {
			return err
		}
	}
	if to, ok := r.wire(); ok {
		tracef("emit %v want %d,%d -> wire %v residual %d,%d",
			from, dx, dy, to, target[0]-to[0], target[1]-to[1])
	}
	noteGoto(target[0], target[1])
	return nil
}

// walkPath follows every point of path with the button held.
func (r *Rel) walkPath(btn byte, path Path) error {
	if len(path) < 2 {
		return nil
	}
	for _, p := range path[1:] {
		if err := r.walkTo(btn, p); err != nil && err != ErrStrokeDeadline {
			return err
		}
	}
	return nil
}

// strokePath draws path in ONE down..up span: the press, every report of every
// segment, and the release go out back to back at the drag pacing, with no
// measurement in between. A stroke is judged after it is complete (the caller
// verifies the endpoint once); reading the wire mid-stroke is the mistake this
// replaces, because the observer publishes at ~16 ms while reports leave every
// few ms, so every mid-stroke reading is stale by construction and each
// correction it provoked became a visible jag in the ink.
//
// The drag pacing is PINNED at dragGapMS rather than taken from r.Gap. The
// delivered distance on this host depends on the report cadence while the
// button is held: the same 160 px draw measured 1.04x at 2 ms, 0.89x at 6 ms
// and 0.65x at 12 ms, so a caller that speeds up the gap would silently shorten
// every stroke. Reports stay small (reportMaxPx) so the ink glides.
func (r *Rel) strokePath(path Path) error {
	gap := r.Gap
	if gap <= 0 {
		gap = time.Duration(dragGapMS) * time.Millisecond
	}
	// The page observes the pointer once per display frame (~16.7 ms), so the
	// sample density of a recorded stroke is set by how long the stroke is in
	// flight, not by how many reports are pushed into each frame. Measured: a
	// 0.35 s Z recorded 20 samples whether it went out as 75 reports (step 4)
	// or 300 (step 1); a frame's worth of reports collapses into one event.
	// Held spans therefore honour the caller's -gap so a stroke can be paced
	// across as many frames as the caller needs.
	if len(path) < 2 {
		return nil
	}
	if err := r.downRaw(); err != nil {
		return err
	}
	for i := 1; i < len(path); i++ {
		dx, dy := path[i][0]-path[i-1][0], path[i][1]-path[i-1][1]
		for _, st := range evenReports(dx, dy) {
			if err := r.streamAt(r.btn, st[0], st[1], gap); err != nil {
				_ = r.upRaw()
				return err
			}
			if err := r.stallGuard(); err != nil {
				// The movement is CANCELLED, not paused: release the button
				// and stop. Continuing would keep pressing with nothing
				// moving, which on a drag turns the user's next click into
				// more of this stroke.
				_ = r.upRaw()
				return err
			}
		}
	}
	return r.upRaw()
}

// drawStep draws ONE short segment of a stroke: it presses, streams the
// segment's reports, releases, and then lands the pointer on the segment end
// exactly.
//
// It exists because the single-span stroke has two defects that are visible in
// the ink and were never certifiable for handwriting (measured on this host):
//
//  1. strokePath never measures its own endpoint, so a stroke's delivered
//     length shortens stroke by stroke: a teacher-copied 577 px bowl arrived as
//     408 px and stopped 10 cells short of its rightmost point.
//  2. a `rel drag` is exact in LENGTH but not in DIRECTION. A 100 % horizontal
//     drag still records ~3 cells of vertical drift and a vertical drag ~3
//     cells of horizontal drift (three horizontal 300 px legs recorded y
//     +15/-16/+4). Chained into one long span these drift legs read as shallow
//     diagonal segments, i.e. an EXTRA CORNER the path never had — the user's
//     complaint "你多了一个折角".
//
// Small per-segment steps keep the drift inside one or two cells, and because
// the button is re-pressed at every waypoint the host's held-span gain never
// accumulates. The endpoint is corrected AFTER the release (button up) so the
// correction is a pointer move, not ink.
func (r *Rel) drawStep(a, b [2]int) error {
	if err := r.DrawStep(a, b); err != nil {
		return err
	}
	return nil
}

// landOn glides the pointer onto target with the button UP, so whatever
// residual the last span left is corrected outside the ink. It gives up after
// landPasses rather than fighting the host, and reports the residual it could
// not remove.
func (r *Rel) landOn(target [2]int) error {
	// NO POST-STROKE CORRECTION.
	//
	// This used to read the pointer back and re-walk it to the target, up to
	// landPasses times. That compensation belonged to a world where the host
	// applied acceleration and the delivered distance had to be pulled back.
	// With the non-acceleration law honoured — bounded vector steps, ACK-paced,
	// exact sum — a stroke already ends where its last report ended, so the
	// correction could only ADD motion. That is the visible jump after a click
	// that commanded no movement at all. Open loop, end to finish.
	_ = target
	return nil
}

// strokePathPaced is strokePath with a per-report cadence: gaps[i] is the wait
// after the report reaching path[i+1] (0 = the pinned drag pacing).
func (r *Rel) strokePathPaced(path Path, gaps []time.Duration) error {
	if len(path) < 2 {
		return nil
	}
	if err := r.downRaw(); err != nil {
		return err
	}
	for i := 1; i < len(path); i++ {
		dx, dy := path[i][0]-path[i-1][0], path[i][1]-path[i-1][1]
		gap := time.Duration(dragGapMS) * time.Millisecond
		if i-1 < len(gaps) && gaps[i-1] > 0 {
			gap = gaps[i-1]
		}
		for _, st := range evenReports(dx, dy) {
			if err := r.streamAt(r.btn, st[0], st[1], gap); err != nil {
				_ = r.upRaw()
				return err
			}
			if err := r.stallGuard(); err != nil {
				_ = r.upRaw()
				return err
			}
		}
	}
	return r.upRaw()
}

// stallGuard is the STALL PREVENTION: one report has just been emitted, and
// this asks whether the pointer actually advanced as a result. It returns
// ErrStalled once the pointer has failed to advance across maxStallReports
// CONSERVATIVE comparisons, and the caller CANCELS the movement (releasing the
// button) rather than continuing to stream.
//
// THE CONTRACT, precisely, because an off-by-one here is the difference between
// a guard and a nuisance: the first call only takes a BASELINE; each later call
// compares against it. A frozen pointer therefore needs maxStallReports+1 calls
// to be cancelled — one to learn where the pointer is, then maxStallReports
// silent reports. A movement of fewer reports than that can never be cancelled,
// which is correct: nothing short of maxStallReports+1 reports can prove a
// stall, and cancelling on less would abort legitimate short strokes (measured:
// a 60 px held drag emits only 3 reports).
//
// WHY THIS EXISTS SEPARATELY FROM loopTolerance. loopTolerance is a
// per-placement acceptance window used by the closed-loop paths; it says
// nothing about an OPEN-LOOP emitter, which has no idea whether its reports
// are doing anything — it just counts down the bytes it owes. At a screen edge
// the pointer physically cannot advance, so those reports are no-ops, and an
// unbounded emitter would keep pressing at the edge for the whole remaining
// displacement. The reference implementation on Linux aborts a movement once
// the pointer fails to advance for N consecutive reports; this is that rule.
//
// It is deliberately NOT a per-report check against an absolute target: the
// observer publishes at ~16 ms while reports leave every ~2 ms, so a report
// emitted now may legitimately not be visible yet. maxStallReports covers that
// lag with room to spare, and the counter RESETS on any advance — a slow but
// real stream is never cancelled, only one that has genuinely stopped.
func (r *Rel) stallGuard() error {
	var x, y int
	var ok bool
	if r.witness != nil {
		p, got := r.witness()
		x, y, ok = p[0], p[1], got
	} else {
		x, y, ok = WirePoint(wireSettle)
	}
	if !ok {
		// NO WITNESS IS NOT A STALL. A platform without a cursor feed (or a
		// feed that is briefly dead) cannot prove nothing moved, and
		// cancelling on absent evidence would abort every stroke on that
		// platform. The byte budget and LetterBudget remain the bounds.
		return nil
	}
	cur := [2]int{x, y}
	if !r.stallSeen {
		r.stallSeen = true
		r.stallFrom = cur
		return nil
	}
	if cur != r.stallFrom {
		r.stallFrom = cur
		r.stallSilent = 0
		return nil
	}
	r.stallSilent++
	if r.stallSilent >= maxStallReports {
		return ErrStalled
	}
	return nil
}

// settleHeldWithin is settleHeldSlow bounded by an explicit remaining budget:
// the caller owns the letter's wall clock, so the correction stops when the
// budget is gone instead of spending a fixed number of passes.
// wireSettled returns the pointer position once the wire has stopped changing,
// which is the only reading a correction may be planned from.
func (r *Rel) wireSettled() ([2]int, bool) {
	prev, ok := r.wire()
	if !ok {
		return prev, false
	}
	deadline := time.Now().Add(wireSettleDeadline)
	for time.Now().Before(deadline) {
		time.Sleep(settleQuiet / 4)
		cur, ok := r.wire()
		if !ok {
			continue
		}
		if cur != prev {
			prev = cur
			continue
		}
		return cur, true
	}
	return prev, true
}

// evenReports turns a requested displacement into the HID reports to send.
//
// MEASUREMENT-DRIVEN (see the constant block): this host delivers a relative
// report 1:1 at sizes from ~12 px up to 60 px+, and delivers essentially
// nothing at 3 px or below — so "many small equal reports" is exactly the
// wrong shape for it. The reports are therefore as FEW and as LARGE as the
// protocol allows: one report per request, split only where the one-byte HID
// field (max ±127) forces a split.
//
// The user's correction on the earlier note: the host does not swallow
// reports — the reader is slower than the writer, so a tight stream of tiny
// reports arrives coalesced. Fewer, larger reports sidestep the reader cadence
// entirely instead of trying to outrun it.
// REPORT-PER-VECTOR CAP. The chip's byte is 127 per axis, but a host does not
// see axes — it sees a vector, and a (100,100) report moves the pointer 141 px
// in one frame. Held motion is only 1:1 while the step stays small (measured on
// this rig: 20 px -> 22, 40 px -> 60, i.e. 1.5x once the step grows), so the
// split is bounded by the VECTOR length, not by max(|dx|,|dy|).
//
// The user's rule makes this concrete: never let the pointer jump during a
// drag, and never leave the no-acceleration band. 20 px per report is inside
// that band on both counts; with the 8 ms transport floor it also caps sustained
// speed at ~2.5 kpx/s, which is the price of never being accelerated.
const HeldStepPx = 20 // exported so the CLI dry-run sizes its estimate with the same constant the emitter uses

// DefaultGap is the report pacing every emitter gets without being asked.
//
// Pacing is not an option here, it is the thing that makes the pointer land
// where it was told. The host accelerates a held stream, and that measured
// factor (~1.1x on an unheld report, more on a held one) is acceleration rather
// than loss — so it is avoided by cadence, never compensated by dividing the
// request. Removing the old -gap flag left this at zero, so held spans fell
// back to a hard-coded 2 ms and delivered short. A default restores correct
// pacing and takes the chance-to-forget-it away: there is no flag to leave out
// and no way to run without a gap.
const DefaultGap = 12 * time.Millisecond

func evenReports(dx, dy int) [][2]int {
	cmdX, cmdY := scaleByGain(dx, dy)
	if cmdX == 0 && cmdY == 0 {
		return nil
	}
	// Split by the LENGTH OF THE VECTOR, so every emitted report is a step of
	// at most heldStepPx pixels of actual travel in ANY direction. A diagonal is
	// the case that matters: bounding by max(|dx|,|dy|) let (141,141) through as
	// one report — 199 px of travel — while claiming to respect a 127 px limit.
	dist := int(math.Sqrt(float64(cmdX)*float64(cmdX) + float64(cmdY)*float64(cmdY)))
	n := (dist + HeldStepPx - 1) / HeldStepPx
	if n < 1 {
		n = 1
	}
	if n > maxVerbs {
		n = maxVerbs
	}
	out := make([][2]int, 0, n)
	fx, fy := 0, 0
	for i := 1; i <= n; i++ {
		tx := cmdX * i / n
		ty := cmdY * i / n
		out = append(out, [2]int{tx - fx, ty - fy})
		fx, fy = tx, ty
	}
	return out
}

// scaleByGain is the identity: there is no gain to divide out.
//
// It used to divide each axis by a measured constant (1.51 / 1.00 / 1.44 /
// 1.75 at various points — the number kept moving because it was compensating
// for report SHAPE, not for a host gain). A single `rel move 0 200` travels
// 200 and a 60 px report travels 60, so the host gain is 1.0 and the only
// thing that was ever wrong was how the leg got chopped up.
func scaleByGain(dx, dy int) (int, int) { return dx, dy }

// roundHalf rounds to the nearest integer (away from zero at .5).
func roundHalf(f float64) float64 {
	if f < 0 {
		return -float64(int(-f + 0.5))
	}
	return float64(int(f + 0.5))
}

// wire is one ring sample, bounded; false means the observer is not publishing.
func (r *Rel) wire() ([2]int, bool) {
	// The settle window must outlast the report pacing: at the default 12 ms gap
	// the last report of a glide is still being absorbed when a 50 ms wait ends,
	// and a caller that then treats "no fresh frame" as failure aborts a move
	// that landed exactly where it was asked to.
	//
	// It only ever SHORTENS the wait, never blocks: a long wait here is a poll
	// loop that makes every placement slow (and slow placement is what a stale
	// ring made look like acceleration). When the feed is healthy (fresh mtime,
	// non-zero heartbeat) the first Position() already answers, so the ceiling
	// only bites on a genuinely dead feed — and a dead feed is an error to
	// report, not a reason to wait a second per sample.
	x, y, ok := WirePoint(wireSettle)
	return [2]int{x, y}, ok
}

// goPoint drives the pointer onto (x,y) with the button up.
func (r *Rel) goPoint(x, y int) error {
	clearTracked()
	return r.walkTo(0, [2]int{x, y})
}

// followPath walks every waypoint with the button held, one leg per waypoint.
func (r *Rel) followPath(path Path) error {
	if len(path) == 0 {
		return nil
	}
	if _, ok := r.wire(); !ok {
		return ErrNoPosition
	}
	return r.walkPath(r.btn, path)
}

// goAbs is the absolute path: the same walk, no button.
func (r *Rel) goAbs(x, y int) error {
	if err := r.walkTo(0, [2]int{x, y}); err != nil {
		clearTracked()
		return err
	}
	noteGoto(x, y)
	return nil
}

// StrokeHeldPaced follows a path with the button held, taking the report
// cadence from gaps: gaps[i] is how long to wait after the report that reaches
// path[i+1], and 0 means "use the span's default pacing".
//
// This is the handwriting path. The pad samples once per display frame, so a
// stroke's SAMPLE DENSITY is set by how long it is in flight — not by how many
// reports are pushed into a frame (analysis.md, measured). A caller that
// reproduces a human's speed therefore has to control the time between reports,
// which the plain span (fixed dragGapMS) cannot express.
//
// ONE CALL IS ONE LETTER: exactly one down..up span, and the whole span
// (reports + landing) is bounded by LetterBudget measured FROM THE PRESS. A
// caller that would overrun is truncated in TIME, not in space: the reports
// still go out in order, the pacing is compressed proportionally, and the span
// still ends with a release. Overrunning is reported as ErrLetterBudget so the
// caller knows the letter was not drawn at the requested speed — the user's
// rule is that a letter costs at most 5 s, not that it is silently dropped.
func (a *Abs) StrokeHeldPaced(path Path, gaps []time.Duration) error {
	if len(path) < 2 {
		return nil
	}
	// Plan the time BEFORE the press: a budget enforced only after the fact
	// would leave a stuck button for the overrun duration.
	planned := spanDuration(path, gaps)
	scale := 1.0
	if planned > LetterBudget {
		scale = float64(LetterBudget) / float64(planned)
		gaps = compressGaps(gaps, scale)
	}
	start := time.Now()
	if err := a.rel.strokePathPaced(path, gaps); err != nil {
		return err
	}
	// THE SPAN IS OVER. strokePathPaced already released the button, and the
	// page closed the stroke on that release. Any correction that presses again
	// is a NEW stroke — and that is exactly what the pad trace showed: the Z's
	// three legs arrived as one stroke, then a second 35-point stroke walked
	// backward up the right-hand edge (x 57..61, y 56..88, heading -90°/-135°),
	// which is this landing being drawn as ink. Two strokes where the caller
	// asked for one, and a shape that is not the letter.
	//
	// The residual it was chasing is real but belongs to a BUTTON-UP move; it is
	// the caller's job (the Zibra-Z procedure lands with `abs move` after the
	// span). Correcting it here cannot be distinguished from ink, so it is not
	// done: one call, one down..up span, nothing after the release.
	if mx, my, ok := ShellPoint(); ok {
		a.dv.px, a.dv.py = mx, my
	}
	if time.Since(start) > LetterBudget {
		return ErrLetterBudget
	}
	return nil
}

// spanDuration is the wall-clock the reports of path will take at the pacing
// gaps asks for: the sum of the reports' pacing, with the default drag pacing
// wherever the caller left a 0.
func spanDuration(path Path, gaps []time.Duration) time.Duration {
	total := time.Duration(0)
	for i := 1; i < len(path); i++ {
		gap := time.Duration(dragGapMS) * time.Millisecond
		if i-1 < len(gaps) && gaps[i-1] > 0 {
			gap = gaps[i-1]
		}
		n := len(evenReports(path[i][0]-path[i-1][0], path[i][1]-path[i-1][1]))
		total += time.Duration(n) * gap
	}
	return total
}

// compressGaps scales every non-zero pacing by f so a span that would overrun
// fits the budget. Report COUNT is untouched: compression changes how fast the
// pointer advances, never how far, so a truncated letter is short in time only
// and its ink still ends on the same target.
func compressGaps(gaps []time.Duration, f float64) []time.Duration {
	if len(gaps) == 0 {
		return gaps
	}
	out := make([]time.Duration, len(gaps))
	for i, g := range gaps {
		if g <= 0 {
			out[i] = 0
			continue
		}
		s := time.Duration(float64(g) * f)
		if s < time.Millisecond {
			s = time.Millisecond
		}
		out[i] = s
	}
	return out
}

// StrokeHeldFast follows a path with the button already held: one leg per
// waypoint, the same motion as StrokeHeld without the redraw retries.
func (a *Abs) StrokeHeldFast(path Path) error {
	if len(path) < 2 {
		return nil
	}
	if err := a.rel.strokePath(path); err != nil {
		return err
	}
	// NO SETTLE. The stroke used to be corrected here, with the button still
	// held, because the host's span gain (measured 1.03-1.06) left the endpoint
	// a few percent past the last corner. That gain is exactly what the
	// non-acceleration law removed — bounded vector steps, ACK-paced, exact
	// sum — so the correction can only add motion the caller never asked for.
	return nil
}
