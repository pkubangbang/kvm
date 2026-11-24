//go:build !linux

package mouse

// hostPoint: other platforms have no X11 witness, so the tracked wire position
// is the only answer.
func hostPoint() (int, int, bool) { return 0, 0, false }
