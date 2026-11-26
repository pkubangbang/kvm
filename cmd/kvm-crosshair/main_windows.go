//go:build windows

package main

// kvm-crosshair — the Win32 crosshair overlay, as a SINGLETON.
//
// This is the Windows twin of main.go (X11). The structure is deliberately the
// same, because the *guarantees* are the same and they are what the CLI relies on:
//
//	one owner ever          -> a named mutex, not a lock file (see below)
//	idempotent `hud on`     -> the loser of the mutex race exits quietly
//	`hud off` can kill it   -> a named pipe carries the off command
//	position from outside   -> the mouse package's witness, not this process
//
// Ownership: the Linux build takes an flock on kvm-crosshair.lock. A named mutex
// (CreateMutexW) gives the same property more strongly: the kernel releases it
// when the owning process dies, including on a crash, so there is nothing to
// clean up and no stale owner to detect. Ownership is creation, not a file.
//
// Position source: internal/mouse's ShellPoint — the same witness every motion
// primitive trusts. The overlay is a pure CONSUMER and never queries the OS
// itself, so there is exactly one position authority per platform and the
// crosshair can never disagree with the tool about where the pointer is.
// If the crosshair and the pointer diverge, it is because a report was dropped,
// which is precisely the thing worth seeing.

import (
	"flag"
	"fmt"
	"os"
	"runtime"
	"time"

	"github.com/pkubangbang/kvm/internal/mouse"
)

const (
	onMsg  = "on"
	offMsg = "off"
)

func main() {
	posFile := flag.String("pos", "", "unused (kept for CLI compatibility)")
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

// control sends one command to the running daemon over the named pipe.
func control(cmd string) error {
	return sendPipe(cmd)
}

// runDaemon takes ownership of the crosshair, or returns immediately when another
// process already owns it (the caller's `hud on` semantics: idempotent).
func runDaemon() error {
	// PIN THIS GOROUTINE TO ONE OS THREAD, for the whole process lifetime.
	//
	// A Win32 window is owned by the THREAD that created it, and its message
	// queue is per-thread: PeekMessageW(hwnd=nil) drains only the CALLING
	// thread's queue. This goroutine creates the window (newWin32Renderer) and
	// pumps its queue, so it must be the same OS thread for both — but a
	// goroutine that blocks in `select` is eligible for hand-off to a different
	// M, and once it migrates the pump drains an empty queue forever. The
	// window's own thread then never services anything, so the OS sees a hung
	// window (Responding=False) while GDI keeps painting the crosshair
	// correctly — because UpdateLayeredWindow is not thread-affine.
	//
	// MEASURED on this rig: 20 s at ~16 new processes/second wedged the daemon
	// permanently (alive, arms still drawn, WM_NULL unanswered). An idle box
	// usually never migrates, which is why the defect looked intermittent.
	// There is no UnlockOSThread: this goroutine owns the window for as long as
	// the process lives.
	runtime.LockOSThread()

	// Take ownership. acquired=false means someone else owns it: not an error,
	// exactly as the Linux build treats EWOULDBLOCK.
	owner, acquired, err := acquireSingleton()
	if err != nil {
		return fmt.Errorf("acquire the crosshair: %w", err)
	}
	if !acquired {
		return nil
	}
	defer owner.Close()

	// We own it. A stale pipe from a crashed predecessor is gone with it — the
	// kernel tears the pipe down with its process — so there is nothing to remove
	// first, unlike the Linux socket file.
	pipe, err := listenPipe()
	if err != nil {
		return fmt.Errorf("listen on the control pipe: %w", err)
	}
	defer pipe.Close()

	r, err := newWin32Renderer()
	if err != nil {
		return err
	}
	defer r.Close()

	// Commands arrive here and only here; the daemon owns the decision to quit.
	//
	// A control connection that fails for any reason OTHER than "off" must not
	// take this goroutine with it. ReadFile fails with ERROR_BROKEN_PIPE when a
	// client connects and closes without writing (or a write is truncated), and
	// any process on the box can open \\.\pipe\kvm-crosshair. Previously the
	// first such client returned from the goroutine, so the daemon lost its only
	// exit path: the crosshair stayed on screen for good and `hud off` reported
	// "already off". Only a real "off" ends the daemon now; everything else is a
	// dead client and the pipe is re-armed by the next Accept.
	quit := make(chan struct{})
	go func() {
		for {
			msg, err := pipe.Accept()
			if err != nil {
				if msg == "" && err != nil {
					// Keep listening: the pipe server is still valid, this was
					// one client's failed session.
					continue
				}
				continue
			}
			if msg == offMsg {
				close(quit)
				return
			}
		}
	}()

	// The Linux daemon ticks at 60 Hz because it polls the ring, which publishes
	// at 60 Hz and can skip. On Windows the witness is a live point sample, so a
	// faster tick costs one cheap query and buys a crosshair that tracks the
	// pointer without a frame of lag.
	tick := time.NewTicker(8 * time.Millisecond)
	defer tick.Stop()

	var lastX, lastY = -1 << 30, -1 << 30
	for {
		// Wait for the NEXT event, whether it is the tick or a mouse/keyboard
		// event, THEN drain the queue. Waiting inside select is what keeps the
		// process answering the OS: a plain `default:` pump drained messages but
		// never yielded, so a burst of input events starved the loop and Windows
		// marked the window "Not Responding".
		select {
		case <-quit:
			return nil
		case <-tick.C:
			if !pump() {
				return nil
			}
			x, y, ok := mouse.ShellPoint()
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
