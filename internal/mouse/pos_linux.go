//go:build linux

package mouse

import "ch9329"

// hostPoint asks the host where the pointer is.
//
// CAVEAT, measured: under XWayland with no X window under the pointer,
// XQueryPointer returns the last value it had (observed frozen at 1031 854
// across several real moves), so this is a fallback, not the primary source.
// It is authoritative on a pure X session — hence it is still consulted when
// nothing has been commanded yet.
func hostPoint() (int, int, bool) {
	x, y, err := ch9329.CursorPoint()
	if err != nil {
		return 0, 0, false
	}
	return x, y, true
}
