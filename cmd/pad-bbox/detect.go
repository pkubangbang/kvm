package main

// detect.go -- read the three QR finder patterns out of a screenshot.
//
// HOW A QR READER FINDS A FINDER, AND WHY WE DO THE SAME
// ------------------------------------------------------
// Nothing here assumes a grid, a cell size or a colour. A finder pattern is
// found the way ISO/IEC 18004 readers (ZXing, ZBar, quirc) find it: walk RUN
// LENGTHS along a scan line and accept five consecutive runs whose lengths are
// 1:1:3:1:1 (dark, light, dark, light, dark) within half a module per run.
// The module size is then MEASURED (total/7), never taken from a constant --
// that is what lets the same code read a 512 px canvas and a 1400 px one.
//
//   - a row hit is only accepted after a vertical, then horizontal, then two
//     diagonal cross-checks through the same centre (the ring must close);
//   - hits are merged by centre, so one mark yields many confirmations;
//   - three marks are then named TL / TR / BL -- the QR triad -- by picking the
//     best isosceles right triangle of agreeing marks.
//
// THE ONE THING THAT IS *NOT* LIKE A QR READER: THE SEED IS BANDED
// --------------------------------------------------------------
// A QR reader binarizes the whole image, because a QR symbol is white paper
// with black modules. THIS page has NO BLACK ANYWHERE -- its own pixel
// self-report reads `dark 0` -- and its dark modules are a DIM GREY (#9a9a9a,
// luma ~154) on white (255). Ink and the page background are ~17. An older
// generation of the page used 154 on 176 with #e6e6e6/#c8c8c8 grid lines.
//
//     pad / eye's light ring   #ffffff   (255)
//     eye dark module          #9a9a9a   (~154)   <- not black, by design
//     page background / ink    #111111   ( ~17)
//
// Measured consequence of any GLOBAL threshold: Otsu over the whole screenshot
// splits at ~40 between the page (17) and the pad (255), calls the pad "dark",
// and the mask either selects the page (1260000 px = the whole 1400x900 frame) or
// selects the eyes *inverted*. No tolerance fix rescues a mask whose polarity is
// backwards -- that was the historic "0 candidates" failure.
//
// So the mask here is a BAND taken from the image itself:
//
//     padLight = the brightest well-populated luma level (the pad's own white)
//     seed     = 0 <= luma <= padLight - seedGap          (seedGap = 20)
//
// That contains the dark modules of ANY palette the page can paint -- 0 for true
// black, 154 for #9a9a9a, 120 for a dimmer grey -- and excludes the pad. There is
// deliberately NO absolute colour constant in this file: the only numbers are the
// gap and a contrast floor. The band is a SEED only: every hit is then
// re-binarized inside its own window at that window's own split (splitFromBand),
// so a display whose greys drift still reads correctly, and a flat region can
// never be mistaken for a mark (contrast floor).
//
// A window is rejected if its contrast is below minMarkContrast -- on this pad an
// eye carries its own two levels (154 vs 255); a pad region or a page region has
// no two levels at all.'

import (
	"encoding/json"
	"fmt"
	"math"
	"os"
	"sort"
)

const (
	// nCells is the pad's grid: a 128x128 cell canvas (page.html `const N=128`).
	nCells = 128
	// THE EYES ARE INSET ONE MODULE. Three 7x7 finders at grid cells
	// (1,1), (119,1) and (1,119) of the 128-cell canvas: the pattern starts one
	// whole cell in, so the TL finder no longer touches the canvas corner and
	// the canvas rect is NOT the eyes' bounding box. Measured from the page's own
	// self-report: `bbox 6,6..755,755` on a 768 px canvas, i.e. 1 cell of margin.
	mkRing = 7
	// eyeOffset is that inset, in modules. It is the difference between "where a
	// finder's centre is" and "where the canvas corner is", and it is the ONE
	// number that changed when the eyes moved.
	eyeOffset = 1
	// qrSize is the symbol's module count; qrSpan is the distance in MODULES
	// between the TL and TR finder centres (119 - 1). The canvas is nCells
	// modules square, and the extent is taken from nCells -- never from qrSpan,
	// which would report the eye bbox (125 modules) as if it were the canvas.
	qrSize = 125
	qrSpan = 118
	// finderCentre is a finder's centre in modules from the canvas corner.
	finderCentre = eyeOffset + 3.5

	// nRuns is how many runs a finder has across ANY line through its centre:
	// dark border, light ring, dark core, light ring, dark border.
	nRuns    = 5
	ratioBig = 3 // the centre run is three modules wide
)

const (
	// THE SEED IS RELATIVE TO THE PAD, NOT A GREY BAND.
	//
	// The retired constants seedMin/seedMax = 100..215 encoded the OLD paints:
	// 154 dark on 176 light, plus #e6e6e6/#c8c8c8 grid lines. On the black-on-
	// white symbol that band matches NOTHING (modules sit at 0 and 255) and on
	// a dim-grey pair it is only accidentally right, so the band is gone.
	//
	// What replaces it is one measured reference: the PAD'S OWN LIGHT LEVEL,
	// taken from the image as the brightest well-populated level. Everything
	// seedGap below it is a candidate dark module. On the black-on-white symbol
	// that is 0..235; on dim grey eyes 120..235; on the page background (~17)
	// the seed still lights up but forms ONE huge blob that blobs() rejects on
	// area, and ink is a thin stroke with no 1:1:3:1:1 crossing. The colour is
	// therefore never a constant here: only the GAP is, and the gap is what
	// "dimmed to grey" and "true black" have in common.
	seedGap = 20.0

	// padTol is how far below the pad's own bright level a pixel may be and still
	// count as part of the pad's SURFACE. It locates the pad (padRect), and it is
	// deliberately small: the surface is flat white, and the eyes inside it are
	// far below it (154 on white, 0 on black).
	padTol = 8.0

	// minMarkContrast is the least light-minus-dark gap a window must show
	// before its own midpoint may be believed. It is a CONTRAST floor only --
	// no level, no colour -- so it holds for 0/255 and for 120/255 alike.
	minMarkContrast = 11.0

	// mergeDx is how close two hits must be, in modules, to be one mark.
	mergeDx = 1.5
)

var ratioWant = [nRuns]int{1, 1, ratioBig, 1, 1}

// dbgMeasure is set by candidates() from PADBBOX_DEBUG: it makes measureFinder
// print every run window it read, so a rejected blob names its own numbers.
var dbgMeasure bool

// finder is one decoded mark: a centre in image pixels, a measured module, and
// the local threshold it was confirmed at.
type finder struct {
	X, Y   float64
	Module float64
	Hits   int
	Mid    float64
	Seed   int // bitmask of the runs the seed scan found it on (diagnostic)
}

func (f *finder) merge(x, y, module, mid float64) {
	n := float64(f.Hits)
	f.X = (f.X*n + x) / (n + 1)
	f.Y = (f.Y*n + y) / (n + 1)
	f.Module = (f.Module*n + module) / (n + 1)
	f.Mid = (f.Mid*n + mid) / (n + 1)
	f.Hits++
}

// gray is the screenshot as a luminance plane, at ORIGINAL pixels: no resize,
// no crop, so every coordinate reported is a pixel of the file handed in.
type gray struct {
	w, h int
	px   []float64
	// lastMid is the threshold the current blob was split at; runsAtRow reads it
	// so it can walk the image without carrying a mask.
	lastMid float64
}

// bandMask is the SEED: which pixels could be a dark marker module. The band is
// derived from the image (padLight, below), never from a painted colour.
func (g *gray) bandMask(lo, hi float64) []bool {
	m := make([]bool, len(g.px))
	for i, v := range g.px {
		m[i] = v >= lo && v <= hi
	}
	return m
}

// levelHist samples the whole frame on a stride and returns the luma histogram
// plus the number of samples. It is the one place an image-wide statistic is
// taken, so padLight and pageDark cannot disagree about the population.
func (g *gray) levelHist() ([256]int, int) {
	var hist [256]int
	const stride = 2
	n := 0
	for y := 0; y < g.h; y += stride {
		row := g.px[y*g.w:]
		for x := 0; x < g.w; x += stride {
			v := int(row[x] + 0.5)
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			hist[v]++
			n++
		}
	}
	return hist, n
}

// padLight is the PAD'S OWN LIGHT LEVEL in this image: the brightest luma level
// that holds a real share of the frame. It is the reference the seed's upper
// bound is taken from, so the detector carries no colour of its own.
//
// WHY THE PAD AND NOT A GLOBAL OTSU: Otsu over the whole frame splits between the
// page (17) and the pad (255), i.e. at ~40. That value calls the pad "dark" and
// the page "light", and a mask built on it selects either the entire page or the
// markers inverted -- the historic "0 candidates" failure.
func (g *gray) padLight() float64 {
	hist, n := g.levelHist()
	if n == 0 {
		return 255
	}
	floor := n / 500
	if floor < 8 {
		floor = 8
	}
	for i := 255; i >= 0; i-- {
		if hist[i] >= floor {
			return float64(i)
		}
	}
	return 255
}

// pageDark is the DARKEST well-populated luma level: the page background (and,
// on this page, ink -- they are the same #111). It exists because the page
// background lies BELOW padLight-seedGap and would otherwise seed, and once the
// frame is seeded the page and any mark touching the canvas edge become ONE blob
// (measured: the three eye frames merged into the page and only their 3x3 cores
// survived as separate components).
//
// Excluding it costs nothing: a dark module is painted at a level of its own, and
// on every palette this page has used that level is either far from the
// background (154 vs 17) or pinned at zero (0 vs 17). The tolerance below is in
// grey levels, not a colour.
func (g *gray) pageDark() float64 {
	hist, n := g.levelHist()
	if n == 0 {
		return 0
	}
	floor := n / 50
	if floor < 8 {
		floor = 8
	}
	for i := 0; i < 256; i++ {
		if hist[i] >= floor {
			return float64(i)
		}
	}
	return 0
}

// seedBand is the luma range the seed accepts, taken from the image: anything
// seedGap or more below the pad's own light level. It is reported, not used as
// the whole mask -- seedMask is the mask, because the band's low end must also
// skip the page background.
func (g *gray) seedBand() (lo, hi float64) {
	light := g.padLight()
	return 0, math.Max(0, light-seedGap)
}

// padRect is the bounding box of the pad's own bright surface: every pixel within
// padTol of padLight.
//
// WHY THIS EXISTS, AND WHY LUMA ALONE CANNOT DO IT. The page behind the pad is a
// GRADIENT spanning luma 15..50, and on a black-on-white palette an eye module is
// luma 0 -- i.e. the page and the eye OVERLAP in luma and no absolute or relative
// luma rule can tell them apart. Geometry can: the eyes are INSIDE the pad and the
// page is OUTSIDE it. So the pad is located first, from the one property that is
// unmistakable on every screenshot of this page -- it is the large bright surface
// -- and the seed is then restricted to its interior, where "darker than the pad"
// is the only distinction that has to be made.
func (g *gray) padRect() (x0, y0, x1, y1 int, ok bool) {
	light := g.padLight()
	lo := light - padTol
	// The bright mask, then its LARGEST CONNECTED COMPONENT.
	//
	// A global bounding box is not enough: a desktop screenshot has other bright
	// surfaces (an editor, a panel, a dialog), and their bbox would span the whole
	// frame and re-admit the page inside it (measured: seed px 1522494, one blob
	// over the entire screen, zero candidates). The pad is the largest single
	// bright region, and the eyes sit INSIDE it -- their white surrounds them, so
	// the pad's white stays one component even with three holes in it.
	bright := make([]bool, len(g.px))
	for i, v := range g.px {
		bright[i] = v >= lo
	}
	seen := make([]bool, len(g.px))
	stack := make([]int, 0, 1<<16)
	bestN := 0
	bx0, by0, bx1, by1 := 0, 0, 0, 0
	for start := 0; start < len(bright); start++ {
		if !bright[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		cx0, cy0, cx1, cy1, n := g.w, g.h, -1, -1, 0
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := p%g.w, p/g.w
			n++
			if x < cx0 {
				cx0 = x
			}
			if x > cx1 {
				cx1 = x
			}
			if y < cy0 {
				cy0 = y
			}
			if y > cy1 {
				cy1 = y
			}
			if x > 0 && bright[p-1] && !seen[p-1] {
				seen[p-1] = true
				stack = append(stack, p-1)
			}
			if x < g.w-1 && bright[p+1] && !seen[p+1] {
				seen[p+1] = true
				stack = append(stack, p+1)
			}
			if y > 0 && bright[p-g.w] && !seen[p-g.w] {
				seen[p-g.w] = true
				stack = append(stack, p-g.w)
			}
			if y < g.h-1 && bright[p+g.w] && !seen[p+g.w] {
				seen[p+g.w] = true
				stack = append(stack, p+g.w)
			}
		}
		if n > bestN {
			bestN, bx0, by0, bx1, by1 = n, cx0, cy0, cx1, cy1
		}
	}
	if bestN < (g.w*g.h)/256 || bx1 <= bx0 || by1 <= by0 {
		return 0, 0, 0, 0, false
	}
	return bx0, by0, bx1 + 1, by1 + 1, true
}

// seedMask is THE SEED: pixels INSIDE the pad that are dark enough to be an eye
// module.
//
//	padLight = brightest well-populated level      (the pad's white)
//	padRect  = where that surface is               (the pad's bounds)
//	seed     = inside padRect AND luma <= padLight - seedGap
//
// There is no colour constant anywhere: #9a9a9a on white, a dimmed grey eye, and
// #000000 on white all seed, and the page behind the pad is excluded by POSITION
// rather than by level -- which is the only way to exclude a gradient that spans
// the same luma range as a black module.
func (g *gray) seedMask() []bool {
	m := make([]bool, len(g.px))
	lo, hi := g.seedBand()
	// SEED ON THE LUMA BAND ALONE, NOT INSIDE padRect. The old form returned
	// an all-false mask when padRect() was !ok (its `return m` on !ok) and when
	// the rect was mis-measured, so the finder decode saw ZERO seed pixels and
	// 0/3 marks on every frame (measured: seed_pixels 0 on a fresh capture).
	// The user's rule is "allow the noise and increase the capability of
	// detection": the seed is a CANDIDATE band over the whole frame, and the
	// FFT matched filter (findInBlobFFT) is what rejects grid rules and ink by
	// their spectrum. pageDark is excluded so the page background (which is
	// BELOW padLight-seedGap and would otherwise seed) does not merge every
	// mark into one blob.
	page := g.pageDark()
	for i := range m {
		v := g.px[i]
		m[i] = v >= lo && v <= hi && v > page+1
	}
	return m
}

// windowStats describes one rectangle by its own two populations: the mean of
// the darker side, the mean of the lighter side, and the midpoint between them.
// It is Otsu restricted to the window, so it is blind to the pad and the page.
func (g *gray) windowStats(x0, y0, x1, y1 int) (dark, light, mid float64, ok bool) {
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > g.w {
		x1 = g.w
	}
	if y1 > g.h {
		y1 = g.h
	}
	if x1-x0 < 1 || y1-y0 < 1 {
		return 0, 0, 0, false
	}
	var hist [256]float64
	n := 0.0
	for y := y0; y < y1; y++ {
		row := g.px[y*g.w:]
		for x := x0; x < x1; x++ {
			v := int(row[x] + 0.5)
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			hist[v]++
			n++
		}
	}
	if n == 0 {
		return 0, 0, 0, false
	}
	var sumAll float64
	for i := 0; i < 256; i++ {
		sumAll += float64(i) * hist[i]
	}
	var w0, sum0, best, bestT float64
	bestT = -1
	for t := 0; t < 255; t++ {
		w0 += hist[t]
		sum0 += float64(t) * hist[t]
		w1 := n - w0
		if w0 == 0 || w1 == 0 {
			continue
		}
		m0 := sum0 / w0
		m1 := (sumAll - sum0) / w1
		between := w0 * w1 * (m0 - m1) * (m0 - m1)
		if between > best {
			best, bestT = between, float64(t)
		}
	}
	if bestT < 0 {
		return 0, 0, 0, false
	}
	var lw, ls, hw, hs float64
	for i := 0; i <= int(bestT); i++ {
		lw += hist[i]
		ls += float64(i) * hist[i]
	}
	for i := int(bestT) + 1; i < 256; i++ {
		hw += hist[i]
		hs += float64(i) * hist[i]
	}
	if lw == 0 || hw == 0 {
		return 0, 0, 0, false
	}
	return ls / lw, hs / hw, bestT + 0.5, true
}

// localMask fills m inside the window with "is this pixel the MARKER'S DARK
// side", using the window's own midpoint. It returns the midpoint and whether
// the window is a believable two-level patch.
func (g *gray) localMask(m *markMask, x0, y0, x1, y1 int) (float64, bool) {
	dark, light, mid, ok := g.windowStats(x0, y0, x1, y1)
	if !ok || light-dark < minMarkContrast {
		return 0, false
	}
	for y := y0; y < y1; y++ {
		row := g.px[y*g.w:]
		for x := x0; x < x1; x++ {
			m.px[y*g.w+x] = row[x] <= mid
		}
	}
	return mid, true
}

func window(x, y, span, w, h int) (int, int, int, int) {
	x0, y0 := x-span, y-span
	x1, y1 := x+span+1, y+span+1
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > w {
		x1 = w
	}
	if y1 > h {
		y1 = h
	}
	return x0, y0, x1, y1
}

// runsAt walks from (x,y) along (dx,dy) collecting run lengths of the mask,
// starting with the value at (x,y). The caller controls which colour starts the
// 1:1:3:1:1 window, which is why this does not "normalise" to dark-first.
//
// NOISE TOLERANCE (the fix this got wrong once): the pad paints light grid
// rules every 8 cells, and a rule crossing a finder SPLITS a light run into
// two (or merges a dark edge), so the raw walk returns 6..7 runs where the
// pattern has 5 and finderRuns rejects a finder the same program painted. The
// page must NOT avoid this (no skip-rings — the user's rule: allow the noise
// and make the detector stronger). So: runs are MERGED here when the split is
// a noise artefact — a run too short to be a module (below the local module
// estimate) joins its neighbour of the SAME value, and a one-pixel dip inside
// a run of the other value is bridged. The merged sequence is what the ratio
// test sees.
func (g *gray) runsAt(m *markMask, x, y, dx, dy, limit int) []int {
	out := make([]int, 0, nRuns+4)
	if x < 0 || x >= g.w || y < 0 || y >= g.h {
		return out
	}
	// `limit` bounds TOTAL run length, not a single run: on the fixture a mark
	// cell is 4 px, so a per-run limit of 4 truncated every run at 4 and the
	// 3-module centre (12 px) could never be read -- measured: "no 1:1:3:1:1
	// crossing" on a correct painter.
	//
	// The walk is also CLIPPED TO THE MASK'S WINDOW. Without that the walk runs
	// out of the finder into the white pad and reports a single 500 px light run
	// -- measured "runs at 328,135 -> [4 508]" where a finder needs 4,4,12,4,4.
	value := m.at(x, y)
	n, total := 0, 0
	for m.in(x, y) && total < limit {
		if m.at(x, y) != value {
			out = append(out, n)
			value = !value
			n = 0
			continue
		}
		n++
		total++
		x += dx
		y += dy
	}
	if n > 0 {
		out = append(out, n)
	}
	return mergeNoiseRuns(out)
}

// mergeNoiseRuns folds runs that a grid rule (or ink speck) split, so the
// sequence the ratio test sees has the finder's true 5 runs.
//
// Two artefacts, both measured on this pad:
//   - a light rule through a light ring splits it into two short light runs
//     with a one-pixel dark dip between: [4, 3, 1, 3, 12, 3, 1, 3, 4] must
//     become [4, 4, 12, 4, 4];
//   - a rule through a DARK run splits the dark run the same way.
//
// The bridging rule: a run of ONE value that is much shorter than both
// neighbours' runs of the OTHER value and shorter than a module is a noise
// dip — merge the two neighbours across it. The module estimate is the median
// run length, robust to the 3-module core. The loop repeats until stable, so a
// rule crossing two rings is folded in one pass per ring.
func mergeNoiseRuns(runs []int) []int {
	for {
		if len(runs) < 3 {
			return runs
		}
		// module estimate: the median run (the 3-module core is an outlier)
		sorted := append([]int(nil), runs...)
		for i := 1; i < len(sorted); i++ {
			for j := i; j > 0 && sorted[j-1] > sorted[j]; j-- {
				sorted[j-1], sorted[j] = sorted[j], sorted[j-1]
			}
		}
		module := sorted[len(sorted)/2]
		if module < 1 {
			module = 1
		}
		changed := false
		out := make([]int, 0, len(runs))
		for i := 0; i < len(runs); {
			// a noise dip: runs[i] is short and runs[i-1], runs[i+1] are the
			// same colour (they are, because runs alternate) and much longer
			if i > 0 && i+1 < len(runs) &&
				runs[i] < module && runs[i] <= runs[i-1]/2 && runs[i] <= runs[i+1]/2 {
				// fold the dip into its neighbours
				if len(out) > 0 {
					out[len(out)-1] += runs[i] + runs[i+1]
				}
				i += 2
				changed = true
				continue
			}
			out = append(out, runs[i])
			i++
		}
		runs = out
		if !changed {
			return runs
		}
	}
}

// isFinderRatio is ZXing's foundPatternCross: 1:1:3:1:1, half a module of
// tolerance per run. That tolerance is what keeps ordinary runs out.
//
// finderRuns is the PAD'S variant, and the difference is geometric, not a
// loosened tolerance. The three eyes are painted FLUSH at the canvas edge, so the
// OUTERMOST dark run of the TL and TR eyes merges with whatever lies beyond the
// canvas -- on this page that is #111, which is DARKER than the eye itself. The
// merged run is therefore unbounded to the outside and can only ever be measured
// as a lower bound; demanding 1:1 there rejects a finder the same program painted
// (measured: runs [1 4 12 4 4], i.e. the border truncated to the pixel the walk
// landed on). The three INNER runs -- light ring, 3-module core, light ring --
// are fully enclosed by the eye and are still held to the strict ratio, which is
// what actually identifies the pattern.
func isFinderRatio(runs []int) bool {
	return finderRuns(runs, 0)
}

// finderRuns validates the five runs. wantModule <= 0 means "the ratio alone is
// the test" (used by TestFinderRatio); a positive wantModule additionally pins the
// core to that module size.
func finderRuns(runs []int, wantModule float64) bool {
	if len(runs) != nRuns {
		return false
	}
	total := 0
	for _, r := range runs {
		if r <= 0 {
			return false
		}
		total += r
	}
	if total < 7 {
		return false
	}
	module := float64(total) / 7.0
	tol := module / 2.0
	for i, r := range runs {
		if i == 0 || i == nRuns-1 {
			// an outer border may be truncated by the canvas edge; it may never
			// be fatter than the one module the pattern allows
			if float64(r) > 2.5*module {
				return false
			}
			continue
		}
		if math.Abs(float64(ratioWant[i])*module-float64(r)) >= float64(ratioWant[i])*tol {
			return false
		}
	}
	if wantModule > 0 {
		core := float64(runs[2]) / float64(ratioBig)
		if math.Abs(core-wantModule) > 0.5*wantModule {
			return false
		}
	}
	return true
}

// centreOf is the middle of the 3-module centre run, given where the walk began.
func centreOf(x, y float64, dx, dy int, runs []int) (float64, float64) {
	off := float64(runs[0]+runs[1]) + float64(runs[2])/2.0
	return x + float64(dx)*off, y + float64(dy)*off
}
// findInBlob confirms one seed blob as a finder pattern.
//
// WHY THE BLOB'S EDGES ARE NOT TRUSTED. A seed blob is every pixel whose luma
// falls in the marker band, so on the pad it also swallows the grid lines that
// findInBlob confirms one seed blob as a finder pattern.
//
// WHY THE BLOB'S EDGES ARE NOT TRUSTED. A seed blob is every pixel whose luma
// falls in the marker band, so on the pad it also swallows the grid lines that
// cross the mark (measured: a 64x64 px blob over a mark whose 9 painted cells
// are only 36 px) and its bounding box therefore lies outside the finder in
// every direction. The centre of that box, on the other hand, is the centre of
// the finder: the grid lines cross the mark symmetrically.
//
// So the box supplies a CENTRE and a MODULE SIZE, nothing else. The finder is
// then measured by scanning outward from that centre, which is what a QR
// blob is one connected component of the seed band: a candidate MARK.
// crossCheck re-reads 1:1:3:1:1 through a candidate centre four ways: vertical,
// horizontal and both diagonals. The diagonals prove the ring closes.
//
// THE WALK STARTS AT THE OUTER BORDER. Starting it at the edge of the dark core
// reads only four runs -- core, light ring, border, then white -- and a correct
// finder is rejected (measured: "crossCheck failed at 313.50,606.00 module 4.000"
// on a fixture the same program had just painted). So the walk steps back over
// the light ring to the FIRST pixel of the dark border, which is where a QR
// reader's five-run window begins.
func (g *gray) crossCheck(cx, cy, wantModule, mid float64) bool {
	darkAt := func(x, y int) bool { return g.px[y*g.w+x] <= mid }
	stepMax := int(math.Ceil(1.5 * wantModule))
	if stepMax < 2 {
		stepMax = 2
	}
	read := func(dx, dy int) bool {
		ix, iy := int(math.Round(cx)), int(math.Round(cy))
		if ix < 0 || ix >= g.w || iy < 0 || iy >= g.h || !darkAt(ix, iy) {
			return false
		}
		// the start of the dark core that contains the centre
		bx, by := ix, iy
		for bx-dx >= 0 && bx-dx < g.w && by-dy >= 0 && by-dy < g.h && darkAt(bx-dx, by-dy) {
			bx -= dx
			by -= dy
		}
		// across the light ring ...
		bx, by = bx-dx, by-dy
		for n := 0; n < stepMax && bx >= 0 && bx < g.w && by >= 0 && by < g.h && !darkAt(bx, by); n++ {
			bx -= dx
			by -= dy
		}
		// ... to the first pixel of the dark border (bounded: past a flush edge
		// lies the page, which is darker than the eye and would swallow the walk)
		if bx < 0 || bx >= g.w || by < 0 || by >= g.h || !darkAt(bx, by) {
			return false
		}
		for n := 0; n < stepMax && bx-dx >= 0 && bx-dx < g.w && by-dy >= 0 && by-dy < g.h && darkAt(bx-dx, by-dy); n++ {
			bx -= dx
			by -= dy
		}
		runs := g.runsAtRow(bx, by, dx, dy, 4*nCells)
		return finderRuns(runs, wantModule)
	}
	return read(0, 1) && read(1, 0) && read(1, 1) && read(1, -1)
}
type blob struct {
	x0, y0, x1, y1 int
	n              int
}

func (g *gray) findInBlob(b blob, scratch []bool) (*finder, bool) {
	w := b.x1 - b.x0 + 1
	h := b.y1 - b.y0 + 1
	dark, light, mid, ok := g.windowStats(b.x0, b.y0, b.x1+1, b.y1+1)
	if !ok || light-dark < minMarkContrast {
		if dbgMeasure {
			fmt.Fprintf(os.Stderr, "    reject: blob contrast %.1f (dark %.1f light %.1f)\n", light-dark, dark, light)
		}
		return nil, false
	}
	g.lastMid = mid
	mid = g.splitFromBand(b.x0, b.y0, b.x1+1, b.y1+1)
	// Two independent module estimates: the blob's box is the whole 7-module
	// finder plus whatever ink or edge slack touches it, the centre run is
	// exactly 3 modules wide. Take the smaller so an inflated box cannot make
	// the ring look one module wide.
	guess := math.Min(float64(w), float64(h)) / float64(mkRing)
	mod := math.Min(guess, (float64(w)/float64(h)+float64(h)/float64(w))/2*guess)
	cx := (float64(b.x0) + float64(b.x1) + 1) / 2
	cy := (float64(b.y0) + float64(b.y1) + 1) / 2
	f, ok := g.measureFinder(cx, cy, mod, mid)
	if !ok {
		if dbgMeasure {
			fmt.Fprintf(os.Stderr, "    reject: no 1:1:3:1:1 through blob centre %.1f,%.1f (module guess %.2f, mid %.1f, box %dx%d)\n",
				cx, cy, mod, mid, w, h)
		}
		return nil, false
	}
	if !g.crossCheck(f.X, f.Y, f.Module, mid) {
		if dbgMeasure {
			fmt.Fprintf(os.Stderr, "    reject: crossCheck failed at %.2f,%.2f module %.3f\n", f.X, f.Y, f.Module)
		}
		return nil, false
	}
	return f, true
}

// measureFinder reads the finder pattern that surrounds (cx, cy).
//
// THE READ IS A CENTRED WINDOW, NOT A LINE. The finder is centred on (cx, cy)
// and is 7 modules across, so any 3-module span centred on the centre is dark;
// every such span is a horizontal line through the core. That is the fact the
// walk uses: it walks a scan line to the dark core that CONTAINS the centre --
// a span of dark at least 2 modules long with the centre inside it -- and then
// reads the five runs outward from the start of that core. A line that merely
// crosses the ring (the mark's outer rows, measured "runs at 300,120 -> [66]")
// has no such core and is skipped.
//
// This makes the read independent of which pixel the walk started on and of any
// grid line crossing the mark: a 1 px light grid stripe inside the core is
// simply part of the measurement noise the ratio tolerance absorbs, and a grid
// stripe outside the core falls inside a light ring, where it cannot split a run
// because grid and ring are both "light" at the mark's own threshold.
func (g *gray) measureFinder(cx, cy, wantModule, mid float64) (*finder, bool) {
	darkAt := func(x, y int) bool { return g.px[y*g.w+x] <= mid }
	// The finder spans 7 modules; a line through its centre is scanned from two
	// modules outside the centre, so the window always covers the whole pattern.
	reach := int(math.Ceil(7 * wantModule / 2))
	if reach < 8 {
		reach = 8
	}
	// The minimum dark core is 3 modules wide; allow half a module of slack so a
	// slightly small guess does not reject a correct mark, and never below 2 px.
	minCore := int(math.Floor(2.5 * wantModule))
	if minCore < 2 {
		minCore = 2
	}
	// stepMax bounds every walk that crosses a light ring to reach a dark border.
	// It is NOT an optimisation: the TL and TR eyes are flush at the canvas edge
	// and the page beyond it (#111, luma 17) is darker than the eye, so an
	// unbounded "step while dark" leaves the canvas and never returns.
	stepMax := int(math.Ceil(1.5 * wantModule))
	if stepMax < 2 {
		stepMax = 2
	}

	// horizontal reads: one per row of the window
	var mods []float64
	var sumX, sumY float64
	hits := 0
	record := func(x0, y0, dx, dy int, runs []int) {
		mods = append(mods, float64(runs[2])/ratioBig)
		px, py := centreOf(float64(x0), float64(y0), dx, dy, runs)
		sumX += px
		sumY += py
		hits++
	}
	i0, i1 := int(math.Round(cx)), int(math.Round(cy))
	for dy := -reach; dy <= reach; dy++ {
		y := i1 + dy
		if y < 0 || y >= g.h {
			continue
		}
		// find the dark core containing column i0
		s, e := i0, i0
		if s < 0 || s >= g.w || !darkAt(s, y) {
			continue
		}
		for s-1 >= 0 && darkAt(s-1, y) {
			s--
		}
		for e+1 < g.w && darkAt(e+1, y) {
			e++
		}
		if e-s+1 < minCore {
			continue
		}
		// walk outward from the core's left edge: the run above the core is the
		// light ring, and past it the dark border. BOTH steps are BOUNDED by the
		// pattern's own size, because the TL and TR eyes are flush at the canvas
		// edge and the page beyond it (#111, luma 17) is DARKER than the eye: an
		// unbounded "step while dark" walks straight out of the canvas into the
		// page and reports one 500 px run (measured: "runs at 0,128 -> [43]").
		// The border is one module wide, so 1.5 modules is a generous bound.
		stepMax := int(math.Ceil(1.5 * wantModule))
		if stepMax < 2 {
			stepMax = 2
		}
		x := s - 1
		for n := 0; n < stepMax && x >= 0 && !darkAt(x, y); n++ {
			x--
		}
		if x < 0 || !darkAt(x, y) {
			continue
		}
		for n := 0; n < stepMax && x-1 >= 0 && darkAt(x-1, y); n++ {
			x--
		}
		runs := g.runsAtRow(x, y, 1, 0, reach*3)
		if dbgMeasure {
			fmt.Fprintf(os.Stderr, "      runs at %d,%d d=1,0 -> %v\n", x, y, runs)
		}
		if !isFinderRatio(runs) {
			continue
		}
		record(x, y, 1, 0, runs)
	}
	for dx := -reach; dx <= reach; dx++ {
		x := i0 + dx
		if x < 0 || x >= g.w {
			continue
		}
		s, e := i1, i1
		if s < 0 || s >= g.h || !darkAt(x, s) {
			continue
		}
		for s-1 >= 0 && darkAt(x, s-1) {
			s--
		}
		for e+1 < g.h && darkAt(x, e+1) {
			e++
		}
		if e-s+1 < minCore {
			continue
		}
		y := s - 1
		for n := 0; n < stepMax && y >= 0 && !darkAt(x, y); n++ {
			y--
		}
		if y < 0 || !darkAt(x, y) {
			continue
		}
		for n := 0; n < stepMax && y-1 >= 0 && darkAt(x, y-1); n++ {
			y--
		}
		runs := g.runsAtRow(x, y, 0, 1, reach*3)
		if dbgMeasure {
			fmt.Fprintf(os.Stderr, "      runs at %d,%d d=0,1 -> %v\n", x, y, runs)
		}
		if !isFinderRatio(runs) {
			continue
		}
		record(x, y, 0, 1, runs)
	}
	if hits == 0 {
		return nil, false
	}
	sort.Float64s(mods)
	module := mods[len(mods)/2]
	return &finder{X: sumX / float64(hits), Y: sumY / float64(hits), Module: module, Hits: hits, Mid: mid}, true
}

// runsAtRow is runsAt without a mask: it walks the image itself and clips only at
// the image edge, stopping once the total walk exceeds limit. Unlike runsAt it
// keeps the run it is on when the limit lands mid-run, so a caller that walks past
// the five runs still sees five.
func (g *gray) runsAtRow(x, y, dx, dy, limit int) []int {
	out := make([]int, 0, nRuns+2)
	if x < 0 || x >= g.w || y < 0 || y >= g.h {
		return out
	}
	darkAt := func(x, y int) bool { return g.px[y*g.w+x] <= g.lastMid }
	value := darkAt(x, y)
	n, total := 0, 0
	for x >= 0 && x < g.w && y >= 0 && y < g.h {
		if darkAt(x, y) != value {
			out = append(out, n)
			value = !value
			n = 0
			if len(out) >= nRuns || total >= limit {
				return out
			}
			continue
		}
		n++
		total++
		if total > limit {
			break
		}
		x += dx
		y += dy
	}
	if n > 0 {
		out = append(out, n)
	}
	return out
}

// 1:1:3:1:1 the way the page paints it.
//
// The blob is grown a few pixels first so the quiet-zone ring around the finder
// is included -- without it the outer dark ring can touch the window edge and
// the run walk would see a truncated pattern.
// lowTwoPeaks finds the two LOWEST levels that are populated in a window and
// returns the midpoint between them. Those two levels are the finder's own pair
// (154 dark, 176 light on this rig); everything brighter -- the pad at 255 -- is
// excluded, because including it drags any mean-based threshold above the light
// ring and makes the whole mark read as one dark run (measured: "[22 490]").
func (g *gray) lowTwoPeaks(x0, y0, x1, y1 int) float64 {
	var hist [256]int
	n := 0
	for y := y0; y < y1; y++ {
		row := g.px[y*g.w:]
		for x := x0; x < x1; x++ {
			v := int(row[x] + 0.5)
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			hist[v]++
			n++
		}
	}
	if n == 0 {
		return 0
	}
	// A level counts as populated once it holds a module-ish share of the window.
	floor := n / 200
	if floor < 1 {
		floor = 1
	}
	var peaks []int
	for i := 0; i < 256; i++ {
		if hist[i] < floor {
			continue
		}
		// Collapse neighbouring bins into one peak (a level smeared by antialiasing).
		if len(peaks) > 0 && i-peaks[len(peaks)-1] <= 2 {
			continue
		}
		peaks = append(peaks, i)
	}
	if len(peaks) < 2 {
		return float64(peaks[0])
	}
	return float64(peaks[0]+peaks[1]) / 2.0
}
// markMask is a two-level picture of ONE window, kept at full-frame index
// width so every scanner can index it with y*g.w+x, but carrying its own
// bounds so a scan is CLIPPED to the window. Outside the window it answers
// "light": a walk must not wander into the pad.
type markMask struct {
	w, h   int
	x0, y0 int
	x1, y1 int
	px     []bool
}

// at is the only way to read a mask pixel; out of bounds is "light".
func (m *markMask) at(x, y int) bool {
	if m == nil || x < m.x0 || x >= m.x1 || y < m.y0 || y >= m.y1 {
		return false
	}
	return m.px[y*m.w+x]
}

// in reports whether (x,y) is inside the window the mask describes.
func (m *markMask) in(x, y int) bool {
	return m != nil && x >= m.x0 && x < m.x1 && y >= m.y0 && y < m.y1
}

// newMask allocates a mask over the window [x0,x1) x [y0,y1).
func newMask(w, x0, y0, x1, y1 int) *markMask {
	return &markMask{w: w, x0: x0, y0: y0, x1: x1, y1: y1, px: make([]bool, w*w)}
}

// splitFromBand is where the mask splits dark from light, given the window: the
// two lowest populated levels BELOW THE PAD are the mark's own pair.
//
// It reads no colour constant. The pad's light level is measured per image
// (padLight), and the two darkest populated levels below it are the mark's dark
// and light modules whatever they are painted -- 0/255 for the black-on-white
// symbol, 120/255 for dim grey eyes, 154/176 for the retired grey palette. The
// pad itself is excluded by construction so it can never drag the split up and
// collapse the mark into one run.
func (g *gray) splitFromBand(x0, y0, x1, y1 int) float64 {
	var hist [256]int
	n := 0
	for y := y0; y < y1; y++ {
		row := g.px[y*g.w:]
		for x := x0; x < x1; x++ {
			v := int(row[x] + 0.5)
			if v < 0 {
				v = 0
			}
			if v > 255 {
				v = 255
			}
			hist[v]++
			n++
		}
	}
	light := g.padLight()
	if n == 0 {
		return math.Max(0, light-seedGap/2)
	}
	floor := n / 100
	if floor < 1 {
		floor = 1
	}
	top := int(math.Max(0, light-seedGap))
	darkLevel, lightLevel := -1, -1
	for i := 0; i <= top; i++ {
		if hist[i] < floor {
			continue
		}
		if darkLevel < 0 {
			darkLevel = i
			continue
		}
		lightLevel = i
		break
	}
	switch {
	case darkLevel < 0:
		return math.Max(0, light-seedGap/2)
	case lightLevel < 0:
		return float64(darkLevel) + minMarkContrast/2
	}
	return float64(darkLevel+lightLevel) / 2.0
}

func (g *gray) relaxWindow(x0, y0, x1, y1 int, mid float64) (*markMask, float64, bool) {
	// pad the box (quiet zone) and clip
	const pad = 2
	x0, y0 = x0-pad, y0-pad
	x1, y1 = x1+pad, y1+pad
	if x0 < 0 {
		x0 = 0
	}
	if y0 < 0 {
		y0 = 0
	}
	if x1 > g.w {
		x1 = g.w
	}
	if y1 > g.h {
		y1 = g.h
	}
	dark, light, mid, ok := g.windowStats(x0, y0, x1, y1)
	if !ok || light-dark < minMarkContrast {
		return nil, 0, false
	}
	// The threshold must separate the finder's TWO DARK-ish levels from its light
	// ring. Measured on the fixture window: peaks 154:288 176:288 255:915 -- i.e.
	// 154 (dark border/core), 176 (light ring) and 255 (pad). Otsu puts the split
	// between {154,175} and {255} (209.6), and (dark+light)/2 = (165+254)/2 is just
	// as bad, because both means are dragged by the pad. In either case the 176
	// ring joins the DARK side and the mark collapses to one run ([22 490]).
	//
	// So the two LOWEST populated levels are the mark's own pair and the split
	// goes between THEM: 165 here, which is what the page paints (154 dark vs 176
	// light) and what the band seed was tuned to. Anything at or above the pad
	// level is explicitly excluded from being the "light" side of the mark.
	// The threshold splits the marker's own TWO BAND LEVELS, never a mean. Measured
	// on the fixture window: peaks 154:288 176:288 255:915 -- 154 (dark border and
	// core), 176 (light ring), 255 (pad). A mean or an Otsu split lands at ~210
	// because the pad dominates the population, so the 176 ring joins the DARK
	// side and the whole mark collapses to one run (measured "runs -> [22 490]",
	// and with the pad in the window even "runs -> [4 508]", 2 runs).
	//
	// The split therefore comes from the seed band's own populated levels: the
	// darkest level inside [seedMin..seedMax] is the core, the next one above it
	// is the ring, and pad/pixel noise outside the band cannot move it.
	if dbgMeasure {
		var hist [256]int
		for y := y0; y < y1; y++ {
			row := g.px[y*g.w:]
			for x := x0; x < x1; x++ {
				v := int(row[x] + 0.5)
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				hist[v]++
			}
		}
		peaks := ""
		for i := 0; i < 256; i++ {
			if hist[i] > 36 {
				peaks += fmt.Sprintf(" %d:%d", i, hist[i])
			}
		}
		fmt.Fprintf(os.Stderr, "      window %d,%d..%d,%d dark=%.1f light=%.1f mid=%.1f peaks%s\n",
			x0, y0, x1, y1, dark, light, mid, peaks)
	}

	// The mask is returned as a SMALL RECTANGLE, not a full-frame plane.

	//
	// This is the bug that hid for the whole session: the mask was allocated at
	// the IMAGE width but only filled inside the window, and runsAt indexes it
	// with `y*g.w+x` (full-frame indices). Those two index spaces only agree when
	// the window starts at x=0, so every scan read out-of-bounds (or the wrong)
	// pixels past the window's first row -- measured "runs at 328,135 -> [4 508]",
	// i.e. a 4 px run then 508 px of nothing, on a row that crosses a correct
	// finder and must read 4,4,12,4,4.
	//
	// So the mask keeps full-frame indexing (so runsAt, crossCheck and
	// centreOf need no changes) and carries its own bounds; readMask is the only
	// way to look at it, and it answers "light" outside the window.
	mk := &markMask{w: g.w, x0: x0, y0: y0, x1: x1, y1: y1, px: make([]bool, g.w*g.w)}
	for y := y0; y < y1; y++ {
		row := g.px[y*g.w:]
		for x := x0; x < x1; x++ {
			mk.px[y*g.w+x] = row[x] <= mid
		}
	}
	if dbgMeasure {
		var hist [256]int
		for y := y0; y < y1; y++ {
			row := g.px[y*g.w:]
			for x := x0; x < x1; x++ {
				v := int(row[x] + 0.5)
				if v < 0 {
					v = 0
				}
				if v > 255 {
					v = 255
				}
				hist[v]++
			}
		}
		peaks := ""
		for i := 0; i < 256; i++ {
			if hist[i] > 36 {
				peaks += fmt.Sprintf(" %d:%d", i, hist[i])
			}
		}
		fmt.Fprintf(os.Stderr, "      window %d,%d..%d,%d dark=%.1f light=%.1f mid=%.1f peaks%s\n",
			x0, y0, x1, y1, dark, light, mid, peaks)
	}
	return mk, mid, true
}
// blobs groups the seed band into connected components: each is a MARK.
func (g *gray) blobs(seed []bool) []blob {
	seen := make([]bool, len(seed))
	var out []blob
	stack := make([]int, 0, 4096)
	for start := 0; start < len(seed); start++ {
		if !seed[start] || seen[start] {
			continue
		}
		seen[start] = true
		stack = append(stack[:0], start)
		b := blob{x0: g.w, y0: g.h, x1: -1, y1: -1}
		for len(stack) > 0 {
			p := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			x, y := p%g.w, p/g.w
			if x < b.x0 {
				b.x0 = x
			}
			if x > b.x1 {
				b.x1 = x
			}
			if y < b.y0 {
				b.y0 = y
			}
			if y > b.y1 {
				b.y1 = y
			}
			b.n++
			for dy := -1; dy <= 1; dy++ {
				for dx := -1; dx <= 1; dx++ {
					nx, ny := x+dx, y+dy
					if nx < 0 || nx >= g.w || ny < 0 || ny >= g.h {
						continue
					}
					q := ny*g.w + nx
					if seed[q] && !seen[q] {
						seen[q] = true
						stack = append(stack, q)
					}
				}
			}
		}
		out = append(out, b)
	}
	return out
}

// candidates finds every finder pattern in the image.
// findInBlobFFT is the NOISE-TOLERANT finder locator: it correlates each row
// and column of the blob against the 1:1:3:1:1 kernel via the discrete FFT
// (see correlateFFT) and takes the spectral peak as the finder's centre line.
//
// WHY THIS EXISTS: findInBlob's per-scanline run-length matcher demands
// exactly five runs in a 1:1:3:1:1 ratio; a grid rule crossing the finder
// SPLITS a run and the match fails on a mark the same program painted
// (measured: 0/3 marks with the pad's rules on). The FFT works in the SIGNAL
// domain — the finder is a periodic square wave (dark ring, light ring, dark
// core) whose fundamental sits at 1/(2*module) with a characteristic harmonic
// signature, while grid rules and ink are broadband noise at other
// frequencies. A rule crossing the mark is rejected by the spectrum instead of
// breaking the run count.
//
// The blob's module estimate comes from the blob's own width/height (a finder
// is 7 modules wide + 2 separator = 9 cells), then the peak position supplies
// the centre. crossCheck (the geometric measure) still confirms it.
func (g *gray) findInBlobFFT(b blob, seed []bool) (*finder, bool) {
	w := b.x1 - b.x0 + 1
	h := b.y1 - b.y0 + 1
	if w < 5 || h < 5 {
		return nil, false
	}
	// A finder's outer bound is 9 cells (7-module ring + 1-cell separator each
	// side). The blob is the seed band's dark modules, so its extent is the
	// ring's 7 cells; module ~= extent/7.
	module := float64(w) / 7.0
	if float64(h)/7.0 > module {
		module = float64(h) / 7.0
	}
	if module < 1.2 {
		module = 1.2
	}
	// Correlate the blob's middle row and middle column against the kernel.
	mask := make([]bool, g.w*g.h)
	for y := b.y0; y <= b.y1; y++ {
		for x := b.x0; x <= b.x1; x++ {
			mask[y*g.w+x] = seed[y*g.w+x]
		}
	}
	// Horizontal scan at the blob's mid row: the peak x is the finder's centre x.
	midY := (b.y0 + b.y1) / 2
	sig := make([]float64, w)
	for i := 0; i < w; i++ {
		if mask[midY*g.w+b.x0+i] {
			sig[i] = 1
		} else {
			sig[i] = -1
		}
	}
	k := finderKernel1D(module)
	corrX := correlateFFT(sig, k)
	bestX, bestXScore := 0, math.Inf(-1)
	for i, c := range corrX {
		if c > bestXScore {
			bestXScore = c
			bestX = i
		}
	}
	// Vertical scan at the blob's mid column.
	midX := (b.x0 + b.x1) / 2
	sigY := make([]float64, h)
	for i := 0; i < h; i++ {
		if mask[(b.y0+i)*g.w+midX] {
			sigY[i] = 1
		} else {
			sigY[i] = -1
		}
	}
	corrY := correlateFFT(sigY, k)
	bestY, bestYScore := 0, math.Inf(-1)
	for i, c := range corrY {
		if c > bestYScore {
			bestYScore = c
			bestY = i
		}
	}
	// A real finder shows the fundamental on both axes: the peak score is
	// ~kernel length (all 7 modules aligned). A grid rule or ink edge gives a
	// much lower score. Require a score of at least half the kernel's energy.
	want := 0.0
	for _, v := range k {
		want += v * v
	}
	want = math.Sqrt(want) * 0.5
	if bestXScore < want || bestYScore < want {
		return nil, false
	}
	cx := float64(b.x0 + bestX)
	cy := float64(b.y0 + bestY)
	_, _, mid, ok := g.windowStats(b.x0, b.y0, b.x1+1, b.y1+1)
	if !ok {
		return nil, false
	}
	f := &finder{X: cx, Y: cy, Module: module, Mid: mid}
	return f, true
}

func (g *gray) candidates(cfg probeCfg) []*finder {
	dbgMeasure = os.Getenv("PADBBOX_DEBUG") != ""
	seed := g.seedMask()
	scratch := make([]bool, len(seed))
	var found []*finder
	maxArea := (g.w * g.h) / 8
	for _, b := range g.blobs(seed) {
		area := (b.x1 - b.x0 + 1) * (b.y1 - b.y0 + 1)
		if dbgMeasure {
			fmt.Fprintf(os.Stderr, "blob %d,%d..%d,%d n=%d area=%d\n", b.x0, b.y0, b.x1, b.y1, b.n, area)
		}
		if area > maxArea {
			continue
		}
		f, ok := g.findInBlob(b, scratch)
		if !ok {
			// FFT FALLBACK (the noise-tolerant path). findInBlob's per-scanline
			// run-length matcher demands exactly five runs in a 1:1:3:1:1
			// ratio; a grid rule crossing the finder SPLITS a run and the
			// match fails on a mark the same program painted (measured: 0/3
			// with the pad's rules on). The FFT matched filter works in the
			// signal domain — the finder is a periodic square wave whose
			// fundamental sits at 1/(2*module), while grid rules and ink are
			// broadband noise — so a rule crossing the mark is rejected by the
			// spectrum instead of breaking the run count. See fft_detect.go.
			f, ok = g.findInBlobFFT(b, seed)
			if !ok {
				continue
			}
		}
		merged := false
		for _, e := range found {
			if math.Abs(e.X-f.X) <= mergeDx*f.Module && math.Abs(e.Y-f.Y) <= mergeDx*f.Module {
				e.merge(f.X, f.Y, f.Module, f.Mid)
				merged = true
				break
			}
		}
		if !merged {
			found = append(found, f)
		}
	}
	sort.Slice(found, func(i, j int) bool { return found[i].Hits > found[j].Hits })
	return found
}

// assignCorners names TL/TR/BL from three candidates, refusing anything that is
// not the QR triad: agreeing module sizes and an isosceles right triangle.
func assignCorners(cands []*finder) (map[string]*finder, string) {
	if len(cands) < 3 {
		return nil, fmt.Sprintf("only %d finder candidate(s)", len(cands))
	}
	s := append([]*finder(nil), cands...)
	if len(s) > 6 {
		s = s[:6]
	}
	var best []*finder
	bestScore := math.Inf(1)
	var bestD [3]float64
	for i := 0; i < len(s); i++ {
		for j := i + 1; j < len(s); j++ {
			for k := j + 1; k < len(s); k++ {
				trio := []*finder{s[i], s[j], s[k]}
				minM, maxM := trio[0].Module, trio[0].Module
				for _, t := range trio {
					minM = math.Min(minM, t.Module)
					maxM = math.Max(maxM, t.Module)
				}
				if maxM > 1.4*minM {
					continue
				}
				d := [3]float64{dist(trio[0], trio[1]), dist(trio[1], trio[2]), dist(trio[0], trio[2])}
				sort.Float64s(d[:])
				if d[0] <= 0 {
					continue
				}
				score := math.Abs(d[0]-d[1])/d[0] + math.Abs(d[2]-d[0]*math.Sqrt2)/d[2]
				if score < bestScore {
					bestScore, best, bestD = score, trio, d
				}
			}
		}
	}
	if best == nil {
		return nil, "no three candidates with agreeing module sizes"
	}
	if math.Abs(bestD[0]-bestD[1]) > 0.05*bestD[0] ||
		math.Abs(bestD[2]-bestD[0]*math.Sqrt2) > 0.05*bestD[2] {
		return nil, fmt.Sprintf("triangle is not a square corner (sides %.1f %.1f %.1f)",
			bestD[0], bestD[1], bestD[2])
	}
	byX := append([]*finder(nil), best...)
	sort.Slice(byX, func(i, j int) bool { return byX[i].X < byX[j].X })
	byY := append([]*finder(nil), best...)
	sort.Slice(byY, func(i, j int) bool { return byY[i].Y < byY[j].Y })
	var tl *finder
	for _, a := range byX[:2] {
		for _, b := range byY[:2] {
			if a == b {
				tl = a
			}
		}
	}
	if tl == nil {
		return nil, "cannot identify the top-left corner"
	}
	var others []*finder
	for _, c := range best {
		if c != tl {
			others = append(others, c)
		}
	}
	tr, bl := others[0], others[1]
	if tr.X < bl.X {
		tr, bl = bl, tr
	}
	return map[string]*finder{"TL": tl, "TR": tr, "BL": bl}, ""
}

func dist(a, b *finder) float64 { return math.Hypot(a.X-b.X, a.Y-b.Y) }

// rect is the canvas rectangle in screen pixels.
type rect struct{ X, Y, W, H float64 }

// canvasRect measures w from TL..TR and h from TL..BL INDEPENDENTLY and refuses
// unless they agree. page.html makes the canvas an exact square (aspect-ratio
// 1/1 with equal max-width and max-height), so a disagreement -- or a spread of
// module sizes -- is a distorted or clipped reading, and produces NO rect: a
// wrong answer must not be able to look like a right one.
//
// THE ARITHMETIC, STATED SO IT IS NOT FITTED. The three finders sit at grid cells
// (1,1), (119,1) and (1,119) -- INSET ONE MODULE from the canvas corner, so a
// finder's centre is finderCentre (= eyeOffset + 3.5 = 4.5) modules in from the
// edges it answers to. The canvas is nCells (128) modules square while the eyes
// span qrSize (125) of them, the remaining modules being the quiet zone. Therefore:
//
//	module = (TR.X - TL.X) / qrSpan        (118 modules between the centres)
//	X      = TL.X - finderCentre*module
//	W      = nCells * module
//
// The span in px is checked against the BL row before anything else, and W is
// taken from nCells, not from the measured span: a rect fitted to the span would
// come out 125 modules wide and silently report the EYE BBOX as the canvas, which
// on this geometry differs by one module on every side.
func canvasRect(named map[string]*finder, squareTol float64) (*rect, map[string]any) {
	var missing []string
	for _, k := range []string{"TL", "TR", "BL"} {
		if named[k] == nil {
			missing = append(missing, k)
		}
	}
	if len(missing) > 0 {
		return nil, map[string]any{"reason": "missing marks", "detail": map[string]any{"missing": missing}}
	}
	tl, tr, bl := named["TL"], named["TR"], named["BL"]
	minM := math.Min(tl.Module, math.Min(tr.Module, bl.Module))
	maxM := math.Max(tl.Module, math.Max(tr.Module, bl.Module))
	module := (tl.Module + tr.Module + bl.Module) / 3.0
	if maxM-minM > 0.25*module {
		return nil, map[string]any{
			"reason": "module sizes disagree",
			"detail": map[string]any{"TL": round3(tl.Module), "TR": round3(tr.Module), "BL": round3(bl.Module)},
		}
	}
	spanW, spanH := tr.X-tl.X, bl.Y-tl.Y
	if math.Abs(spanW-spanH) > squareTol {
		return nil, map[string]any{
			"reason": "reading is not square (distorted image)",
			"detail": map[string]any{
				"w": round3(spanW), "h": round3(spanH), "delta": round3(spanW - spanH), "square_tol": squareTol,
			},
		}
	}
	w := float64(nCells) * module
	h := w
	// THE SPAN IS THE ACCURATE MODULE, THE CORE RUN IS NOT.
	//
	// Each finder reports Module = coreRun/3, i.e. a 3-module run measured to the
	// nearest pixel -- on a 5.2 px module that is ±0.17 px of quantisation, and it
	// amplified to 17 px of error on the canvas side (measured on a live shot:
	// core-run module 5.333 -> side 683, while TR-TL spanned 614 px = 5.203/module
	// -> side 666, which is what the white surface actually measures).
	//
	// The span between two finder centres averages that error over qrSpan (118)
	// modules instead of 3, and it is the number the rect is anchored on anyway.
	// So the span decides the scale, and the core-run modules above are kept as a
	// CONSISTENCY CHECK only.
	spanModule := (spanW + spanH) / 2.0 / float64(qrSpan)
	if spanModule > 0 {
		w = float64(nCells) * spanModule
		h = w
		module = spanModule
	}
	return &rect{X: tl.X - finderCentre*module, Y: tl.Y - finderCentre*module, W: w, H: h}, nil
}

func round3(v float64) float64 { return math.Round(v*1000) / 1000 }

// probe prints what the decoder saw: the seed band population, every candidate
// with its hit count and local threshold. It is the Go replacement for poking
// at pixels with python.
func probe(g *gray, cands []*finder, out *os.File) {
	seed := g.seedMask()
	n := 0
	for _, b := range seed {
		if b {
			n++
		}
	}
	lo, hi := g.seedBand()
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	_ = enc.Encode(map[string]any{
		"image_wh":      [2]int{g.w, g.h},
		"seed_band":     [2]int{int(math.Round(lo)), int(math.Round(hi))},
		"pad_light":     round3(g.padLight()),
		"page_dark":     round3(g.pageDark()),
		"seed_pixels":   n,
		"seed_fraction": round3(float64(n) / float64(g.w*g.h)),
		"candidates":    len(cands),
		"list":          cands,
	})
}
