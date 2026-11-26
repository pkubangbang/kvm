package main

import (
	"image"
	"image/color"
	"testing"
)

// TestFinderRatio pins the run-length test against a real finder's runs: a
// 4 px module reads 4,4,12,4,4 across a centre line.
func TestFinderRatio(t *testing.T) {
	if !isFinderRatio([]int{4, 4, 12, 4, 4}) {
		t.Fatal("4:4:12:4:4 rejected")
	}
	if !isFinderRatio([]int{1, 1, 3, 1, 1}) {
		t.Fatal("1:1:3:1:1 rejected")
	}
	if isFinderRatio([]int{4, 4, 4, 4, 4}) {
		t.Fatal("equal runs accepted")
	}
	if isFinderRatio([]int{4, 4, 12, 4}) {
		t.Fatal("four runs accepted")
	}
}

// TestFixtureDecodes is the closed loop: the decoder reads the screenshot this
// same program paints. If it cannot, the painter and the reader disagree and
// at least one of them is wrong.
func TestFixtureDecodes(t *testing.T) {
	fx := defaultFixture()
	img := makeFixture(fx)
	g := grayOf(img)
	// THE EYES ARE INSET ONE MODULE. Painted cell (0,0) is now the pad's white
	// margin, not the finder: the TL eye starts at cell (eyeOffset,eyeOffset) =
	// (1,1). So the finder's own border module is painted cell (1,1), the light
	// ring is (2,2) (d==2), and the 3x3 dark core is (4,4).
	cell := float64(fx.Side) / float64(fx.Cells)
	darkX, darkY := fx.X+int((eyeOffset+0.5)*cell), fx.Y+int((eyeOffset+0.5)*cell)
	lightX, lightY := fx.X+int((eyeOffset+1.5)*cell), fx.Y+int((eyeOffset+1.5)*cell)
	coreX, coreY := fx.X+int((eyeOffset+3.5)*cell), fx.Y+int((eyeOffset+3.5)*cell)
	darkL := g.px[darkY*g.w+darkX]
	lightL := g.px[lightY*g.w+lightX]
	coreL := g.px[coreY*g.w+coreX]
	t.Logf("painted (1,1) border luma %.1f ; painted (2,2) light ring luma %.1f ; painted (4,4) core luma %.1f",
		darkL, lightL, coreL)
	if darkL >= lightL {
		t.Fatalf("polarity: border luma %.1f must be DARKER than light ring %.1f", darkL, lightL)
	}
	if lightL-darkL < minMarkContrast {
		t.Fatalf("eye contrast %.1f below floor %.1f", lightL-darkL, minMarkContrast)
	}
	// The seed is no longer an absolute band: it is derived from the image, so
	// the assertion is that BOTH eye levels are inside the band this image
	// produces, not that they fall in a hardcoded 100..215.
	lo, hi := g.seedBand()
	if darkL < lo || darkL > hi {
		t.Fatalf("dark level %.1f outside the derived seed band %.0f..%.0f", darkL, lo, hi)
	}
	if lightL <= hi {
		t.Fatalf("light level %.1f is inside the seed band %.0f..%.0f: the pad must not seed", lightL, lo, hi)
	}

	cands := g.candidates(probeCfg{rowStride: 1})
	t.Logf("candidates %d", len(cands))
	for _, c := range cands {
		t.Logf("  (%7.2f,%7.2f) module %.3f hits %d mid %.1f", c.X, c.Y, c.Module, c.Hits, c.Mid)
	}
	if len(cands) < 3 {
		t.Fatalf("want >=3 candidates, got %d", len(cands))
	}
	named, why := assignCorners(cands)
	if why != "" {
		t.Fatalf("assignCorners: %s", why)
	}
	rc, refusal := canvasRect(named, 1.5)
	if rc == nil {
		t.Fatalf("canvasRect refused: %v", refusal)
	}
	wantX, wantY := float64(fx.X), float64(fx.Y)
	wantW := float64(fx.Side)
	if d := abs(rc.X - wantX); d > 1.5 {
		t.Errorf("x %.2f want %.2f", rc.X, wantX)
	}
	if d := abs(rc.Y - wantY); d > 1.5 {
		t.Errorf("y %.2f want %.2f", rc.Y, wantY)
	}
	if d := abs(rc.W - wantW); d > 1.5 {
		t.Errorf("w %.2f want %.2f", rc.W, wantW)
	}
	if d := abs(rc.H - wantW); d > 1.5 {
		t.Errorf("h %.2f want %.2f", rc.H, wantW)
	}
}

func abs(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}

// grayOf converts a painted fixture to the luma plane the decoder reads.
func grayOf(img *image.RGBA) *gray {
	w, h := img.Bounds().Dx(), img.Bounds().Dy()
	g := &gray{w: w, h: h, px: make([]float64, w*h)}
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			r, gg, bb, _ := img.At(x, y).RGBA()
			g.px[y*w+x] = (0.299*float64(r) + 0.587*float64(gg) + 0.114*float64(bb)) / 257.0
		}
	}
	return g
}

// TestDecodesAnyPalette is the assertion that the detector carries NO colour of
// its own. The page's palette has changed twice -- grey 154/176 with grid lines,
// true black on white, and now a dim grey single pair -- and each change broke a
// decoder that had a colour baked into it. Here the SAME reader is run over
// fixtures painted in three palettes, including the dimmest pair that is still
// above the contrast floor, and every one must decode to the same rectangle.
func TestDecodesAnyPalette(t *testing.T) {
	palettes := []struct {
		name string
		eye  color.RGBA
	}{
		{"#9a9a9a on white (today)", color.RGBA{0x9a, 0x9a, 0x9a, 0xff}},
		{"#000000 on white (the v27 symbol)", color.RGBA{0x00, 0x00, 0x00, 0xff}},
		{"#b4b4b4 dim grey on white", color.RGBA{0xb4, 0xb4, 0xb4, 0xff}},
	}
	for _, p := range palettes {
		t.Run(p.name, func(t *testing.T) {
			fx := defaultFixture()
			fx.Eye = p.eye
			g := grayOf(makeFixture(fx))
			cands := g.candidates(probeCfg{rowStride: 1})
			if len(cands) < 3 {
				t.Fatalf("candidates %d, want >=3", len(cands))
			}
			named, why := assignCorners(cands)
			if why != "" {
				t.Fatalf("assignCorners: %s", why)
			}
			rc, refusal := canvasRect(named, 1.5)
			if rc == nil {
				t.Fatalf("canvasRect refused: %v", refusal)
			}
			if d := abs(rc.X - float64(fx.X)); d > 1.5 {
				t.Errorf("x %.2f want %d", rc.X, fx.X)
			}
			if d := abs(rc.Y - float64(fx.Y)); d > 1.5 {
				t.Errorf("y %.2f want %d", rc.Y, fx.Y)
			}
			if d := abs(rc.W - float64(fx.Side)); d > 1.5 {
				t.Errorf("w %.2f want %d", rc.W, fx.Side)
			}
		})
	}
}
