// Package crosshair draws a crosshair centred on a screen position.
//
// It is deliberately rendering-only: it owns no window, no timer and no input.
// The caller decides where the position comes from (a platform witness, a test)
// and calls Draw on every update.
package crosshair

// Draw paints a full-screen crosshair centred on (cx, cy) into a buffer that
// represents the rectangle whose top-left corner is (originX, originY).
//
// The arms run the whole width and height of the buffer — a crosshair is a
// screen-wide guide, not a small marker — and the crossing itself is solid.
// Pixels outside the buffer are skipped, so a crosshair near the screen edge is
// drawn half-visible, which is what a user expects.
//
// The buffer is width*height pixels in row-major order, each 4 bytes RGBA, and
// is cleared to fully transparent first, so the caller can draw the whole
// surface each frame without tracking dirty rectangles.
func Draw(buf []byte, width, height, originX, originY, cx, cy int, r, g, b, a byte) {
	for i := range buf {
		buf[i] = 0
	}
	if width <= 0 || height <= 0 || len(buf) < width*height*4 {
		return
	}
	set := func(x, y int) {
		if x < 0 || y < 0 || x >= width || y >= height {
			return
		}
		i := (y*width + x) * 4
		buf[i], buf[i+1], buf[i+2], buf[i+3] = r, g, b, a
	}
	// Coordinates inside the buffer: shift the absolute centre into local space.
	lx, ly := cx-originX, cy-originY

	// Horizontal arm: the full row through ly.
	for x := 0; x < width; x++ {
		set(x, ly)
	}
	// Vertical arm: the full column through lx.
	for y := 0; y < height; y++ {
		set(lx, y)
	}
}
