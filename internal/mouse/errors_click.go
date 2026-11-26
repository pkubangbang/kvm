package mouse

import (
	"errors"
	"strconv"
)

// ErrNoPosition means the pointer position could not be established.
var ErrNoPosition = errors.New("pointer position unavailable: the shell extension is not publishing")

// ErrStrokeDeadline means a stroke hit its time budget.
var ErrStrokeDeadline = errors.New("stroke deadline reached: the button was released at the position the ink had reached")

// ErrNoMotion means the pointer stopped responding to reports while a leg still
// had distance to cover: the wire is dead, not the arithmetic.
var ErrNoMotion = errors.New("pointer stopped moving: no fresh frame followed the reports")

// ErrStalled means the pointer did not advance for maxStallReports consecutive
// reports, so the MOVEMENT WAS CANCELLED — not deferred, not shortened. It
// replaces ErrNoMotion on the open-loop emitters, which have no distance
// feedback of their own and would otherwise keep pressing at a screen edge (or
// against any place the pointer cannot move) until their byte budget ran out.
// A caller seeing it must treat the stroke as INCOMPLETE: the ink is whatever
// the pointer last reached, and the button has already been released.
var ErrStalled = errors.New("movement cancelled: the pointer stopped advancing for " +
	strconv.Itoa(maxStallReports) + " consecutive reports")
