package mouse

import (
	"time"
)

const (
	loopTolerance = 1

	// maxStallReports is the STALL GUARD: the number of consecutive relative
	// reports a movement may send without the pointer advancing before the
	// whole movement is CANCELLED rather than continued.
	//
	// It exists because "the pointer cannot move further" is a NORMAL state,
	// not an error: at a screen edge, under a pointer lock, or when the host
	// starts coalescing the stream, the remaining displacement simply never
	// arrives. Without this bound the open-loop emitters keep sending reports
	// that change nothing until their byte budget runs out — the button stays
	// DOWN while nothing moves, which on a drag means the user's next real
	// click becomes a continuation of a phantom stroke.
	//
	// The reference implementation (the Linux branch of this rig) cancels the
	// movement once the pointer fails to advance for N reports; this is that
	// same rule, with N = 8: at the 8 ms transport floor that is ~64 ms of no
	// progress, comfortably longer than the observer's ~16 ms publication lag
	// and far shorter than a hand's reaction time.
	maxStallReports = 8

	// LetterBudget is the HARD wall-clock cap on ONE letter, measured from the
	// press. A hand writes one letter as ONE held down..up span, and the user's
	// rule is that no letter may take longer than this — so the span aborts,
	// RELEASES, and reports ErrLetterBudget instead of inking on past the cap.
	//
	// Without it a mis-parsed path or a stalled host turns a 1.5 s stem into an
	// unbounded held press: the button stays down for as long as the report
	// stream lasts, which is both the wrong stroke and a stuck physical button.
	LetterBudget = 5 * time.Second
)
