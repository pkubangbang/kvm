package mouse

import "errors"

// ErrNotDown is returned when a drag is attempted with the button up.
var ErrNotDown = errors.New("drag requires the button to be DOWN")

// ErrLetterBudget reports that one letter's held span spent its whole
// LetterBudget. It is returned AFTER the button has been released: the span is
// compressed to fit the budget, drawn to its last target, and released, and
// only then declared over-budget — the caller learns the letter was drawn too
// slowly (the reports could not be paced as asked) without ever seeing a stuck
// button or a truncated stroke.
var ErrLetterBudget = errors.New("letter exceeded its 5 s budget")

// ErrOversize was declared but never returned: no caller composes a frame wider
// than one report any more (streamAt/stream CLAMP to ±127 instead of failing, so
// the excess is dropped, not reported). Deleted rather than left as a sentinel
// that cannot be produced — a caller checking for it would be checking a lie.
// If an oversize frame must ever be an error, return it from the composer, not
// from a hope.

// ErrNotImplemented marks API surface the PoC has not yet exercised.
var ErrNotImplemented = errors.New("not implemented yet")

// ErrNoDisplay is returned when the pointer cannot be relocated because there is
// no usable display connection (no DISPLAY / no X server).
var ErrNoDisplay = errors.New("no display connection: cannot place the pointer")

// ErrWarpNeedsWalk is returned when a session cannot warp the pointer and no
// relative controller is wired to travel to the target instead.
var ErrWarpNeedsWalk = errors.New("no X11 warp and no relative controller: cannot place the pointer")

// ErrAbsWhileHeld is returned when absolute placement is asked for while a
// stroke holds the button: the 0x04 report carries its own button mask, so it
// would replace a press this call does not own. Placement is a button-up verb.
var ErrAbsWhileHeld = errors.New("abs placement requires the button to be UP")
