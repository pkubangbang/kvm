package main

// fft_detect.go -- locate the three QR finder patterns by DISCRETE FFT
// matched-filter correlation, replacing the fragile per-scanline run-length
// matcher.
//
// WHY FFT (the user's correction): the run-length matcher walks each scanline
// and demands exactly five runs in a 1:1:3:1:1 ratio. A grid rule crossing a
// finder SPLITS a run, so the walk returns 6..7 runs and rejects a finder the
// same program painted (measured: 0/3 marks with the pad's grid rules on).
// Every "tolerance" tweak on that path is fighting the wrong representation.
//
// The FFT path works in the SIGNAL domain, where the pattern is a PERIOD:
//
//   - a finder crossed by ANY scanline through its core is a square wave of
//     period 2*module (dark ring, light ring, dark core) with a 3-module core
//     at its centre. Its DFT has a strong fundamental at f = 1/(2*module) and
//     a characteristic harmonic signature (the 1:1:3:1:1 profile's harmonics);
//   - a grid rule is broadband, low-energy noise at OTHER frequencies, and a
//     stroke is a thin edge: neither produces the fundamental+harmonics of the
//     finder. Noise is rejected by the spectrum, not by pixel-level run
//     counting.
//
// The detector therefore:
//  1. Binarizes the seed band once (dark marker modules vs the light pad).
//  2. Projects to row and column "dark counts" (how dark per row/col inside a
//     sliding window) — or, more directly, correlates a 1-D 1:1:3:1:1 kernel
//     against every row and every column via DFT convolution;
//  3. Takes the per-line correlation scores, sums the top ones per candidate
//     centre, and keeps the three peaks that agree in module size — the three
//     finder centres;
//  4. Refines each peak's centre/module with the existing crossCheck geometry
//     (that part measures the finder and is not what failed).
//
// The DFT here is the naive O(n^2) one on short windows (n <= 1024): a pad
// line is 600-800 px, and there are ~1500 lines, so a full FFT library is not
// needed — a radix-2 FFT on zero-padded windows is enough and keeps the file
// dependency-free.

import (
	"math"
)

// finderKernel1D is the 1:1:3:1:1 profile sampled at `module` px per unit:
// a dark ring of 1 module, light ring of 1, dark core of 3, light ring of 1,
// dark ring of 1 — total 7 modules. The kernel is dark=+1, light=-1 so the
// correlation peaks where the pattern sits.
func finderKernel1D(module float64) []float64 {
	w := []float64{1, 1, 3, 1, 1}
	total := 0.0
	for _, u := range w {
		total += u
	}
	n := int(math.Round(total*module)) + 1
	k := make([]float64, n)
	i := 0
	for _, u := range w {
		un := int(math.Round(u * module))
		val := 1.0
		if i/(int(module)+1)%2 == 1 {
			val = -1.0
		}
		for j := 0; j < un && i < n; j++ {
			k[i] = val
			i++
		}
	}
	// alternate sign per run (dark=+1, light=-1, dark=+1 ...)
	sign := 1.0
	idx := 0
	for _, u := range w {
		un := int(math.Round(u * module))
		for j := 0; j < un && idx < n; j++ {
			k[idx] = sign
			idx++
		}
		sign = -sign
	}
	return k
}

// fft is an in-place iterative radix-2 Cooley-Tukey on a complex sequence
// interleaved as (re, im) pairs. n must be a power of two.
func fft(a []float64, inverse bool) {
	n := len(a) / 2
	// bit reversal
	for i, j := 0, 0; i < n; i++ {
		if i < j {
			a[2*i], a[2*j] = a[2*j], a[2*i]
			a[2*i+1], a[2*j+1] = a[2*j+1], a[2*i+1]
		}
		m := n >> 1
		for m >= 1 && j >= m {
			j -= m
			m >>= 1
		}
		j += m
	}
	for size := 2; size <= n; size <<= 1 {
		half := size >> 1
		step := math.Pi / float64(half)
		if inverse {
			step = -step
		}
		for start := 0; start < n; start += size {
			for k := 0; k < half; k++ {
				theta := step * float64(k)
				wr, wi := math.Cos(theta), math.Sin(theta)
				p := 2 * (start + k)
				q := 2 * (start + k + half)
				vr := a[q]*wr - a[q+1]*wi
				vi := a[q]*wi + a[q+1]*wr
				a[q], a[q+1] = a[p]-vr, a[p+1]-vi
				a[p], a[p+1] = a[p]+vr, a[p+1]+vi
			}
		}
	}
	if inverse {
		for i := range a {
			a[i] /= float64(n)
		}
	}
}

// correlateFFT returns the full circular correlation of signal s with kernel k
// (both real), via DFT. The result has len(s) entries: result[i] is how well k
// matches s starting at i (wrapping). Wrapping is fine here because the pad
// lines are long and the kernel is short.
func correlateFFT(s, k []float64) []float64 {
	n := 1
	for n < len(s)+len(k) {
		n <<= 1
	}
	a := make([]float64, 2*n)
	b := make([]float64, 2*n)
	for i := range s {
		a[2*i] = s[i]
	}
	for i := range k {
		b[2*i] = k[i]
	}
	fft(a, false)
	fft(b, false)
	for i := 0; i < n; i++ {
		ar, ai := a[2*i], a[2*i+1]
		br, bi := b[2*i], b[2*i+1]
		// A * conj(B): correlation, not convolution
		a[2*i] = ar*br + ai*bi
		a[2*i+1] = ai*br - ar*bi
	}
	fft(a, true)
	out := make([]float64, len(s))
	for i := range out {
		out[i] = a[2*i]
	}
	return out
}

// lineScore is a candidate centre on one axis: where the 1:1:3:1:1 kernel
// best matches a row or column signal.
type lineScore struct {
	pos   int
	score float64
}

// scanAxis correlates the kernel along one axis of the binarized image and
// returns the top peaks. The signal for row y is the dark mask (1 = dark
// module, -1 = light) sampled along x; the kernel is the 1:1:3:1:1 profile at
// the given module size. Peaks are local maxima above a fraction of the best.
func (g *gray) scanAxis(mask []bool, module float64, horizontal bool, topN int) []lineScore {
	k := finderKernel1D(module)
	length := g.w
	if horizontal {
		length = g.h
	}
	sig := make([]float64, length)
	var out []lineScore
	for line := 0; line < (g.h); line++ {
		if !horizontal {
			break
		}
	}
	lines := g.h
	if horizontal {
		lines = g.w
	}
	_ = lines
	// For each line, build the signal and correlate.
	lineCount := g.h
	axisLen := g.w
	if horizontal {
		lineCount = g.w
		axisLen = g.h
	}
	sig = make([]float64, axisLen)
	for li := 0; li < lineCount; li++ {
		for i := 0; i < axisLen; i++ {
			var v float64 = -1
			x, y := li, i
			if horizontal {
				x, y = i, li
			}
			if mask[y*g.w+x] {
				v = 1
			}
			sig[i] = v
		}
		corr := correlateFFT(sig, k)
		best := 0.0
		bestPos := 0
		for i, c := range corr {
			if c > best {
				best = c
				bestPos = i
			}
		}
		if best > 0 {
			out = append(out, lineScore{pos: bestPos, score: best})
		}
	}
	// Keep the strongest topN scores.
	for i := 0; i < len(out); i++ {
		for j := i + 1; j < len(out); j++ {
			if out[j].score > out[i].score {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	if len(out) > topN {
		out = out[:topN]
	}
	return out
}
