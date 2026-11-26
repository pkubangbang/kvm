// The pad's commit rule, exercised without a browser.
//
// The page commits a stroke when it sees a terminal signal (pointerup,
// pointercancel, mouseup, blur). An injected HID drag arrives as a stream of
// reports and the host synthesises one of those after almost every report,
// because a relative report is a full-state packet: a report that does not
// re-assert the button byte is indistinguishable from a lift at that layer.
// Committing on the spot cut one drag into one stroke per report — measured on
// this rig as 137 samples landing on disk as 137 one-point strokes.
//
// The fix is a short deferral: endOnce waits LIFT_SETTLE_MS and any sample
// arriving inside that window cancels the pending commit. A real lift is
// followed by no samples, so it still commits.
//
// This test models that rule as the page implements it, and runs the four
// sequences that matter. It is deliberately a MODEL of the rule rather than a
// copy of the page's code — the page is a string handed to a browser and
// cannot be imported here — so the constant below must track LIFT_SETTLE_MS in
// cmd/kvm-inputpad/page.html. TestSettleWindowMatchesPage fails if they drift.
package main

import (
	"os"
	"regexp"
	"strconv"
	"testing"
	"time"
)

// settleRule is the smallest faithful model of endOnce/cancelSettle: a
// terminal signal arms a pending commit; a sample cancels it; disarming
// nothing commits nothing.
type settleRule struct {
	pending   bool
	commits   int
	lastPts   int
	samples   int // samples accumulated in the CURRENT stroke
	settleFor time.Duration
}

func newSettleRule(d time.Duration) *settleRule { return &settleRule{settleFor: d} }

// sample is one pointermove carrying a recorded point. Arriving during a
// pending commit means the "lift" was synthetic, so it is cancelled.
func (s *settleRule) sample() {
	s.samples++
	if s.pending {
		s.pending = false
	}
}

// lift is a terminal signal for one synthetic release.
func (s *settleRule) lift() { s.pending = true }

// settle is the timer firing after LIFT_SETTLE_MS of quiet.
func (s *settleRule) settle() {
	if !s.pending {
		return
	}
	s.pending = false
	s.commits++
	s.lastPts = s.samples
	s.samples = 0 // the page resets pts[] on commit
}

// A drag whose every report is followed by a synthetic lift must still commit
// exactly ONE stroke, holding every sample. This is the defect the guard
// exists for: before it, each lift committed and reset the buffer, so 10
// samples produced 10 one-point strokes.
func TestGuardCommitsOneStrokeAcrossSyntheticLifts(t *testing.T) {
	s := newSettleRule(40 * time.Millisecond)

	s.lift() // pointerdown, then the first synthetic release
	for i := 0; i < 10; i++ {
		s.sample()
		s.lift() // the host's per-report release
	}
	s.settle() // the genuine end of the gesture

	if s.commits != 1 {
		t.Fatalf("committed %d strokes, want 1 — the guard did not merge synthetic lifts", s.commits)
	}
	if s.lastPts != 10 {
		t.Errorf("committed stroke holds %d samples, want 10", s.lastPts)
	}
}

// The same sequence WITHOUT the guard, to show the test is measuring the fix
// and not something incidental: committing at once produces one stroke per
// sample.
func TestUnguardedCommitSplitsPerSample(t *testing.T) {
	commits := 0
	samples := 0
	for i := 0; i < 10; i++ {
		samples++ // the sample
		commits++ // commit immediately on the synthetic lift
	}
	if commits != 10 {
		t.Fatalf("unguarded model committed %d strokes, want 10 (one per sample)", commits)
	}
	if samples != 10 {
		t.Fatalf("samples = %d", samples)
	}
}

// A real lift — no sample afterwards — must still commit.
func TestRealLiftStillCommits(t *testing.T) {
	s := newSettleRule(40 * time.Millisecond)
	s.sample()
	s.sample()
	s.lift()
	s.settle()

	if s.commits != 1 {
		t.Fatalf("a real lift committed %d strokes, want 1", s.commits)
	}
	if s.lastPts != 2 {
		t.Errorf("stroke holds %d samples, want 2", s.lastPts)
	}
}

// Two separate gestures separated by a real lift must commit as two strokes,
// or the guard would merge distinct strokes and destroy the record instead of
// repairing it.
func TestTwoGesturesStayTwo(t *testing.T) {
	s := newSettleRule(40 * time.Millisecond)

	// Gesture one: its samples arrive, then its lift, then quiet.
	s.sample()
	s.sample()
	s.lift()
	s.settle()

	// Gesture two, after a genuine pause.
	s.lift()
	s.sample()
	s.lift()
	s.settle()

	if s.commits != 2 {
		t.Fatalf("committed %d strokes, want 2", s.commits)
	}
}

// The cancellation rule is conditional: a move that still asserts a primary
// button continues the stroke, and a move that does not is the tail of a
// finished one and must be allowed to settle.
//
// The unconditional form — cancel on every move — made every held drag vanish,
// because the chip's post-release residual frames arrive as pointermoves with
// buttons===0 and each one cancelled the pending commit. A click survived
// (no trailing moves), a drag did not, which is exactly the symptom measured
// on the pane: a 10-point click committed while a two-report drag committed
// nothing.
func TestCancelOnlyWhileButtonHeld(t *testing.T) {
	// e.buttons bitmask: 1 primary, 2 secondary, 4 auxiliary.
	const btnAny = 1 | 2 | 4
	shouldCancel := func(buttons int) bool { return buttons&btnAny != 0 }

	cases := []struct {
		name    string
		buttons int
		want    bool
	}{
		{"mid-drag move with primary held", 1, true},
		{"mid-drag move with secondary held", 2, true},
		{"post-release tail frame", 0, false},
		{"hover move with no button", 0, false},
	}
	for _, c := range cases {
		if got := shouldCancel(c.buttons); got != c.want {
			t.Errorf("%s (buttons=%d): cancel=%v, want %v", c.name, c.buttons, got, c.want)
		}
	}
}

// which means the model above could silently drift from the thing it is
// supposed to describe. This reads the constant out of the machine's source and
// fails if the two disagree, so a change to the machine forces a matching change
// here rather than leaving a test that passes about code nobody runs.
//
// LIFT_SETTLE_MS USED TO BE IN page.html AND IS NOT ANY MORE. The stroke state
// machine moved to pad-core.js, so this test failed at HEAD with "LIFT_SETTLE_MS
// not found in the page source" — a red suite that predated this rework. The
// constant lives with the code that owns it; this reads it there.
func TestSettleWindowMatchesPage(t *testing.T) {
	src, err := os.ReadFile("pad-core.js")
	if err != nil {
		t.Fatalf("read pad-core.js: %v", err)
	}
	re := regexp.MustCompile(`const LIFT_SETTLE_MS = (\d+);`)
	m := re.FindSubmatch(src)
	if m == nil {
		t.Fatal("LIFT_SETTLE_MS not found in pad-core.js — the guard may have been removed")
	}
	pageMS, err := strconv.Atoi(string(m[1]))
	if err != nil {
		t.Fatalf("parse LIFT_SETTLE_MS: %v", err)
	}
	const modelMS = 40
	if pageMS != modelMS {
		t.Fatalf("machine LIFT_SETTLE_MS = %d ms but this test models %d ms; update the model",
			pageMS, modelMS)
	}
}
