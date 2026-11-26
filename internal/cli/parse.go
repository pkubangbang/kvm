// Package cli holds the RIGID syntax checker.
//
// It is a pure function: no hardware, no clock, no I/O. Everything about the
// grammar is decided here, before a single byte reaches the chip, so a malformed
// command can never move the pointer.
//
// The rules, in the order they are enforced:
//  1. shape        "mouse" namespace verb...
//  2. vocabulary   the verb must exist in THAT namespace
//  3. arity        exact argument count, never padded
//  4. lexical      integers only; no x=1 key/value form
//  5. range        screen bounds for abs, +/-127 for a single rel report
//  6. state        down/up latch; some verbs are only legal in one state
//  7. termination  the sequence must end with the button UP
package cli

import (
	"fmt"
	"os"
	"regexp"
	"strconv"
)

// debugParse prints the tokenisation when KVM_DEBUG_PARSE is set.
var debugParse = os.Getenv("KVM_DEBUG_PARSE") != ""

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// Kind identifies a verb.
type Kind string

const (
	Move Kind = "move"
	Down Kind = "down"
	Up   Kind = "up"
	Drag Kind = "drag"
	Wait Kind = "wait"
	// RDown and RUp are "down" and "up" with the RIGHT button. They exist as
	// verbs, not as a flag, because a right-click is a first-class action
	// ("open the context menu") and not a mode the whole command runs in.
	RDown Kind = "rdown"
	RUp   Kind = "rup"
)

// Verb is one parsed command in a sequence.
type Verb struct {
	Kind Kind
	A, B int // coordinates (abs: X Y) or deltas (rel: DX DY); unused when arity is 0
	I    int // index in the original argv, for error messages
}

// Sequence is a validated, executable list of verbs.
type Sequence struct {
	Rel   bool // false = absolute, true = relative
	Verbs []Verb
}

// Config supplies the values the checker needs but does not own.
type Config struct {
	ScreenW, ScreenH int
}

var integer = regexp.MustCompile(`^[+-]?\d+$`)

// spec describes one verb's shape.
type spec struct {
	arity   int
	absOnly bool
	relOK   bool
	// legal state: stateUp means only while the button is up, stateDown the
	// reverse, stateAny in both.
	state stateKind
}

type stateKind int

const (
	stateUp stateKind = iota
	stateDown
	stateAny
)

// absSpecs is PLACEMENT ONLY: where the pointer goes, never what the button does.
// There is deliberately no abs down/up/drag/click: on this host an absolute report
// drops the button, so any abs press verb could only be a fake — and a missing verb
// is better than a corrupted one. Button work lives in relSpecs alone.
var absSpecs = map[Kind]spec{
	Move: {arity: 2, state: stateUp},
}

var relSpecs = map[Kind]spec{
	Move: {arity: 2, state: stateUp},
	Down: {arity: 0, state: stateUp},
	Up:   {arity: 0, state: stateDown},
	Drag: {arity: 2, state: stateDown},
	Wait: {arity: 1, state: stateAny},
	// The right button obeys the same latch: press only when up, release only
	// when down. RDown and RUp are separate verbs rather than a button flag so
	// one sequence can mix buttons without a mode switch.
	RDown: {arity: 0, state: stateUp},
	// RUp releases WHERE THE POINTER IS, so it is legal with either latch: a
	// right-click in one process is `rdown` (which presses and releases where it
	// started), and a two-process right-click is `rdown` then `rup`.
	RUp: {arity: 0, state: stateAny},
}

func lookup(rel bool, k Kind) (spec, bool) {
	if rel {
		s, ok := relSpecs[k]
		return s, ok
	}
	s, ok := absSpecs[k]
	return s, ok
}

// Parse validates argv (WITHOUT the program name, starting at "mouse").
//
// SPLIT lets one process run SEVERAL sequences: kvm mouse abs move A B ;; rel down drag C D up.
// The port, the paced controller and the ring reader are process-scoped resources —
// paying their open cost per leg is pure latency in front of every stroke
// (MEASURED: ~0.25 s of a 4.6 s Z was process start alone). Each segment is
// validated on its own, so the grammar never becomes more permissive.
func Parse(argv []string, cfg Config) (Sequence, error) {
	if len(argv) < 2 {
		return Sequence{}, fmt.Errorf("usage: kvm mouse abs|rel <verb> [args]...")
	}
	if argv[0] != "mouse" {
		return Sequence{}, fmt.Errorf("unknown command %q (expected \"mouse\")", argv[0])
	}
	var rel bool
	switch argv[1] {
	case "abs":
		rel = false
	case "rel":
		rel = true
	default:
		return Sequence{}, fmt.Errorf("unknown namespace %q (expected \"abs\" or \"rel\")", argv[1])
	}

	seq := Sequence{Rel: rel}
	down := false // the latch

	i := 2
	for i < len(argv) {
		tok := argv[i]
		k := Kind(tok)
		sp, ok := lookup(rel, k)
		if !ok {
			return Sequence{}, fmt.Errorf("at token %d: unknown verb %q for %s mode", i, tok, argv[1])
		}

		// 3. arity
		if i+sp.arity >= len(argv) && sp.arity > 0 {
			return Sequence{}, fmt.Errorf("at token %d: %q needs %d argument(s)", i, tok, sp.arity)
		}

		// 6. state machine
		switch sp.state {
		case stateUp:
			if down {
				return Sequence{}, fmt.Errorf("at token %d: %q is illegal while the button is DOWN (allowed: drag, up, wait)", i, tok)
			}
		case stateDown:
			if !down {
				// drag outside a down..up span is not a shortcut, it is an error:
				// there is no button to hold, so it could only draw nothing.
				return Sequence{}, fmt.Errorf("at token %d: %q must be inside a down..up span (the button is UP: press with \"down\" first, release with \"up\")", i, tok)
			}
		}

		v := Verb{Kind: k, I: i}
		if debugParse {
			fmt.Fprintf(os.Stderr, "parse: idx=%d verb=%s arity=%d nums=%v nxt=%v\n",
				i, k, sp.arity, argv[i+1:minInt(i+1+sp.arity, len(argv))], argv[i+1+sp.arity:])
		}
		var nums []int
		for a := 1; a <= sp.arity; a++ {
			raw := argv[i+a]
			if !integer.MatchString(raw) {
				return Sequence{}, fmt.Errorf("at token %d: %q is not an integer", i+a, raw)
			}
			n, err := strconv.Atoi(raw)
			if err != nil {
				return Sequence{}, fmt.Errorf("at token %d: %q is out of range", i+a, raw)
			}
			nums = append(nums, n)
		}

		// 5. range
		switch {
		case k == Wait:
			if nums[0] < 0 || nums[0] > 60000 {
				return Sequence{}, fmt.Errorf("at token %d: wait %d out of range [0,60000]", i, nums[0])
			}
		case rel:
			// NOTE: |d| <= 127 is a WIRE constraint (one HID report), not a limit on
			// what the user may type. A long leg is sliced into many reports by the
			// executor, exactly like a real mouse crossing the screen. Only the
			// per-report frame is checked, in mouse.Fire. We bound the magnitude
			// here so a typo cannot ask for a million-frame walk.
			const maxLeg = 100000
			for a, n := range nums {
				if n > maxLeg || n < -maxLeg {
					return Sequence{}, fmt.Errorf("at token %d: delta %d is implausible (|d| <= %d)", i+1+a, n, maxLeg)
				}
			}
		default:
			if len(nums) > 0 {
				if nums[0] < 0 || nums[0] >= cfg.ScreenW {
					return Sequence{}, fmt.Errorf("at token %d: x=%d outside [0,%d)", i+1, nums[0], cfg.ScreenW)
				}
			}
			if len(nums) > 1 {
				if nums[1] < 0 || nums[1] >= cfg.ScreenH {
					return Sequence{}, fmt.Errorf("at token %d: y=%d outside [0,%d)", i+2, nums[1], cfg.ScreenH)
				}
			}
		}
		if len(nums) > 0 {
			v.A = nums[0]
		}
		if len(nums) > 1 {
			v.B = nums[1]
		}

		// latch + "positioned" bookkeeping
		switch k {
		case Down:
			// In ABS mode a press must resend a coordinate, so something must have
			// put the pointer somewhere in this same invocation. In REL mode a
			// press is "press where the pointer already is" — coordinates are not
			// part of the relative vocabulary at all, so no prior verb is needed.
			down = true
		case RDown:
			down = true
		case Up, RUp:
			down = false
		}

		seq.Verbs = append(seq.Verbs, v)
		i += 1 + sp.arity
	}

	if len(seq.Verbs) == 0 {
		return Sequence{}, fmt.Errorf("no verbs given")
	}
	// 7. termination: append the release instead of refusing, so a command typed
	// as `down drag ...` always ends with the button up.
	if down {
		seq.Verbs = append(seq.Verbs, Verb{Kind: Up})
	}
	return seq, nil
}
