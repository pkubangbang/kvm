package mouse

import (
	"math"
)

// Path is what the loop follows: a list of absolute points.
type Path [][2]int

// LinePath is a straight leg from (x0,y0) to (x1,y1).
func LinePath(x0, y0, x1, y1 int) Path { return Path{{x0, y0}, {x1, y1}} }

// CirclePath is a closed circle drawn as n small arcs, starting at angle 0.
func CirclePath(cx, cy, r, n int) Path {
	if n < 8 {
		n = 8
	}
	pts := make(Path, 0, n+1)
	for k := 0; k <= n; k++ {
		th := 2 * math.Pi * float64(k) / float64(n)
		pts = append(pts, [2]int{
			cx + int(math.Round(float64(r)*math.Cos(th))),
			cy + int(math.Round(float64(r)*math.Sin(th))),
		})
	}
	return pts
}

func sign(v int) int {
	if v < 0 {
		return -1
	}
	return 1
}

func absOf(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
