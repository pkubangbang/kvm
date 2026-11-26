package ch9329

// HID Usage ID table (USB HID Usage Tables 1.12, Keyboard/Keypad page 0x07).
// Only keys that a controller plausibly needs are listed; hex constants keep
// the mapping auditable against the spec instead of a magic array.

var namedKeys = map[string]byte{
	"enter": 0x28, "return": 0x28,
	"esc": 0x29, "escape": 0x29,
	"backspace": 0x2A,
	"tab":       0x2B,
	"space":     0x2C, "spacebar": 0x2C,
	"minus": 0x2D, "equal": 0x2E,
	"lbracket": 0x2F, "rbracket": 0x30,
	"backslash": 0x31, "hash": 0x32, "semicolon": 0x33,
	"quote": 0x34, "grave": 0x35, "comma": 0x36,
	"period": 0x37, "dot": 0x37, "slash": 0x38,
	"capslock": 0x39,
	"home":     0x4A, "pageup": 0x4B, "delete": 0x4C, "del": 0x4C,
	"end": 0x4D, "pagedown": 0x4E, "insert": 0x49,
	"right": 0x4F, "left": 0x50, "down": 0x51, "up": 0x52,
	"numlock": 0x53, "printscreen": 0x46, "pause": 0x48,
	"f1": 0x3A, "f2": 0x3B, "f3": 0x3C, "f4": 0x3D, "f5": 0x3E, "f6": 0x3F,
	"f7": 0x40, "f8": 0x41, "f9": 0x42, "f10": 0x43, "f11": 0x44, "f12": 0x45,
}

// letter/digit base usages: 'a' -> 0x04 ... 'z' -> 0x1D, '1' -> 0x1E ... '9' -> 0x26, '0' -> 0x27
func letterUsage(r rune) (byte, bool) {
	switch {
	case r >= 'a' && r <= 'z':
		return byte(0x04 + (r - 'a')), true
	case r >= 'A' && r <= 'Z':
		return byte(0x04 + (r - 'A')), true
	case r >= '1' && r <= '9':
		return byte(0x1E + (r - '1')), true
	case r == '0':
		return 0x27, true
	}
	return 0, false
}

// shiftedUS maps a printable US-layout character to its base usage and
// whether it needs the Left-Shift modifier.
var shiftedUS = map[rune]struct {
	usage byte
	shift bool
}{
	'!': {0x1E, true}, '@': {0x1F, true}, '#': {0x20, true}, '$': {0x21, true},
	'%': {0x22, true}, '^': {0x23, true}, '&': {0x24, true}, '*': {0x25, true},
	'(': {0x26, true}, ')': {0x27, true},
	' ': {0x2C, false}, '\t': {0x2B, false}, '\n': {0x28, false},
	'-': {0x2D, false}, '_': {0x2D, true},
	'=': {0x2E, false}, '+': {0x2E, true},
	'[': {0x2F, false}, '{': {0x2F, true},
	']': {0x30, false}, '}': {0x30, true},
	'\\': {0x31, false}, '|': {0x31, true},
	';': {0x33, false}, ':': {0x33, true},
	'\'': {0x34, false}, '"': {0x34, true},
	'`': {0x35, false}, '~': {0x35, true},
	',': {0x36, false}, '<': {0x36, true},
	'.': {0x37, false}, '>': {0x37, true},
	'/': {0x38, false}, '?': {0x38, true},
}

// ParseKey resolves a key spec to (usage, modifiers). Accepted forms:
//
//	"a" "A" "1" "/"        a literal printable character (US layout)
//	"enter" "esc" "tab"    a name from the table above (case-insensitive)
//	"ctrl" "shift" "alt" "win"  a lone modifier -> usage 0, the bit set
//	"ctrl+w" "shift+tab"   a chord: modifiers before '+', one final key
//
// A character's IMPLIED shift is folded into mods. keyUsage already reports
// it (its second result: 'A' and '!' are shifted legends of the same physical
// keys as 'a' and '1'), and TypeText has always consumed it; ParseKey used to
// discard it, so a caller that sent ParseKey's result put the unshifted frame
// on the wire. Measured: `tap A` and `tap a` produced byte-identical frames
// (57 AB 00 02 08 00 00 04 ... 10), the manual's plain 'a' example, while the
// manual's Shift+A vector is 57 AB 00 02 08 02 00 04 ... 12. 'A' typing as 'a'
// is what that looked like to the user.
//
// An EXPLICIT prefix wins: "shift+a" and "A" resolve to the same frame, and a
// prefix that already sets LeftShift is not doubled up.
func ParseKey(spec string) (usage byte, mods byte, err error) {
	m, key, ok := splitChord(spec)
	if ok {
		mods = m
		spec = key
	}
	u, shift, err := keyUsage(spec)
	if err != nil {
		return u, mods, err
	}
	if shift && mods&ModLeftShift == 0 {
		mods |= ModLeftShift
	}
	return u, mods, nil
}

// splitChord peels leading "mod+" segments off a spec.
func splitChord(spec string) (mods byte, rest string, found bool) {
	for {
		i := indexByte(spec, '+')
		if i <= 0 || i == len(spec)-1 {
			return mods, spec, found
		}
		head := lowerASCII(spec[:i])
		var bit byte
		switch head {
		case "ctrl", "control", "lctrl":
			bit = ModLeftCtrl
		case "shift", "lshift":
			bit = ModLeftShift
		case "alt", "lalt":
			bit = ModLeftAlt
		case "win", "lwin", "meta", "super", "cmd":
			bit = ModLeftWin
		case "rctrl":
			bit = ModRightCtrl
		case "rshift":
			bit = ModRightShift
		case "ralt":
			bit = ModRightAlt
		default:
			if v, isNamed := namedKeys[head]; isNamed {
				// e.g. "f1+..." is not a chord modifier; treat the whole spec as a key.
				_ = v
				return mods, spec, found
			}
			return mods, spec, found
		}
		mods |= bit
		found = true
		spec = spec[i+1:]
	}
}

// keyUsage resolves a single key name/character, reporting whether Shift is
// implied by the character (callers that build a whole report use it).
func keyUsage(spec string) (usage byte, shift bool, err error) {
	if spec == "" {
		return 0, false, errEmptyKey
	}
	if len(spec) == 1 {
		r := rune(spec[0])
		if u, ok := letterUsage(r); ok {
			if r >= 'A' && r <= 'Z' {
				return u, true, nil
			}
			return u, false, nil
		}
		if e, ok := shiftedUS[r]; ok {
			return e.usage, e.shift, nil
		}
	}
	l := lowerASCII(spec)
	if v, ok := namedKeys[l]; ok {
		return v, false, nil
	}
	switch l {
	case "ctrl", "control":
		return 0, false, nil
	case "shift":
		return 0, false, nil
	case "alt":
		return 0, false, nil
	case "win", "meta", "super":
		return 0, false, nil
	}
	return 0, false, errUnknownKey(spec)
}

type keyError string

func (e keyError) Error() string { return string(e) }

const errEmptyKey = keyError("empty key spec")

func errUnknownKey(s string) error {
	return keyError("unknown key spec: " + s)
}

func indexByte(s string, c byte) int {
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			return i
		}
	}
	return -1
}

func lowerASCII(s string) string {
	b := []byte(s)
	for i := range b {
		if b[i] >= 'A' && b[i] <= 'Z' {
			b[i] += 32
		}
	}
	return string(b)
}
