//go:build linux

package mouse

// Freshness of the published pointer, for closed-loop correction DURING a
// stroke.
//
// A stroke is drawn faster than the publisher ticks, so most of the time the
// newest frame is the one the stroke's own first report produced — measuring
// against it re-anchors on a lie (it says "you are still at the previous
// waypoint") and the residual correction then either does nothing or doubles
// the remaining leg. ShellPoint() alone cannot tell a NEW measurement from the
// same record read twice, because it only promises "a live writer exists".
//
// freshEnough answers the narrower question the loop actually asks: has a
// record been published since the last time the loop looked? The reader keeps
// the last seq it consumed per goroutine-free process, which is exactly one
// stroke at a time here (a kvm invocation draws one stroke).

import (
	"sync"
	"time"
)

var (
	freshMu  sync.Mutex
	freshSeq uint32
	freshOK  bool
)

// freshEnough reports whether a NEW pointer record has been published since the
// previous call. The first call establishes a baseline and reports false: there
// is no prior sample to compare, and re-anchoring a stroke is only sound when
// the measurement is known to postdate the report the stroke just sent.
func freshEnough() bool {
	p, ok := ring().Latest()
	if !ok {
		return false
	}
	freshMu.Lock()
	defer freshMu.Unlock()
	if !freshOK {
		freshSeq, freshOK = p.Seq, true
		return false
	}
	if p.Seq == freshSeq {
		return false
	}
	freshSeq = p.Seq
	return true
}

// resetFresh forgets the baseline, so a NEW stroke starts without reusing the
// previous stroke's last consumed seq.
func resetFresh() {
	freshMu.Lock()
	freshOK = false
	freshMu.Unlock()
}

var _ = time.Second // keep the import list stable for build-tag variants
