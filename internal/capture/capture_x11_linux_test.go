//go:build linux

package capture

import (
	"os"
	"testing"
	"time"
)

// fdCount is the number of open file descriptors in this process. /proc/self/fd
// also contains the directory handle used to read it, but that cancels out
// across calls, so the difference is what matters.
func fdCount(t *testing.T) int {
	t.Helper()
	ents, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Skipf("/proc/self/fd unavailable: %v", err)
	}
	return len(ents)
}

// settle lets xgb's asynchronous Close release the socket before counting.
// xgb.Conn.Close sends a sentinel on its request channel; the send goroutine
// then closes the net.Conn. Counting without a pause measures that race, not a
// leak — verified: counting immediately after Close reads ~3 fds high, and the
// same count is flat once the send goroutine has finished.
func settle() { time.Sleep(400 * time.Millisecond) }

// cycle creates and closes n capturers.
func cycle(t *testing.T, n int) {
	t.Helper()
	for i := 0; i < n; i++ {
		c, err := New()
		if err != nil {
			t.Fatalf("cycle %d: New(): %v", i, err)
		}
		c.Close()
	}
}

// TestFDMeasurementIsSensitive proves the counter can SEE a leak. Without this,
// a flat result below would be indistinguishable from a broken measurement.
func TestFDMeasurementIsSensitive(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY is empty: no X server to hold a connection to")
	}
	cycle(t, 10)
	settle()
	before := fdCount(t)
	const deliberate = 5
	leaked := make([]Capturer, 0, deliberate)
	for i := 0; i < deliberate; i++ {
		c, err := New()
		if err != nil {
			t.Fatalf("New(): %v", err)
		}
		leaked = append(leaked, c) // deliberately NOT closed
	}
	settle()
	after := fdCount(t)
	for _, c := range leaked {
		c.Close()
	}
	if after-before != deliberate {
		t.Fatalf("fd counter is not sensitive: %d leaked X connections moved the count by %d",
			deliberate, after-before)
	}
}

// TestNewCloseIsFlatOnFailure pins the no-X case: every failed New() must
// release whatever it opened. Without the ownership guard in New(), a failed
// validation after a successful connect leaks one socket per call.
//
// Only GROWTH is a leak. The count can also FALL during this test when a close
// from an earlier test finishes settling, so asserting equality would be flaky
// for a reason that has nothing to do with New().
func TestNewCloseIsFlatOnFailure(t *testing.T) {
	t.Setenv("DISPLAY", ":9999") // no such display
	settle()                     // let any earlier async close finish
	before := fdCount(t)
	for i := 0; i < 50; i++ {
		if _, err := New(); err == nil {
			t.Fatalf("New() with DISPLAY=:9999 unexpectedly succeeded")
		}
	}
	settle()
	if after := fdCount(t); after > before {
		t.Fatalf("fd leak on the connect-failure path: %d -> %d over 50 failed New() calls", before, after)
	}
}

// TestNewCloseIsFlat is the real leak proof: a capturer that actually holds an
// X11 connection, created and closed repeatedly. The fd count must not grow.
//
// It deliberately does NOT require Screen() to succeed. XWayland refuses
// X_GetImage on the root window (verified independently: `xwd -root` fails with
// the same BadMatch on this host), so requiring a successful grab would make
// this test skip on exactly the platform where the connection lifecycle needs
// pinning most. The connection is what leaks; the grab is orthogonal.
func TestNewCloseIsFlat(t *testing.T) {
	if os.Getenv("DISPLAY") == "" {
		t.Skip("DISPLAY is empty: no X server to hold a connection to")
	}
	// Establish that a working capturer is even possible before measuring.
	c0, err := New()
	if err != nil {
		t.Skipf("no usable X server: %v", err)
	}
	c0.Close()
	c0.Close() // idempotent: must not panic or block on a second xgb send

	cycle(t, 20) // warm up process-level constants (goroutine arenas, epoll)
	settle()
	before := fdCount(t)
	const rounds = 200
	var screenErr error
	for i := 0; i < rounds; i++ {
		c, err := New()
		if err != nil {
			t.Fatalf("round %d: New(): %v", i, err)
		}
		// Exercise the connection: a round trip that is guaranteed to be legal
		// on every server, even one that forbids GetImage on the root.
		if _, err := c.Screen(); err != nil {
			screenErr = err
		}
		c.Close()
		c.Close() // double Close must be a no-op, not a second xgb send
	}
	if screenErr != nil {
		t.Logf("Screen() returned %v on every round (expected on XWayland, which forbids X_GetImage on the root)", screenErr)
	}
	settle()
	// Growth only: a close started by the previous test can finish during this
	// one and make the count DROP, which is not a leak.
	if after := fdCount(t); after > before {
		t.Fatalf("fd leak: %d -> %d over %d New/Close rounds", before, after, rounds)
	}
}

// TestCloseWithoutNew guards the nil-receiver path, which a deferred Close on a
// partially constructed value would hit.
func TestCloseNilSafe(t *testing.T) {
	var x *X11
	x.Close() // must not panic
	(&X11{}).Close()
}
