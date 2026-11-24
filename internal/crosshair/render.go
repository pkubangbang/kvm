// Package crosshair draws a crosshair centred on a screen position.
//
// It is deliberately rendering-only: it owns no window, no timer and no input.
// The caller decides where the position comes from (the GNOME cursor reader's
// position file, the wire tracker, a test) and calls Draw on every update.
package crosshair

// Span is how far each arm reaches from the centre, in pixels.
const Span = 40

// ArmThickness is the width of each arm.
const ArmThickness = 1

// Draw paints a crosshair centred on (cx, cy) into a buffer that represents the
// rectangle whose top-left corner is (originX, originY). Pixels outside the
// buffer are skipped rather than clipped away silently, so a crosshair near the
// screen edge is drawn half-visible — which is what a user expects.
//
// The buffer is width*height pixels in row-major order, each 4 bytes RGBA.
// It is cleared to fully transparent first, so the caller can draw the whole
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
	for d := -Span; d <= Span; d++ {
		for t := 0; t < ArmThickness; t++ {
			set(lx+d, ly+t) // horizontal arm
			set(lx+t, ly+d) // vertical arm
		}
	}
}
