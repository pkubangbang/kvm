package main

// eyes_geom.go -- the three QR "eyes" the pad paints, and where they sit.
//
// THE USER'S INSTRUCTION (verbatim): "only the eyes should be retained and
// should be dimmed to grey". The page used to paint the WHOLE version-27
// symbol (125x125 modules, black on white) from qr_table.go. It no longer does.
// What survives is the calibration triad the rest of the toolchain reads --
// three 7x7 finder patterns -- painted in a DIM GREY so they are visible to a
// decoder but do not dominate the canvas a human draws on.
//
// WHY THE GEOMETRY IS NOT INVENTED HERE. The three finders are modules 0..6 of
// the symbol, and the symbol is painted flush at the canvas origin, so:
//
//	TL finder occupies grid cells (0..6, 0..6)
//	TR finder occupies grid cells (118..124, 0..6)
//	BL finder occupies grid cells (0..6, 118..124)
//
// with a light separator at module 7. That is not a choice; it is where a
// version-27 QR puts its finders, and it is where scripts/pad_bbox.py and
// cmd/pad-bbox look. The 128-cell pad minus the 125-module symbol leaves 3
// cells of white quiet zone on the RIGHT and BOTTOM only -- the user's
// "让右侧还有下方留出白边".
const (
	// eyeSep is the light separator ring around a 7x7 finder, in modules.
	eyeSep = 1
	// eyeBox is the painted extent of one eye: 7 modules plus the separator on
	// both sides. It is NOT a grid cell count of the pad -- it is where to stop
	// painting light pixels.
	eyeBox = 7 + 2*eyeSep
	// eyesJS is the literal handed to the page as window.PAD_EYES. The page
	// needs the origins and the module pitch, nothing else: the 7x7 shape is
	// fixed by the QR spec and painted by finderDark below (mirrored in JS).
	eyesJS = "{" +
		"module: 7," +
		"sep: 1," +
		"size: 125," +
		"origins: [[0,0],[118,0],[0,118]]," +
		"grey: '#9a9a9a'" +
		"}"
)

// finderDark reports whether (x,y) of a 7-module finder is a dark module:
// a solid 3x3 core inside a one-module light ring inside a one-module dark
// border. Read across the centre it is dark, light, dark, light, dark in the
// 1:1:3:1:1 ratio: the same runs TestFinderRatio in cmd/pad-bbox checks.
func finderDark(y, x int) bool {
	d := absi(x - 3)
	if a := absi(y - 3); a > d {
		d = a
	}
	return d == 3 || d <= 1
}

func absi(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
