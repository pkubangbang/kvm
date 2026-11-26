//go:build !linux && !windows

package main

import (
	"fmt"
	"os"
	"runtime"
)

// runMouseHud is the stand-in for `kvm mouse hud on|off` on platforms with no
// overlay implementation.
//
// Windows is excluded: hud_windows.go spawns the Win32 crosshair there. The verb
// fails loudly rather than silently doing nothing — a hud command that quietly
// succeeded would look like a running overlay that never appears.
func runMouseHud(args []string) {
	fmt.Fprintf(os.Stderr, "kvm: mouse hud: not supported on %s (no overlay renderer)\n", runtime.GOOS)
	os.Exit(1)
}
