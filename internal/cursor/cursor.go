// Package cursor answers ONE question: where is the pointer right now?
//
// It is deliberately independent of everything else in the tool. The crosshair
// overlay renders positions, the input package moves the pointer, and neither
// imports this package's implementation details — they only see the Source
// interface. Swapping the position feed (a GNOME Shell extension publishing a
// file on Linux, GetCursorPos on Windows, CGEventGetLocation on macOS) is a
// change inside this package alone.
package cursor

import "time"

// Source publishes the live pointer position.
//
// Position returns ok=false when no trustworthy answer exists (the feed is
// missing, or the value is stale) — never a guessed value. A renderer must
// simply keep the last known position in that case; a mover must not start a
// closed-loop walk from a point it does not know.
type Source interface {
	// Position returns the pointer position in screen pixels.
	Position() (x, y int, ok bool)
	// Describe names the feed, for logs.
	Describe() string
	// Close releases whatever the source holds.
	Close() error
}

// FileSource reads "X Y" from a file that some other process keeps up to date,
// and rejects the value once the file has not been written for maxAge.
//
// Freshness is the whole point: a file left behind by a dead publisher still
// contains a plausible number, and using it would silently place everything at
// a position the pointer has long left.
type FileSource struct {
	Path   string
	MaxAge time.Duration
}

// NewFileSource builds a file-backed source.
func NewFileSource(path string, maxAge time.Duration) *FileSource {
	return &FileSource{Path: path, MaxAge: maxAge}
}

func (f *FileSource) Describe() string { return "file " + f.Path }

func (f *FileSource) Close() error { return nil }
