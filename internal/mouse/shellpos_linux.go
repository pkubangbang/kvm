//go:build linux

package mouse

// The pointer position, and only that: the ring buffer written by the GNOME
// Shell extension kvm-cursor-report@mycc ($XDG_RUNTIME_DIR/kvm-cursor.ring).
//
// There is no fallback on purpose. A number from anywhere else (XWayland's
// frozen X pointer, a stale file) is indistinguishable from a true one once it
// is an int, and a wrong origin makes every walk silently wrong from its first
// frame. No fresh frame means "unknown", and the motion code already knows what
// to do with unknown.

import (
	"sync"
	"time"

	"github.com/pkubangbang/kvm/internal/cursor"
)

// One mapping per process: every motion primitive asks for the pointer several
// times per walk, and re-opening the file per question costs latency for
// nothing. The reader never writes, so sharing it across goroutines is safe.
var (
	ringOnce sync.Once
	ringSrc  *cursor.RingSource
)

func ring() *cursor.RingSource {
	ringOnce.Do(func() { ringSrc = cursor.NewRingSource("") })
	return ringSrc
}

// ShellPoint returns the live published pointer position, or ok=false when the
// newest frame is missing or stale.
func ShellPoint() (int, int, bool) {
	return ring().Position()
}

// WaitPointer blocks until a fresh frame exists (or the timeout expires), so a
// short-lived command does not report "unknown" because it started between two
// frames.
func WaitPointer(timeout time.Duration) (int, int, bool) {
	p, ok := ring().Wait(timeout)
	return p.X, p.Y, ok
}

// RecentPointer returns the last `within` of published points, newest first.
func RecentPointer(within time.Duration) []cursor.Point { return ring().Recent(within) }

// ClosePointerFeed unmaps the ring.
func ClosePointerFeed() { _ = ring().Close() }

// WirePoint returns the newest pointer sample, waiting up to maxAge for one.
func WirePoint(maxAge time.Duration) (int, int, bool) {
	deadline := time.Now().Add(maxAge)
	for {
		x, y, ok := ring().Position()
		if ok {
			return x, y, true
		}
		if time.Now().After(deadline) {
			return 0, 0, false
		}
		time.Sleep(time.Millisecond)
	}
}
