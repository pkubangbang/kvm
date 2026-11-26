package main

// pad-bbox: the inputpad's canvas rectangle, read from a screenshot of the pad.
//
// It lives beside cmd/kVm-inputpad on purpose (the pad's service is
// cmd/kvm-inputpad): it reads the marks that service's page paints, it is built
// by the same `go build ./...`, and it ships with it. The pad's size recognition
// is part of the pad's distribution, not a side script.
//
// WHAT IT READS
// -------------
// page.html paints three QR finder patterns -- the real ISO/IEC 18004 shape, a
// 3x3 dark core inside a light ring inside a dark ring, 1:1:3:1:1 across any
// centre line -- at the canvas's TL, TR and BL corners, each in a 1-cell light
// separator. The BR corner is bare: that is the QR arrangement, and three marks
// already fix both axes and the scale.
//
// It does NOT ask the pad server anything. AGENTS.md §2: the read-back endpoints
// are gone for good and were never trustworthy -- they answered in the page's
// own viewport coordinates, which are neither screen pixels nor checkable, and a
// wrong answer was indistinguishable from a right one. A mark on screen is the
// only witness that cannot lie about where the canvas is.
//
// THE SQUARE INVARIANT
// --------------------
// page.html makes the canvas an exact square (aspect-ratio 1/1, identical
// max-width and max-height), so w is measured from TL..TR and h from TL..BL
// INDEPENDENTLY and a rect is emitted only if the two agree within -square-tol
// and the three module sizes agree. A disagreement is a distorted image or a
// clipped mark, and then there is NO rect at all.
//
// Coordinates are SCREEN pixels -- the space `kvm mouse abs move X Y` speaks.
//
// Usage
// -----
//     go run ./cmd/pad-bbox -shot SHOT.png [-json] [-v] [-probe]
//     go run ./cmd/pad-bbox -make-fixture .tmp/pad.png     # Go-painted test shot
//     go build -o pad-bbox ./cmd/pad-bbox
//
// Exit codes: 0 = a square rect from all three marks, 1 = no rect, 2 = IO/usage.

import (
	"encoding/json"
	"flag"
	"fmt"
	"image/png"
	"math"
	"os"
	"time"
)

// probeCfg is what the scan needs from the command line, grouped so the scan
// can be exercised without the flag package.
type probeCfg struct {
	rowStride int
	probe     bool
}

// report is the JSON this program prints with -json.
type report struct {
	Shot       string         `json:"shot"`
	ImageWH    [2]int         `json:"image_wh"`
	SeedBand   [2]int         `json:"seed_band"`
	SeedPixels int            `json:"seed_pixels"`
	Candidates int            `json:"candidates"`
	Decode     string         `json:"decode"`
	Markers    map[string]any `json:"markers,omitempty"`
	Canvas     *canvasOut     `json:"canvas,omitempty"`
	CellPx     float64        `json:"cell_px,omitempty"`
	Anchor     [2]int         `json:"anchor,omitempty"`
	Extent     [2]int         `json:"extent,omitempty"`
	Refused    map[string]any `json:"refused,omitempty"`
	Confident  bool           `json:"confident"`
}

type canvasOut struct {
	X int `json:"x"`
	Y int `json:"y"`
	W int `json:"w"`
	H int `json:"h"`
}

// loadGray decodes a PNG into a luminance plane, at the file's own pixels.
// ITU-R BT.601 luma: the three marker levels are pure greys, so any reasonable
// weighting gives the same band, but a fixed one keeps the numbers reportable.
func loadGray(path string) (*gray, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	img, err := png.Decode(f)
	if err != nil {
		return nil, err
	}
	b := img.Bounds()
	g := &gray{w: b.Dx(), h: b.Dy(), px: make([]float64, b.Dx()*b.Dy())}
	for y := 0; y < g.h; y++ {
		for x := 0; x < g.w; x++ {
			r, gg, bb, _ := img.At(b.Min.X+x, b.Min.Y+y).RGBA()
			g.px[y*g.w+x] = (0.299*float64(r) + 0.587*float64(gg) + 0.114*float64(bb)) / 257.0
		}
	}
	return g, nil
}

func main() {
	shot := flag.String("shot", "", "PNG screenshot of the pad")
	grab := flag.Bool("grab", false, "capture the screen in-process (no ffmpeg, no file)")
	asJSON := flag.Bool("json", false, "print one JSON object")
	asBBox := flag.Bool("bbox", false, "print only `x1 y1 x2 y2` on one line (the pad rect)")
	verbose := flag.Bool("v", false, "list every candidate")
	rowStride := flag.Int("row-stride", 2, "scan every Nth line (1 = every pixel row and column)")
	doProbe := flag.Bool("probe", false, "print the seed band, seed population and every candidate, then exit")
	squareTol := flag.Float64("square-tol", 1.5, "max |w-h| in px before the reading is refused")
	mkFixture := flag.String("make-fixture", "", "paint the synthetic pad screenshot to this path and exit")
	flag.Parse()
	if *shot == "" && flag.NArg() > 0 {
		*shot = flag.Arg(0)
	}

	if *mkFixture != "" {
		img := makeFixture(defaultFixture())
		f, err := os.Create(*mkFixture)
		if err != nil {
			fmt.Fprintf(os.Stderr, "pad-bbox: cannot create %s: %v\n", *mkFixture, err)
			os.Exit(2)
		}
		if err := png.Encode(f, img); err != nil {
			f.Close()
			fmt.Fprintf(os.Stderr, "pad-bbox: cannot encode %s: %v\n", *mkFixture, err)
			os.Exit(2)
		}
		if err := f.Close(); err != nil {
			fmt.Fprintf(os.Stderr, "pad-bbox: cannot close %s: %v\n", *mkFixture, err)
			os.Exit(2)
		}
		fmt.Printf("wrote %s (%dx%d, canvas at %d,%d side %d)\n",
			*mkFixture, defaultFixture().W, defaultFixture().H,
			defaultFixture().X, defaultFixture().Y, defaultFixture().Side)
		return
	}

	if *shot == "" && !*grab {
		fmt.Fprintln(os.Stderr, "pad-bbox: -shot is required (or -grab, or -make-fixture OUT)")
		os.Exit(2)
	}
	var g *gray
	var err error
	source := *shot
	if *grab {
		source = "(grab)"
		t0 := time.Now()
		g, err = grabGray()
		t1 := time.Now()
		// Report dimensions only on a successful capture. The old order printed
		// g.w/g.h BEFORE checking err, so on a platform where grabGray fails
		// (it is Windows-only; elsewhere it returns an error) the nil *gray
		// dereferenced here and the tool segfaulted instead of reporting the
		// honest "use -shot" error — measured on Linux/Wayland as
		// SIGSEGV at main.go:156.
		if err == nil {
			fmt.Fprintf(os.Stderr, "pad-bbox: capture %dx%d in %v\n", g.w, g.h, t1.Sub(t0))
		}
	} else {
		g, err = loadGray(*shot)
	}
	if err != nil {
		fmt.Fprintf(os.Stderr, "pad-bbox: cannot read %s: %v\n", source, err)
		os.Exit(2)
	}

	cands := g.candidates(probeCfg{rowStride: *rowStride, probe: *doProbe})
	if *doProbe {
		probe(g, cands, os.Stdout)
		if len(cands) < 3 {
			os.Exit(1)
		}
		return
	}

	named, why := assignCorners(cands)
	rc, refusal := canvasRect(named, *squareTol)

	lo, hi := g.seedBand()
	seed := g.seedMask()
	seedPixels := 0
	for _, b := range seed {
		if b {
			seedPixels++
		}
	}

	rep := report{
		Shot:       *shot,
		ImageWH:    [2]int{g.w, g.h},
		SeedBand:   [2]int{int(math.Round(lo)), int(math.Round(hi))},
		SeedPixels: seedPixels,
		Candidates: len(cands),
		Decode:     fmt.Sprintf("%d/3", len(named)),
	}
	if len(named) > 0 {
		rep.Markers = map[string]any{}
		for k, f := range named {
			rep.Markers[k] = map[string]any{
				"x": round3(f.X), "y": round3(f.Y), "module": round3(f.Module),
				"hits": f.Hits, "mid": round3(f.Mid),
			}
		}
	}
	if why != "" {
		rep.Refused = map[string]any{"reason": why}
	}
	if refusal != nil {
		rep.Refused = refusal
	}
	if rc != nil {
		rep.Canvas = &canvasOut{
			X: int(math.Round(rc.X)), Y: int(math.Round(rc.Y)),
			W: int(math.Round(rc.W)), H: int(math.Round(rc.H)),
		}
		rep.CellPx = round3(rc.W / float64(nCells))
		rep.Anchor = [2]int{rep.Canvas.X, rep.Canvas.Y}
		rep.Extent = [2]int{rep.Canvas.W, rep.Canvas.H}
	}
	rep.Confident = rc != nil && len(named) >= 3

	if *asBBox {
		if rc == nil {
			os.Exit(1)
		}
		x1, y1 := int(math.Round(rc.X)), int(math.Round(rc.Y))
		x2, y2 := x1+int(math.Round(rc.W)), y1+int(math.Round(rc.H))
		fmt.Printf("%d %d %d %d\n", x1, y1, x2, y2)
		return
	}
	if *asJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		_ = enc.Encode(rep)
	} else {
		fmt.Printf("shot        %s  (%dx%d)  seed band %d..%d  seed px %d\n",
			rep.Shot, g.w, g.h, rep.SeedBand[0], rep.SeedBand[1], seedPixels)
		fmt.Printf("candidates  %d   decode %s\n", rep.Candidates, rep.Decode)
		for _, k := range []string{"TL", "TR", "BL"} {
			if f := named[k]; f != nil {
				fmt.Printf("  %-2s  centre (%7.2f,%7.2f)  module %.3f  hits %2d  mid %.1f\n",
					k, f.X, f.Y, f.Module, f.Hits, f.Mid)
			}
		}
		if rc == nil {
			reason := "no rect"
			if refusal != nil {
				reason = fmt.Sprint(refusal["reason"])
			}
			if why != "" {
				reason = why
			}
			fmt.Printf("canvas      REFUSED: %s\n", reason)
			if refusal != nil && refusal["detail"] != nil {
				b, _ := json.Marshal(refusal["detail"])
				fmt.Printf("            %s\n", b)
			}
		} else {
			fmt.Printf("canvas      x=%d y=%d w=%d h=%d  (%.4f px/cell)\n",
				rep.Canvas.X, rep.Canvas.Y, rep.Canvas.W, rep.Canvas.H, rep.CellPx)
		}
		if *verbose {
			for _, f := range cands {
				fmt.Printf("  cand (%7.2f,%7.2f) module %.3f n=%d seed=%d mid %.1f\n",
					f.X, f.Y, f.Module, f.Hits, f.Seed, f.Mid)
			}
		}
	}
	if !rep.Confident {
		os.Exit(1)
	}
}
