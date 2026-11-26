package ch9329

import (
	"bytes"
	"testing"
)

// ParseKey must carry a character's implied Shift into mods, or `kvm key tap`
// sends the unshifted frame. The vectors are the WCH manual's worked examples
// (§2.2.2), the same ground truth frame_test.go uses.
//
// Measured before the fix: `tap A` and `tap a` produced identical frames,
// A/a -> 57 AB 00 02 08 00 00 04 00 00 00 00 00 10, which is the manual's
// plain 'a' example; the manual's Shift+A vector, what the host renders as
// 'A', is 57 AB 00 02 08 02 00 04 00 00 00 00 00 12. So 'A' typed as 'a'.
// This test pins the corrected behaviour.
func TestParseKeyCarriesImpliedShift(t *testing.T) {
	manualADown := []byte{0x57, 0xAB, 0x00, 0x02, 0x08, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10}
	manualShiftADown := []byte{0x57, 0xAB, 0x00, 0x02, 0x08, 0x02, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x12}

	cases := []struct {
		spec  string
		want  []byte
		usage byte
		mods  byte
	}{
		{"a", manualADown, 0x04, 0x00},
		{"A", manualShiftADown, 0x04, ModLeftShift},
		{"shift+a", manualShiftADown, 0x04, ModLeftShift},
		{"shift+A", manualShiftADown, 0x04, ModLeftShift}, // not doubled, same frame
	}
	for _, c := range cases {
		u, m, err := ParseKey(c.spec)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", c.spec, err)
		}
		if u != c.usage || m != c.mods {
			t.Fatalf("ParseKey(%q) = usage 0x%02X mods 0x%02X; want 0x%02X / 0x%02X",
				c.spec, u, m, c.usage, c.mods)
		}
		if got := KeyFrame(m, []byte{u}); !bytes.Equal(got, c.want) {
			t.Fatalf("ParseKey(%q) frame = % X; want % X", c.spec, got, c.want)
		}
	}
}

// Lowercase and unshifted digits are unchanged by the fix: their implied
// shift is false, so mods stays 0 and the frame is the manual's plain form.
func TestParseKeyLeavesUnshiftedKeysAlone(t *testing.T) {
	for _, spec := range []string{"a", "z", "1", "enter", "esc", "tab", "left"} {
		_, m, err := ParseKey(spec)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", spec, err)
		}
		if m&ModLeftShift != 0 {
			t.Fatalf("ParseKey(%q) mods = 0x%02X carries Shift; want none", spec, m)
		}
	}
}

// A shifted symbol resolves to the digit key plus Shift — the relation that
// is NOT derivable from the character's own ASCII value and needs the US
// layout table (shiftedUS).
func TestParseKeyShiftedSymbolsUseTheLayoutTable(t *testing.T) {
	cases := map[string]byte{
		"!": 0x1E, "@": 0x1F, "#": 0x20, "$": 0x21, "%": 0x22,
		"^": 0x23, "&": 0x24, "*": 0x25, "(": 0x26, ")": 0x27,
	}
	for spec, usage := range cases {
		u, m, err := ParseKey(spec)
		if err != nil {
			t.Fatalf("ParseKey(%q): %v", spec, err)
		}
		if u != usage || m != ModLeftShift {
			t.Fatalf("ParseKey(%q) = usage 0x%02X mods 0x%02X; want 0x%02X / Shift",
				spec, u, m, usage)
		}
	}
}
