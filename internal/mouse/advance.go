package mouse

import (
	"time"
)

const (
	loopTolerance = 1

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
