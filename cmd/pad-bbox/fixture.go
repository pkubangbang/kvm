package main

// fixture.go -- paint a synthetic screenshot of the pad AS IT IS NOW.
//
// Why this exists: the decoder had never been shown an image it was supposed to
// read. The only fixture on the rig was a PNG painted by a python script from a
// page generation that has since been replaced twice, and a stale fixture makes a
// correct decoder look broken. So the fixture is generated here, in Go, by the
// same program that reads it: `pad-bbox -make-fixture OUT.png` and then
// `pad-bbox -shot OUT.png` is a closed loop with nothing else in it.
//
// WHAT THE PAGE PAINTS (cmd/kvm-inputpad/page.html, marker()`):
//
//   - a WHITE canvas, 128 cells square, no grid lines at all;
//   - THREE QR finder patterns, 7x7 modules each, at grid cells
//     (0..6, 0..6), (118..124, 0..6) and (0..6, 118..124). The TL finder is
//     FLUSH AT THE CANVAS CORNER -- module 0 IS the canvas edge -- so the
//     separator is the white at module 7 and there is NO one-cell painted
//     offset box around the mark;
//   - the fourth corner (BR) is bare, because that is what a QR symbol's
//     three-corner triad looks like;
//   - dark modules #9a9a9a (luma ~154) on #ffffff (255). There is NO BLACK
//     anywhere on the page: the pad's own self-report says `dark 0`.
//
// The paint colour is a FIELD of fixtureCfg (Eye / Pad), not a constant buried
// in the painter, because the page's palette has changed twice already and will
// change again. Changing the page's colour means changing ONE value in
// defaultFixture and nothing else in this package.
//
// Cell edges are rounded to whole pixels the same way the page does
// (`Math.round(c*cell)`), so the fixture carries the same one-pixel jitter a
// real screenshot has.

import (
	"image"
	"image/color"
	"math"
)

// fixtureCfg is the geometry and palette of the synthetic shot.
type fixtureCfg struct {
	W, H  int        // image size in px
	X, Y  int        // canvas top-left in image px
	Side  int        // canvas side in px (page.html keeps it square)
	Cells int        // the pad's cell count (128)
	Eye   color.RGBA // dark module of a finder pattern
	Pad   color.RGBA // canvas fill: white, and the separator ring inside it
	Page  color.RGBA // everything outside the canvas
}

// defaultFixture is the arrangement measured on this rig: 768 px backing store
// shown as a 512 px canvas at (300,120) -- 4.0 px per cell -- on a 1400x900
// frame. The eye colour is #9a9a9a, which is what the page paints today.
func defaultFixture() fixtureCfg {
	return fixtureCfg{
		W: 1400, H: 900, X: 300, Y: 120, Side: 512, Cells: nCells,
		Eye:  color.RGBA{0x9a, 0x9a, 0x9a, 0xff},
		Pad:  color.RGBA{0xff, 0xff, 0xff, 0xff},
		Page: color.RGBA{0x11, 0x11, 0x11, 0xff},
	}
}

// mkFinder is the ISO/IEC 18004 finder over its own 7x7 modules: dark iff the
// Chebyshev distance from the centre is 3 (the border) or <= 1 (the 3x3 core).
// Across any centre line that is 1:1:3:1:1.
func mkFinder(y, x int) bool {
	c := mkRing / 2 // 3
	d := x - c
	if y-c > d {
		d = y - c
	}
	if -x+c > d {
		d = -x + c
	}
	if -y+c > d {
		d = -y + c
	}
	return d == 3 || d <= 1
}

// eyeOrigins are the three corners the page paints, in CELLS. The pattern is
// inset one whole module (eyeOffset) from the canvas corner -- measured from the
// page's own report `bbox 6,6..755,755` on a 768 px canvas with 6 px cells -- so
// the TL eye starts at cell (1,1), not (0,0).
func eyeOrigins(cells int) [][2]int {
	return [][2]int{
		{eyeOffset, eyeOffset},
		{eyeOffset + qrSpan, eyeOffset},
		{eyeOffset, eyeOffset + qrSpan},
	}
}

// makeFixture renders the shot described by cfg.
func makeFixture(cfg fixtureCfg) *image.RGBA {
	img := image.NewRGBA(image.Rect(0, 0, cfg.W, cfg.H))
	for y := 0; y < cfg.H; y++ {
		for x := 0; x < cfg.W; x++ {
			img.SetRGBA(x, y, cfg.Page)
		}
	}
	cell := float64(cfg.Side) / float64(cfg.Cells)
	fill := func(x0, y0, x1, y1 int, c color.RGBA) {
		for y := y0; y < y1; y++ {
			if y < 0 || y >= cfg.H {
				continue
			}
			for x := x0; x < x1; x++ {
				if x < 0 || x >= cfg.W {
					continue
				}
				img.SetRGBA(x, y, c)
			}
		}
	}
	// the canvas: white, and NO grid lines (the page draws none any more)
	fill(cfg.X, cfg.Y, cfg.X+cfg.Side, cfg.Y+cfg.Side, cfg.Pad)

	// the three eyes, one module per cell, dark modules only: the light
	// separator is the pad showing through, exactly as on the page.
	for _, o := range eyeOrigins(cfg.Cells) {
		for j := 0; j < mkRing; j++ {
			for i := 0; i < mkRing; i++ {
				if !mkFinder(j, i) {
					continue
				}
				cx, cy := o[0]+i, o[1]+j
				x0 := cfg.X + int(math.Round(float64(cx)*cell))
				y0 := cfg.Y + int(math.Round(float64(cy)*cell))
				x1 := cfg.X + int(math.Round(float64(cx+1)*cell))
				y1 := cfg.Y + int(math.Round(float64(cy+1)*cell))
				if x1 <= x0 {
					x1 = x0 + 1
				}
				if y1 <= y0 {
					y1 = y0 + 1
				}
				fill(x0, y0, x1, y1, cfg.Eye)
			}
		}
	}
	return img
}
