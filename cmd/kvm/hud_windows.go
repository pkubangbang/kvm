//go:build windows

package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"strconv"
	"time"
)

// runMouseHud implements `kvm mouse hud on|off` on Windows — the crosshair's
// lifecycle as two verbs, nothing more.
//
// on   start the singleton daemon detached (idempotent: the kvm-crosshair binary
//
//	owns a named mutex, so a second copy exits quietly)
//
// off  ask the running daemon to exit over its control pipe; when none is
//
//	running that is the desired end state, not a failure
//
// The daemon is a separate process so the crosshair outlives the command and
// survives the terminal that started it. It is built as a GUI binary, so it
// shows no console window.
func runMouseHud(args []string) {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "usage: kvm mouse hud on|off")
		os.Exit(1)
	}
	switch args[0] {
	case "on":
		hudOn()
	case "off":
		hudOff()
	default:
		fmt.Fprintf(os.Stderr, "kvm: mouse hud: unknown verb %q (want on or off)\n", args[0])
		os.Exit(1)
	}
}

// crosshairBin is the sibling binary that draws the overlay.
func crosshairBin() string {
	self, err := os.Executable()
	if err != nil {
		return "kvm-crosshair.exe"
	}
	return filepath.Join(filepath.Dir(self), "kvm-crosshair.exe")
}

func hudOn() {
	bin := crosshairBin()
	if _, err := os.Stat(bin); err != nil {
		// Running from a build tree: `go run ./cmd/kvm` leaves the sibling
		// binary absent. Say so plainly instead of spawning nothing.
		fmt.Fprintf(os.Stderr, "kvm: mouse hud on: %s not found (build it: go build -ldflags \"-H windowsgui\" -o kvm-crosshair.exe ./cmd/kvm-crosshair)\n", bin)
		os.Exit(1)
	}
	cmd := osexec.Command(bin)
	// No inherited handles and no console: the daemon must not tie this command
	// (or its terminal) to the overlay's lifetime.
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "kvm: mouse hud on: %v\n", err)
		os.Exit(1)
	}
	_ = cmd.Process.Release() // the daemon is not ours to reap
	fmt.Println("kvm: crosshair on")
}

func hudOff() {
	out, err := osexec.Command(crosshairBin(), "off").CombinedOutput()
	if err != nil {
		if len(out) > 0 {
			fmt.Fprint(os.Stderr, string(out))
		}
		fmt.Println("kvm: crosshair already off")
		return
	}
	// The daemon closes its pipe as it exits; wait for the process to disappear
	// so a following `on` cannot race a half-dead predecessor.
	for i := 0; i < 50; i++ {
		if !crosshairRunning() {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Println("kvm: crosshair off")
}

// crosshairRunning reports whether a kvm-crosshair process exists. Purely a
// post-`off` settle check, so a tasklist probe is enough — the daemon itself
// owns the real singleton guarantee.
func crosshairRunning() bool {
	out, err := osexec.Command("tasklist", "/FI", "IMAGENAME eq kvm-crosshair.exe", "/NH").Output()
	if err != nil {
		return false
	}
	return len(out) > 0 && containsImage(string(out))
}

func containsImage(s string) bool {
	for i := 0; i+14 <= len(s); i++ {
		if s[i:i+14] == "kvm-crosshair." {
			return true
		}
	}
	return false
}

var _ = strconv.Itoa
