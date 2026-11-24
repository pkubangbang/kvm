//go:build linux

package main

import (
	"fmt"
	"os"
	osexec "os/exec"
	"path/filepath"
	"syscall"
	"time"
)

// runMouseHud implements `kvm mouse hud on|off` — the crosshair's lifecycle as
// two verbs, nothing more.
//
// on   start the singleton daemon detached (idempotent: an already-running
//
//	daemon makes it a no-op, because the daemon itself holds the lock)
//
// off  ask the daemon to exit; if none is running this is also a no-op
//
// The daemon is a separate process so the crosshair outlives the command and
// survives the terminal that started it.
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

func hudOn() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: mouse hud on: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Join(filepath.Dir(self), "kvm-crosshair")
	if _, err := os.Stat(bin); err != nil {
		// Running from a build tree: `go run ./cmd/kvm` leaves the sibling
		// binary absent. Say so plainly instead of spawning nothing.
		fmt.Fprintf(os.Stderr, "kvm: mouse hud on: %s not found (build it: go build -o %s ./cmd/kvm-crosshair)\n", bin, bin)
		os.Exit(1)
	}
	cmd := osexec.Command(bin)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = nil, nil, nil
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true} // detach from this terminal
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "kvm: mouse hud on: %v\n", err)
		os.Exit(1)
	}
	_ = cmd.Process.Release() // the daemon is not ours to reap
	fmt.Println("kvm: crosshair on")
}

func hudOff() {
	self, err := os.Executable()
	if err != nil {
		fmt.Fprintf(os.Stderr, "kvm: mouse hud off: %v\n", err)
		os.Exit(1)
	}
	bin := filepath.Join(filepath.Dir(self), "kvm-crosshair")
	out, err := osexec.Command(bin, "off").CombinedOutput()
	if err != nil {
		// Not running is the desired end state, not a failure.
		if len(out) > 0 {
			fmt.Fprint(os.Stderr, string(out))
		}
		fmt.Println("kvm: crosshair already off")
		return
	}
	// Give the daemon a moment to unmap and exit so the next `on` is clean.
	for i := 0; i < 50; i++ {
		if _, err := os.Stat(filepath.Join(runtimeDir(), "kvm-crosshair.sock")); err != nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	fmt.Println("kvm: crosshair off")
}

// runtimeDir is where the daemon keeps its lock and socket.
func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return "/tmp"
}
