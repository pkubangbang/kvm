package main

import (
	"testing"

	"github.com/pkubangbang/kvm/internal/cli"
)

// A held run of drags is ONE span: `down`, many `drag`s, `up`. Parsing gives
// [Down, Drag xN, Up]. heldRun must claim every drag and report the index of
// the FIRST verb it did not claim -- the terminating `up`. If that index is
// wrong, the exec loop never advances past the first drag and the whole run is
// replayed on every pass, forever, pressing and releasing the button each time.
//
// That is not hypothetical: the flat (-human=false) arm used to `continue`
// without setting `i = end`, so `mouse rel -human=false down drag .. up` never
// returned and the user's cursor ran away across the pad.
func TestHeldRunClaimsEveryDragAndStopsOnUp(t *testing.T) {
	verbs := []cli.Verb{
		{Kind: cli.Down},
		{Kind: cli.Drag, A: 5, B: 0},
		{Kind: cli.Drag, A: 5, B: 1},
		{Kind: cli.Drag, A: 5, B: -1},
		{Kind: cli.Up},
	}

	path, gaps, end := heldRun(verbs, 1)

	if end != 4 {
		t.Fatalf("heldRun end = %d, want 4 (the index of `up`)", end)
	}
	if got, want := len(path), 4; got != want {
		t.Fatalf("path has %d points, want %d (start + one per drag)", got, want)
	}
	if got, want := len(gaps), 3; got != want {
		t.Fatalf("gaps has %d entries, want %d (one per drag)", got, want)
	}
	if verbs[end].Kind != cli.Up {
		t.Fatalf("verb at end is %v, want Up", verbs[end].Kind)
	}

	// The legs must actually accumulate from the start point.
	start := path[0]
	if path[3][0]-start[0] != 15 || path[1][1]-start[1] != 0 {
		t.Fatalf("legs did not accumulate: start %v end %v", start, path[3])
	}
}

// The regression itself, expressed as the loop's own contract: starting at the
// first drag and stepping `i = end` must walk the verb list to completion in
// exactly two visits (the run, then the `up`) -- not loop on index 1 forever.
func TestHeldRunAdvancesCursorToTheUp(t *testing.T) {
	verbs := []cli.Verb{
		{Kind: cli.Down},
		{Kind: cli.Drag, A: 4, B: 0},
		{Kind: cli.Drag, A: 4, B: 0},
		{Kind: cli.Up},
	}

	i := 1
	runs := 0 // how many times the held-run branch is entered
	for i < len(verbs) {
		if runs > 10 {
			t.Fatalf("cursor never advanced past index %d: the run is replayed forever", i)
		}
		if verbs[i].Kind != cli.Drag {
			break
		}
		runs++
		_, _, end := heldRun(verbs, i)
		if end <= i {
			t.Fatalf("heldRun returned end=%d for start=%d: no progress", end, i)
		}
		i = end // this is the line whose absence caused the runaway mouse
	}

	if runs != 1 {
		t.Fatalf("entered the held-run branch %d times, want exactly 1", runs)
	}
	if i != len(verbs)-1 {
		t.Fatalf("ended at index %d, want %d (the `up`)", i, len(verbs)-1)
	}
}

// A `wait` inside a span is a pause, not a leg: it must extend the previous
// gap and must not terminate the run.
func TestHeldRunTreatsWaitAsAPause(t *testing.T) {
	verbs := []cli.Verb{
		{Kind: cli.Drag, A: 6, B: 0},
		{Kind: cli.Wait, A: 40},
		{Kind: cli.Drag, A: 6, B: 0},
		{Kind: cli.Up},
	}

	path, gaps, end := heldRun(verbs, 0)

	if end != 3 {
		t.Fatalf("heldRun end = %d, want 3 (the `up`)", end)
	}
	if len(path) != 3 {
		t.Fatalf("path has %d points, want 3 (wait adds no leg)", len(path))
	}
	if len(gaps) != 2 {
		t.Fatalf("gaps has %d entries, want 2", len(gaps))
	}
	if gaps[0] == 0 {
		t.Fatalf("the wait did not extend the preceding gap: %v", gaps)
	}
}
