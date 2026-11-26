package mouse

import "time"

// Keyboard bridge.
//
// The vendored driver already speaks the HID boot keyboard (ch9329.KeyPress /
// KeyDown / KeyUp / TypeText); these four methods are the thin wrapper that
// keeps the keyboard on the SAME serial link as the pointer, so one process can
// place the pointer and then type without reopening the port.
//
// Why a wrapper at all: Device.d is unexported on purpose. A caller that
// reached into it for keyboard frames would bypass the wire log and the
// close guarantee, which is exactly what this type exists to own.

// KeyPress taps one key with modifiers, then releases every key.
func (dv *Device) KeyPress(usage, mods byte) error { return dv.d.KeyPress(usage, mods) }

// KeyHold presses a key and LEAVES IT DOWN. The caller must send KeyRelease
// later; `kvm key down` does so on every exit path for exactly this reason.
func (dv *Device) KeyHold(usage, mods byte) error { return dv.d.KeyDown(usage, mods) }

// KeyRelease sends the all-zero frame: no keys, no modifiers held.
func (dv *Device) KeyRelease() error { return dv.d.KeyUp() }

// TypeText types an ASCII string on a US layout. Non-ASCII runes are skipped
// and counted — see ch9329.Device.TypeText.
func (dv *Device) TypeText(s string, delay time.Duration) (int, error) {
	return dv.d.TypeText(s, delay)
}

// KeyChord presses several keys at once with modifiers, then releases.
func (dv *Device) KeyChord(mods byte, usages ...byte) error { return dv.d.Hotkey(mods, usages...) }
