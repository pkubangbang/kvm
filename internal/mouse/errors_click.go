package mouse

import (
	"errors"
)

// ErrNoPosition means the pointer position could not be established.
var ErrNoPosition = errors.New("pointer position unavailable: the shell extension is not publishing")

// ErrStrokeDeadline means a stroke hit its time budget.
var ErrStrokeDeadline = errors.New("stroke deadline reached: the button was released at the position the ink had reached")

// ErrNoMotion means the pointer stopped responding to reports while a leg still
// had distance to cover: the wire is dead, not the arithmetic.
var ErrNoMotion = errors.New("pointer stopped moving: no fresh frame followed the reports")
