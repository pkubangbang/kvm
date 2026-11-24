// Command kvm drives the CH9329 USB-HID bridge as a real mouse.
//
// The workflow is look -> aim -> fire:
//
//	kvm plan mouse rel move 100 50 down drag 30 0 up   # predict, send nothing
//	kvm fire mouse rel move 100 50 down drag 30 0 up   # send exactly that
//
// Absolute and relative are two separate namespaces; the usual shape is an
// an absolute move to place the pointer, then a relative drag to draw:
//
//	kvm mouse abs move 900 550 && kvm mouse rel down drag -40 -20 up
package main

import (
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/pkubangbang/kvm/internal/config"
	"github.com/pkubangbang/kvm/internal/mouse"
)

func main() {
	var (
		portFlag   = flag.String("port", "", "serial port (default: discovered)")
		baudFlag   = flag.Int("baud", 0, "baud rate (default: discovered)")
		dryRun     = flag.Bool("dry-run", false, "print the frames, send nothing")
		eased      = flag.Bool("ease", false, "ease travel: slow at both ends, fast in the middle")
		humanFlag  = flag.Bool("human", true, "human motion profile (accelerate, dwell at corners, tremor)")
		stepFlag   = flag.Int("step", 0, "max px per report (default: config/4)")
		originFlag = flag.Bool("origin", false, "home to the top-left corner first (a known start)")
		gapFlag    = flag.Int("gap", 0, "ms between reports (default: config; larger = slower, stays in the 1:1 plateau)")
	)
	flag.Usage = usage
	flag.Parse()

	// Go's flag package stops at the first non-flag argument, so `kvm plan -ease
	// mouse ...` would treat -ease as a command. Re-parse the tail ourselves so
	// flags are accepted anywhere in the line.
	args := flag.Args()
	for {
		rest := []string{}
		consumed := false
		for i := 0; i < len(args); i++ {
			a := args[i]
			switch {
			case a == "-ease" || a == "--ease":
				*eased = true
				consumed = true
			case a == "-human" || a == "--human":
				*humanFlag = true
				consumed = true
			case strings.HasPrefix(a, "-human=") || strings.HasPrefix(a, "--human="):
				v := a[strings.Index(a, "=")+1:]
				*humanFlag = (v != "0" && v != "false" && v != "no")
				consumed = true
			case a == "-origin" || a == "--origin":
				*originFlag = true
				consumed = true
			case a == "-dry-run" || a == "--dry-run":
				*dryRun = true
				consumed = true
			case (a == "-step" || a == "--step") && i+1 < len(args):
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					*stepFlag = n
				}
				i++
				consumed = true
			case (a == "-gap" || a == "--gap") && i+1 < len(args):
				if n, err := strconv.Atoi(args[i+1]); err == nil {
					*gapFlag = n
				}
				i++
				consumed = true
			default:
				rest = append(rest, a)
			}
		}
		args = rest
		if !consumed {
			break
		}
	}
	if len(args) == 0 {
		usage()
		os.Exit(1)
	}

	switch args[0] {
	case "mouse":
		// The grammar IS the command: kvm mouse abs|rel <verbs>...
		// -dry-run predicts, otherwise it sends. No wrapper verbs.
		if len(args) < 2 {
			fmt.Fprintln(os.Stderr, "usage: kvm mouse abs|rel <verbs>... | kvm mouse pos")
			os.Exit(1)
		}
		if args[1] == "pos" {
			runMousePos()
			return
		}
		if args[1] == "hud" {
			runMouseHud(args[2:])
			return
		}
		// `kvm mouse abs holdseg X Y U V` — one straight held segment from
		// (X,Y) to (U,V) plus a button-up landing on (U,V). See runHeldSegment
		// for why a stroke is drawn as many short segments instead of one span.
		if args[1] == "abs" && len(args) > 2 && args[2] == "holdseg" {
			runHeldSegment(args[3:], *portFlag, *baudFlag)
			return
		}
		humanOn = *humanFlag
		runMouse(args, *dryRun, *eased, *stepFlag, *gapFlag, *originFlag, *portFlag, *baudFlag)
		return
	case "help", "-h", "--help":
		usage()
		return
	}
	fmt.Fprintf(os.Stderr, "unknown command %q\n", args[0])
	usage()
	os.Exit(1)
}

func usage() {
	fmt.Fprint(os.Stderr, `kvm — drive a CH9329 USB-HID bridge as a mouse

  kvm mouse abs move X Y          place the pointer
  kvm mouse rel move DX DY | down | up | drag DX DY | wait MS
  kvm mouse pos                   where is the pointer
  kvm mouse hud on | off          the crosshair overlay

  The verb set is closed: move, down, up, drag, wait (plus pos and hud).

Flags:
  -dry-run   predict the path, print the frames, send nothing
  -ease      ease travel (slow at the ends, fast in the middle)
  -origin    home to the top-left corner first, for a known start
  -step N    max px per report (4 stays inside libinput's 1:1 plateau)
`)
}

// settings resolves the discovered config, with flags winning where given.
func settings(portFlag string, baudFlag int) config.Config {
	cfg := config.Load()
	if portFlag != "" {
		cfg.Port = portFlag
		cfg.Source["port"] = "flag"
	}
	if baudFlag > 0 {
		cfg.Baud = baudFlag
		cfg.Source["baud"] = "flag"
	}
	return cfg
}

func open(cfg config.Config) (*mouse.Device, error) {
	dv, err := mouse.Open(cfg.Port, cfg.Baud, cfg.ScreenW, cfg.ScreenH)
	if err != nil && cfg.PortAlt != "" && cfg.PortAlt != cfg.Port {
		dv, err = mouse.Open(cfg.PortAlt, cfg.Baud, cfg.ScreenW, cfg.ScreenH)
	}
	if err != nil {
		return nil, err
	}
	// The witness: the shell extension's shared-memory ring feed. No fallback —
	// an unknown position must stay unknown.
	dv.ReadPos = mouse.ShellPoint
	return dv, nil
}

func stepOf(cfg config.Config, flagStep int) int {
	if flagStep > 0 {
		return flagStep
	}
	if cfg.StepPx > 0 {
		return cfg.StepPx
	}
	return 4
}

// runMousePos answers `kvm mouse pos`: where the pointer is.
func runMousePos() {
	x, y, ok := mouse.ShellPoint()
	if !ok {
		fmt.Fprintln(os.Stderr, "kvm: mouse pos: no pointer feed (is kvm-cursor-report@mycc enabled?)")
		os.Exit(1)
	}
	fmt.Printf("%d %d\n", x, y)
}
