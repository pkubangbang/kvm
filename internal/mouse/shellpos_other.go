//go:build !linux && !windows

package mouse

import (
	"time"

	"github.com/pkubangbang/kvm/internal/cursor"
)

// Non-Linux builds have no shell extension publishing a pointer ring.
func ShellPoint() (int, int, bool) { return 0, 0, false }

// WaitPointer has nothing to wait for on this platform.
func WaitPointer(timeout time.Duration) (int, int, bool) { return 0, 0, false }

// RecentPointer has no history on this platform.
func RecentPointer(within time.Duration) []cursor.Point { return nil }

// ClosePointerFeed releases the feed, if one was opened.
func ClosePointerFeed() {}

// WirePoint returns the newest pointer sample, waiting up to maxAge for one.
//
// No pointer feed on this platform, so it fails closed and never blocks: callers
// must treat ok=false as ErrNoPosition (the linux contract is identical in shape
// and differs only in where the frame comes from).
func WirePoint(maxAge time.Duration) (int, int, bool) { return 0, 0, false }
