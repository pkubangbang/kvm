//go:build windows

package mouse

// pressRidesFirstMove defers a zero-motion button frame (btn != 0, dx == dy ==
// 0) so the press rides the first MOVING report instead.
//
// Why Windows differs from Linux here: the inputpad page ends a stroke on any
// pointermove whose `buttons` is 0. A standalone press frame reaches it as
// exactly that — a move with no buttons — so the stroke is committed at press
// time and every ink report after it is orphaned. Measured on this rig as
// strokes.jsonl entries with points: 1..3 for multi-hundred-pixel legs.
//
// Linux is left at false ON PURPOSE: the Linux motion path is byte-identical
// to what shipped, per the standing instruction not to change it.
const pressRidesFirstMove = true
