//go:build !linux

package mouse

// walker is the relative controller used for placement where no warp exists.
type walker interface {
	Home(w, h int) error
	TravelTo(x, y int) error
}

// PlacePointer has no OS warp on this build: it travels with relative reports
// rather than pretending a jump happened.
func PlacePointer(x, y, w, h int, walk walker) error {
	if walk == nil {
		return ErrWarpNeedsWalk
	}
	if err := walk.Home(w, h); err != nil {
		return err
	}
	return walk.TravelTo(x, y)
}

// X11Available reports whether a pointer warp is possible on this build.
func X11Available() bool { return false }

// Dialect names the window system, for `kvm config`.
func Dialect() string { return "none" }

// WarpSupported reports whether goto can actually jump on this build.
func WarpSupported() bool { return false }
