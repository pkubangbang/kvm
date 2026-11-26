package crosshair

import "testing"

func TestDrawSpansTheWholeSurface(t *testing.T) {
	const w, h = 101, 61
	buf := make([]byte, w*h*4)
	Draw(buf, w, h, 0, 0, 50, 30, 0xFF, 0, 0, 0xFF)

	alpha := func(x, y int) byte { return buf[(y*w+x)*4+3] }
	// The arms reach both edges: a crosshair is a full-screen guide.
	for _, p := range [][2]int{{0, 30}, {w - 1, 30}, {50, 0}, {50, h - 1}} {
		if alpha(p[0], p[1]) != 0xFF {
			t.Fatalf("arm missing at %v", p)
		}
	}
	// Corners are untouched: the arms are thin, not filled quadrants.
	for _, p := range [][2]int{{0, 0}, {w - 1, 0}, {0, h - 1}, {w - 1, h - 1}} {
		if alpha(p[0], p[1]) != 0 {
			t.Fatalf("corner %v should stay transparent", p)
		}
	}
}

func TestDrawFillsTheCrossing(t *testing.T) {
	const w, h = 41, 41
	buf := make([]byte, w*h*4)
	Draw(buf, w, h, 0, 0, 20, 20, 0xFF, 0, 0, 0xFF)

	alpha := func(x, y int) byte { return buf[(y*w+x)*4+3] }
	// The crossing is solid, not hollow.
	if alpha(20, 20) != 0xFF {
		t.Fatalf("centre pixel must be painted")
	}
	// The row through the centre is filled end to end.
	for x := 0; x < w; x++ {
		if alpha(x, 20) != 0xFF {
			t.Fatalf("horizontal arm gap at x=%d", x)
		}
	}
	// ... and so is the column.
	for y := 0; y < h; y++ {
		if alpha(20, y) != 0xFF {
			t.Fatalf("vertical arm gap at y=%d", y)
		}
	}
}

func TestDrawClipsAtEdges(t *testing.T) {
	const w, h = 10, 10
	buf := make([]byte, w*h*4)
	// A centre outside the buffer must not panic or wrap around.
	Draw(buf, w, h, 0, 0, -5, -5, 0xFF, 0, 0, 0xFF)
	for i := 0; i < len(buf); i += 4 {
		if buf[i] != 0 || buf[i+3] != 0 {
			t.Fatalf("pixel %d painted by an off-screen crosshair", i/4)
		}
	}
}
