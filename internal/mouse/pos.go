package mouse

// Where the pointer is, as a Go subcommand needs to know it.
//
// On this host there is exactly one reliable answer: the wire. The bridge is an
// output-only HID device, XWayland's pointer follows the compositor's real
// pointer (XQueryPointer works), and the compositor offers no query API to
// unprivileged clients (`org.gnome.Shell.Eval` is disabled, the RemoteDesktop
// portal needs a permission token, /dev/uinput is root-only).
//
// So the position is CUMULATIVE, derived from what this binary actually sent.
// It is kept in a small state file so it survives process boundaries — every
// `kvm` invocation is a new process, but the pointer has only moved by what the
// previous ones commanded.
//
//   Home      (-127,-127)*24 sets the origin to exactly (0,0)
//   TravelTo  (+dx,+dy) walks from the tracked point, adding each endpoint
//   Move      (+dx,+dy) adds, exactly
//   GoTo      an exact walk to the requested point (NO homing: see Abs.GoTo)
//
// Nothing here guesses: a value that was never established is reported unknown,
// so a caller refuses instead of reporting a position it does not know.

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// posFile is where the tracked pointer position lives between processes.
func posFile() string {
	if p := os.Getenv("KVM_POS_FILE"); p != "" {
		return p
	}
	return os.TempDir() + "/kvm-pointer.pos"
}

// readTracked returns the last established pointer position.
func readTracked() (int, int, bool) {
	data, err := os.ReadFile(posFile())
	if err != nil {
		return 0, 0, false
	}
	f := strings.Fields(string(data))
	if len(f) < 2 {
		return 0, 0, false
	}
	x, e1 := strconv.Atoi(f[0])
	y, e2 := strconv.Atoi(f[1])
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return x, y, true
}

// writeTracked records a position that the wire has actually produced.
func writeTracked(x, y int) {
	p := posFile()
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, []byte(strconv.Itoa(x)+" "+strconv.Itoa(y)+"\n"), 0o600); err != nil {
		return
	}
	_ = os.Rename(tmp, p)
}

// clearTracked forgets the position (used when a walk failed half-way, so the
// record never claims a point the pointer may not have reached).
func clearTracked() { _ = os.Remove(posFile()) }

// noteMove advances the tracked position by the motion a command produced.
// Unknown stays unknown: a move made before any origin is established cannot
// produce an absolute point.
func noteMove(dx, dy int) {
	x, y, ok := readTracked()
	if !ok {
		return
	}
	writeTracked(x+dx, y+dy)
}

// noteHome records the origin that Home(screenW,screenH) establishes.
func noteHome() { writeTracked(0, 0) }

// noteGoto records a completed `goto`: an exact walk to the point, no homing.
// Only call it once the motion has actually landed there.
func noteGoto(x, y int) { writeTracked(x, y) }

// PointerPoint answers "where is the pointer" for the CLI.
//
// Order of trust, measured on this host (not assumed):
//  1. the tracked WIRE position. After `abs goto 500 300` the record is exactly
//     500 300, and a +100/+50 then -100/-50 round-trips back to it — the wire is
//     an exact account of where the pointer is.
//  2. the host reading (XQueryPointer). Under XWayland it is FROZEN at the
//     value it had when the pointer was last over an X surface (observed stuck
//     at 1031 854 across real moves), so it is only a last resort for a session
//     where nothing has been commanded yet.
//
// UPDATED: the ring (the compositor's own publication) now comes FIRST. The
// wire record is bookkeeping — what we COMMANDED — and it drifts the moment a
// report is dropped or amplified by the host. Reporting the bookkeeping made a
// `pos`-vs-ring disagreement visible and sent a whole diagnosis down the wrong
// path: "the feed is stale" was really "the reported number is our own guess".
// A witness beats a ledger.
func PointerPoint() (int, int, bool) {
	if x, y, ok := ShellPoint(); ok {
		return x, y, true
	}
	if x, y, ok := readTracked(); ok {
		return x, y, true
	}
	return hostPoint()
}

// posFilePath exposes the path for diagnostics.
func posFilePath() string { return filepath.Clean(posFile()) }

// Tracked answers where the wire last recorded the pointer, for the CLI's
// dry-run plan: known=false means "not established", never "at the origin".
func Tracked() (int, int, bool) { return readTracked() }
