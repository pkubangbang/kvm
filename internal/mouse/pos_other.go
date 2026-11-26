//go:build !linux && !windows

package mouse

// hostPoint: other platforms have no X11 witness and no GetCursorPos binding, so
// the tracked wire position is the only answer.
//
// Windows is excluded: pos_windows.go reads the real pointer there. Leaving this
// file tagged !linux alone would shadow it and make every command refuse with
// ErrNoPosition.
func hostPoint() (int, int, bool) { return 0, 0, false }
