package crosshair

import "testing"

func TestDrawCentresOnPoint(t *testing.T) {
	const w, h = 21, 21
	buf := make([]byte, w*h*4)
	// Centre the crosshair in the middle of the buffer.
	Draw(buf, w, h, 100, 100, 110, 110, 0xFF, 0, 0, 0xFF)

	at := func(x, y int) byte { return buf[(y*w+x)*4+3] }
	if at(10, 10) != 0xFF {
		t.Fatalf("centre pixel is not opaque: alpha=%d", at(10, 10))
	}
	// The buffer is 21 wide, so the arms reach the border: the tip of a
	// Span-long arm lands exactly on the edge (index 0 and index 20).
	if at(0, 10) != 0xFF {
		t.Fatalf("left arm tip missing")
	}
	if at(20, 10) != 0xFF {
		t.Fatalf("right arm tip missing")
	}
	if at(10, 0) != 0xFF {
		t.Fatalf("top arm tip missing")
	}
	if at(0, 0) != 0 {
		t.Fatalf("corner should stay transparent, alpha=%d", at(0, 0))
	}
}

func TestDrawClipsAtEdges(t *testing.T) {
	const w, h = 10, 10
	buf := make([]byte, w*h*4)
	// Crosshair centred outside the buffer must not panic or wrap.
	Draw(buf, w, h, 0, 0, -5, -5, 0xFF, 0, 0, 0xFF)
	Draw(buf, w, h, 0, 0, 900, 900, 0xFF, 0, 0, 0xFF)
	for i, v := range buf {
		if i%4 == 3 && v != 0 {
			t.Fatalf("pixel %d set by an off-screen crosshair", i/4)
		}
	}
}
