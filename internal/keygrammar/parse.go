// Package keygrammar holds the keyboard verb grammar: a pure validator, in the
// same spirit as internal/cli — no hardware, no clock, no I/O. A malformed
// keyboard command must never reach the chip.
//
// The verb set is deliberately small and closed:
//
//	kvm key type TEXT...        type the arguments literally, as one string
//	kvm key tap SPEC [SPEC...]  press and release each key spec in turn
//	kvm key down SPEC           hold a key spec (no release)
//	kvm key up                  release every key and modifier
//
// TEXT is taken verbatim from the remaining arguments, so quoting decides
// spaces: `kvm key type hello world` types "hello world" (the arguments are
// joined with a single space), and `kvm key type "hello world"` types the same
// thing. There is no escape syntax: the shell's quoting is the escape syntax.
package keygrammar

import "fmt"

// Kind identifies a keyboard verb.
type Kind string

const (
	Type Kind = "type"
	Tap  Kind = "tap"
	Down Kind = "down"
	Up   Kind = "up"
)

// Command is one validated keyboard command.
type Command struct {
	Kind  Kind
	Text  string   // Type only: the literal string to type
	Specs []string // Tap: one key spec per chord; Down: exactly one
}

// Parse validates argv WITHOUT the program name, starting at "key".
func Parse(argv []string) (Command, error) {
	if len(argv) < 2 {
		return Command{}, fmt.Errorf("usage: kvm key type TEXT | tap SPEC... | down SPEC | up")
	}
	if argv[0] != "key" {
		return Command{}, fmt.Errorf("unknown command %q (expected \"key\")", argv[0])
	}
	switch Kind(argv[1]) {
	case Type:
		if len(argv) < 3 {
			return Command{}, fmt.Errorf("at token 2: %q needs text to type", argv[1])
		}
		return Command{Kind: Type, Text: join(argv[2:])}, nil
	case Tap:
		if len(argv) < 3 {
			return Command{}, fmt.Errorf("at token 2: %q needs at least one key spec", argv[1])
		}
		return Command{Kind: Tap, Specs: append([]string(nil), argv[2:]...)}, nil
	case Down:
		// Exactly one spec: a chord is itself one spec ("ctrl+w"), and two
		// separate specs would be two different held keys, which the boot
		// keyboard can do but no caller here needs.
		if len(argv) != 3 {
			return Command{}, fmt.Errorf("at token 2: %q needs exactly one key spec (got %d)", argv[1], len(argv)-2)
		}
		return Command{Kind: Down, Specs: []string{argv[2]}}, nil
	case Up:
		if len(argv) != 2 {
			return Command{}, fmt.Errorf("at token 2: %q takes no arguments", argv[1])
		}
		return Command{Kind: Up}, nil
	}
	return Command{}, fmt.Errorf("at token 1: unknown key verb %q (allowed: type, tap, down, up)", argv[1])
}

// join glues the text arguments with single spaces, which is how a shell
// line that was not quoted reads back.
func join(parts []string) string {
	out := ""
	for i, p := range parts {
		if i > 0 {
			out += " "
		}
		out += p
	}
	return out
}
