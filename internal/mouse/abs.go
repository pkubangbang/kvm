package mouse

// Abs is the absolute API set: screen pixels as the caller's unit.
//
// Despite the name, nothing here sends an absolute (0x04) frame. Measured on
// this host: a 0x04 frame carries MOTION but its button byte is silently
// dropped (0 BTN_LEFT events across a whole absolute drag), and the placement it
// performs is not honoured under XWayland either. So placement is a RELATIVE
// WALK in screen pixels (see Abs.GoTo, which owns the policy), and every report
// that inks goes through the Rel namespace.
//
// Abs therefore has no press verbs of its own to rely on: the stroke verbs that
// remain (DragTo / Stroke / Press / ClickAt) are thin wrappers that drive
// Rel.Down / Rel.followPath / Rel.Up. Keep the two job families separate in the
// CLI: place with `abs`, draw with `rel`.
type Abs struct {
	dv  *Device
	rel *Rel // placement only needs it for the walk; no button work lives here
}

// NewAbs builds the absolute controller. rel is the frame source for button
// work; the two types stay separate, they are only wired together here.
func NewAbs(dv *Device, rel *Rel) *Abs { return &Abs{dv: dv, rel: rel} }

// Wheel scrolls; the wheel byte rides on a relative report.
func (a *Abs) Wheel(n int) error { return a.rel.Wheel(n) }

// Wait pauses.
func (a *Abs) Wait(ms int) error { return a.rel.Wait(ms) }
