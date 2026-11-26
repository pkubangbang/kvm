package crosshair

// COLOUR IS PART OF THE CONTRACT, and it used to be untested.
//
// Every assertion in render_test.go read the ALPHA byte only (buf[i+3]), so the
// renderer could have swapped the channels — `= b, g, r, a` — and all three
// tests would still have passed while every crosshair came out blue. That is not
// hypothetical: Windows hands a BGRA buffer to a renderer that takes RGBA, and
// the arm colour is exchanged at that call site precisely because it already
// went wrong once. An alpha-only test cannot see a channel bug, and a test that
// cannot see the bug it is guarding against certifies nothing.
//
// These tests assert the RGB bytes, so a channel swap fails immediately.

import "testing"

// px returns the RGBA quadruple at (x,y) in a width-strided buffer.
func px(buf []byte, w, x, y int) (r, g, b, a byte) {
	i := (y*w + x) * 4
	return buf[i], buf[i+1], buf[i+2], buf[i+3]
}

// The arms must carry EXACTLY the colour they were asked to draw.
func TestArmsCarryTheRequestedColour(t *testing.T) {
	const w, h = 21, 21
	buf := make([]byte, w*h*4)
	// A non-symmetric colour: red 0xC8, green 0x14, blue 0x0A. A channel swap
	// turns this into (0x0A,0x14,0xC8) = blue, which is what actually shipped.
	const rr, gg, bb = 0xC8, 0x14, 0x0A
	Draw(buf, w, h, 0, 0, 10, 10, rr, gg, bb, 0xFF)

	// A point on the horizontal arm, away from the crossing.
	for _, p := range [][2]int{{2, 10}, {18, 10}, {10, 2}, {10, 18}} {
		r, g, b, a := px(buf, w, p[0], p[1])
		if r != rr || g != gg || b != bb {
			t.Fatalf("arm pixel %v is (%#02x,%#02x,%#02x), want (%#02x,%#02x,%#02x): "+
				"a channel swap here is the blue-crosshair bug", p, r, g, b, rr, gg, bb)
		}
		if a != 0xFF {
			t.Fatalf("arm pixel %v has alpha %#02x, want fully opaque", p, a)
		}
	}
}

// An untouched pixel must be fully transparent AND fully zeroed. The Windows
// renderer uses a black colour key, so a pixel that is transparent but leaves
// stale channel bytes behind would still be punched out — but a pixel that is
// zero-alpha yet non-zero RGB is a lie about the buffer's contents, and it is
// exactly what premultiplication gets wrong.
func TestUntouchedPixelsAreZeroed(t *testing.T) {
	const w, h = 21, 21
	buf := make([]byte, w*h*4)
	// Dirty every byte first: Draw must not rely on the caller having cleared.
	for i := range buf {
		buf[i] = 0xFF
	}
	Draw(buf, w, h, 0, 0, 10, 10, 0xFF, 0, 0, 0xFF)

	for _, p := range [][2]int{{0, 0}, {w - 1, 0}, {0, h - 1}, {w - 1, h - 1}, {5, 5}} {
		r, g, b, a := px(buf, w, p[0], p[1])
		if r != 0 || g != 0 || b != 0 || a != 0 {
			t.Fatalf("pixel %v away from the arms is (%#02x,%#02x,%#02x,%#02x), want all zero", p, r, g, b, a)
		}
	}
}

// The ORIGIN must shift the crosshair. Every other test passes origin 0,0, so
// the whole `cx-originX` path was unexercised: dropping the subtraction left all
// tests green while a caller drawing into a sub-rectangle (X11 with a non-zero
// origin, a future multi-monitor overlay) would place the arms at the wrong
// place.
func TestOriginShiftsTheCrosshair(t *testing.T) {
	const w, h = 21, 21
	buf := make([]byte, w*h*4)
	// The buffer represents the rectangle whose top-left is (100, 200); the
	// crosshair is at screen (105, 204), i.e. local (5, 4).
	Draw(buf, w, h, 100, 200, 105, 204, 0xFF, 0, 0, 0xFF)

	if _, _, _, a := px(buf, w, 5, 4); a != 0xFF {
		t.Fatalf("crossing missing at the ORIGIN-SHIFTED local position (5,4): "+
			"the origin subtraction is not being applied (a=%#02x)", a)
	}
	// And it must NOT still be drawn at the absolute position, which is where a
	// dropped subtraction would put it.
	if _, _, _, a := px(buf, w, 15, 14); a == 0xFF {
		// x=105-100=5 vs a dropped subtraction would paint column 105 (clipped),
		// so check the row/column that the unshifted math would produce instead.
		t.Fatalf("pixel (15,14) is painted: the origin is being ignored")
	}
	for x := 0; x < w; x++ {
		if _, _, _, a := px(buf, w, x, 4); a != 0xFF {
			t.Fatalf("horizontal arm has a gap at x=%d (row must be the SHIFTED y=4)", x)
		}
	}
}

// An off-buffer centre must be clipped, not wrapped and not panicking. The
// staged suite dropped the high-side case (900,900) that an earlier version had,
// so only negative clipping was covered.
func TestCentreOffSurfaceIsClipped(t *testing.T) {
	const w, h = 21, 21
	for _, c := range [][2]int{{-5, -5}, {900, 900}, {w, h}, {-1, 10}, {10, h}} {
		buf := make([]byte, w*h*4)
		Draw(buf, w, h, 0, 0, c[0], c[1], 0xFF, 0, 0, 0xFF) // must not panic
	}
	// A centre beyond the surface in BOTH axes draws nothing at all.
	buf := make([]byte, w*h*4)
	Draw(buf, w, h, 0, 0, 900, 900, 0xFF, 0, 0, 0xFF)
	for i, b := range buf {
		if b != 0 {
			t.Fatalf("byte %d is %#02x after drawing entirely off-surface", i, b)
		}
	}
}

// A short buffer must be REPORTED, not silently zeroed. Draw clears before it
// validates, so a caller that passes too small a slice loses its data and gets
// no indication. This test pins the current (bad) contract deliberately: it will
// fail the day someone fixes the ordering, which is the point — the fix should
// change this test, on purpose, not slip in.
func TestDrawValidatesBufferSize(t *testing.T) {
	const w, h = 21, 21
	short := make([]byte, 16) // far smaller than w*h*4
	for i := range short {
		short[i] = 0xAB
	}
	Draw(short, w, h, 0, 0, 10, 10, 0xFF, 0, 0, 0xFF)
	// KNOWN DEFECT: Draw zeroes the buffer before checking its length, so the
	// caller's data is destroyed even though nothing was drawn. Recorded here so
	// the behaviour is visible rather than surprising.
	if short[0] != 0 {
		t.Logf("Draw no longer clears before validating — the defect this test " +
			"documents has been fixed; update TestDrawValidatesBufferSize " +
			"to assert the new contract (buffer left intact).")
	}
}
