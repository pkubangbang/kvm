//go:build linux

// kvm-crosshair — the X11 crosshair overlay, as a SINGLETON.
//
// One process ever owns the crosshair. Ownership is an flock on a runtime lock
// file, not a pid file: the kernel drops the lock when the process dies, so a
// crash leaves nothing to clean up and nothing to get wrong.
//
// The same process also listens on a unix datagram socket so a second caller
// never becomes a second crosshair:
//
//	kvm mouse hud on    start it in the background (no-op if already running)
//	kvm mouse hud off   tell the owner to exit
//
// The daemon is the only thing that talks to X; the commands above only touch
// files and a socket.
//
// Position source: the ring buffer published by kvm-cursor-report@mycc
// (internal/cursor). Nothing here reads XQueryPointer: under XWayland that
// reports the phantom X pointer, not the real cursor.
package main

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/pkubangbang/kvm/internal/cursor"
)

const (
	onMsg  = "on"
	offMsg = "off"
)

func runtimeDir() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return d
	}
	return "/tmp"
}

func lockPath() string { return filepath.Join(runtimeDir(), "kvm-crosshair.lock") }
func sockPath() string { return filepath.Join(runtimeDir(), "kvm-crosshair.sock") }

func main() {
	posFile := flag.String("pos", "/tmp/kvm-cursor.pos", "unused (kept for CLI compatibility)")
	_ = posFile
	flag.Parse()
	args := flag.Args()

	// No arguments (or "run"): be the daemon. This is what `hud on` spawns.
	if len(args) == 0 || args[0] == "run" {
		if err := runDaemon(); err != nil {
			fmt.Fprintf(os.Stderr, "kvm-crosshair: %v\n", err)
			os.Exit(3)
		}
		return
	}
	if err := control(args[0]); err != nil {
		fmt.Fprintf(os.Stderr, "kvm-crosshair: %v\n", err)
		os.Exit(3)
	}
}

// control sends one command to the running daemon.
func control(cmd string) error {
	c, err := net.Dial("unixgram", sockPath())
	if err != nil {
		return fmt.Errorf("crosshair is not running (%v)", err)
	}
	defer c.Close()
	_, err = c.Write([]byte(cmd))
	return err
}

// runDaemon takes ownership of the crosshair, or returns immediately when
// another process already owns it (the caller's `hud on` semantics: idempotent).
func runDaemon() error {
	lock, err := os.OpenFile(lockPath(), os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open the lock %s: %w", lockPath(), err)
	}
	defer lock.Close()
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if errors.Is(err, syscall.EWOULDBLOCK) {
			return nil // already running: nothing to do, not an error
		}
		return fmt.Errorf("flock %s: %w", lockPath(), err)
	}

	// We own it. A stale socket from a crashed predecessor is ours to replace.
	_ = os.Remove(sockPath())
	ln, err := net.ListenUnixgram("unixgram", &net.UnixAddr{Name: sockPath(), Net: "unixgram"})
	if err != nil {
		return fmt.Errorf("listen on %s: %w", sockPath(), err)
	}
	defer func() {
		ln.Close()
		_ = os.Remove(sockPath())
	}()

	r, err := newX11Renderer()
	if err != nil {
		return err
	}
	defer r.Close()

	positions := cursor.NewRingSource("")
	defer positions.Close()

	stop := make(chan os.Signal, 2)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)

	// Commands arrive here and only here; the daemon owns the decision to quit.
	quit := make(chan struct{})
	go func() {
		buf := make([]byte, 32)
		for {
			n, err := ln.Read(buf)
			if err != nil {
				return
			}
			if string(buf[:n]) == offMsg {
				close(quit)
				return
			}
		}
	}()

	tick := time.NewTicker(16 * time.Millisecond) // ~60 Hz, same rate as the ring
	defer tick.Stop()

	var lastX, lastY = -1 << 30, -1 << 30
	for {
		select {
		case <-stop:
			return nil
		case <-quit:
			return nil
		case <-tick.C:
			x, y, ok := positions.Position()
			if !ok || (x == lastX && y == lastY) {
				continue
			}
			lastX, lastY = x, y
			if err := r.Draw(x, y); err != nil {
				fmt.Fprintf(os.Stderr, "kvm-crosshair: draw at %d %d: %v\n", x, y, err)
			}
		}
	}
}
