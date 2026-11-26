package main

import (
	"fmt"
	"os"
	"strings"
	"time"

	"ch9329"
	"github.com/pkubangbang/kvm/internal/keygrammar"
	"github.com/pkubangbang/kvm/internal/mouse"
)

// runKey is the keyboard surface: `kvm key type|tap|down|up ...`.
//
// It opens the same serial link the mouse path uses, through mouse.Open, so the
// port resolution (-port / discovery / PortAlt fallback) and the close-on-exit
// guarantee are identical to `kvm mouse ...`. A keyboard command that cannot
// open the port fails before sending anything, exactly like a mouse one.
//
// KEY RELEASES ARE THE HAZARD HERE. `key down X` leaves X held on the wire with
// no release frame, and os.Exit does not run defers. Every exit path below
// therefore sends an all-zero release before leaving, the same contract the
// mouse path enforces with fail(). A stranded modifier turns every later
// keystroke — including the user's own typing — into a chord.
func runKey(argv []string, portFlag string, baudFlag int) {
	cmd, err := keygrammar.Parse(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: %v\n", err)
		os.Exit(2)
	}

	// Validate every spec BEFORE opening the port: a typo must not hold the
	// port (and on Windows, must not race the mouse path for it).
	type chord struct {
		spec  string
		usage byte
		mods  byte
	}
	var chords []chord
	if cmd.Kind == keygrammar.Tap || cmd.Kind == keygrammar.Down {
		for _, spec := range cmd.Specs {
			u, m, perr := ch9329.ParseKey(spec)
			if perr != nil {
				fmt.Fprintf(os.Stderr, "kvm: key %q: %v\n", spec, perr)
				os.Exit(2)
			}
			chords = append(chords, chord{spec, u, m})
		}
	}
	if cmd.Kind == keygrammar.Down {
		c := chords[0]
		if c.usage == 0 && c.mods == 0 {
			fmt.Fprintf(os.Stderr, "kvm: key down %q: that is not a key\n", c.spec)
			os.Exit(2)
		}
	}

	cfg := settings(portFlag, baudFlag)
	dv, err := open(cfg)
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: key: %v\n", err)
		os.Exit(1)
	}
	// Always release, and always close, whatever happens next.
	release := func(code int) {
		_ = dv.KeyRelease()
		dv.Close()
		os.Exit(code)
	}

	switch cmd.Kind {
	case keygrammar.Type:
		skipped, terr := dv.TypeText(cmd.Text, keyGap)
		if terr != nil {
			fmt.Fprintf(os.Stderr, "kvm: key type: %v\n", terr)
			release(1)
		}
		if skipped > 0 {
			fmt.Fprintf(os.Stderr, "kvm: key type: skipped %d character(s) with no US-layout HID usage (the boot keyboard cannot type them; non-ASCII needs the host's input method)\n", skipped)
		}
		fmt.Printf("typed %d char(s), skipped %d\n", len(cmd.Text)-skipped, skipped)
	case keygrammar.Tap:
		for _, c := range chords {
			if err := dv.KeyPress(c.usage, c.mods); err != nil {
				fmt.Fprintf(os.Stderr, "kvm: key tap %q: %v\n", c.spec, err)
				release(1)
			}
		}
		fmt.Printf("tapped %s\n", strings.Join(cmd.Specs, " "))
	case keygrammar.Down:
		c := chords[0]
		if err := dv.KeyHold(c.usage, c.mods); err != nil {
			fmt.Fprintf(os.Stderr, "kvm: key down %q: %v\n", c.spec, err)
			release(1)
		}
		fmt.Printf("holding %s\n", c.spec)
	case keygrammar.Up:
		if err := dv.KeyRelease(); err != nil {
			fmt.Fprintf(os.Stderr, "kvm: key up: %v\n", err)
			release(1)
		}
		fmt.Println("released")
	}
	release(0)
}

// keyGap is the delay BETWEEN keys while typing. The chip's own PressGap is the
// delay inside one press/release pair (100 ms, ch9329.PressGap); this is the
// extra pause so a fast host renderer does not coalesce repeats. Measured
// need on this rig: Notepad accepts a 20 ms inter-key gap; anything faster
// risks the host dropping repeat frames.
var keyGap = 20 * time.Millisecond

// Compile-time assertion that mouse.Device exposes the keyboard bridge methods
// runKey relies on. If the wrapper loses them, this file stops building rather
// than silently reaching into the chip device directly. (Close() returns no
// error on mouse.Device — that is its signature, not a typo.)
var _ interface {
	KeyPress(byte, byte) error
	KeyHold(byte, byte) error
	KeyRelease() error
	TypeText(string, time.Duration) (int, error)
	Close()
} = (*mouse.Device)(nil)
