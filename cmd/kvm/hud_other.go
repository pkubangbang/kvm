//go:build !linux

package main

import (
	"fmt"
	"os"
	"runtime"
)

// runMouseHud is the non-Linux stand-in for `kvm mouse hud on|off`.
//
// The crosshair is an X11 overlay (cmd/kvm-crosshair), so it
// does not exist on this platform. The verb is still parsed (the CLI grammar is
// the same everywhere) and it fails loudly: a hud command that silently did
// nothing would look like a running overlay that never appears.
func runMouseHud(args []string) {
	fmt.Fprintf(os.Stderr, "kvm: mouse hud: not supported on %s (the crosshair is an X11 overlay)\n", runtime.GOOS)
	os.Exit(1)
}
