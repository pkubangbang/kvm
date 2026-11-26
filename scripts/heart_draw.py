"""Emit one CLOSED heart stroke at the pad centre, lobes up, tip DOWN.

Every point is computed in INK space (cell units on the 128 grid) and converted
to SCREEN pixels with the recorded calibration in scripts/pad_calib.json:

    screen_x = left + cx * px_per_cell
    screen_y = top  + cy * px_per_cell_y

The two axes use SEPARATE divisors because the canvas box is not guaranteed
square -- README "Coordinates".

The heart is the classic parametric curve, y flipped so the lobes point up and
the cusp points down:

    x = 16 sin^3 t
    y = 13 cos t - 5 cos 2t - 2 cos 3t - cos 4t     (y grows downward on screen)

It is closed by construction (t: 0 -> 2pi), so no seam repair is needed.

Output is the kvm argument list, one `drag DX DY` per step, so the whole heart
is ONE held span: down, drag*, up. Prints the leg vectors so their sum can be
asserted to be zero.
"""
import json
import math
import sys

CELL = 128.0
CALIB = "scripts/pad_calib.json"


def load_calib(path=CALIB):
    with open(path) as f:
        c = json.load(f)
    # The old key names are the authority; fail loudly rather than guess.
    for k in ("left", "top", "px_per_cell"):
        if k not in c:
            raise SystemExit("pad_calib.json missing %r" % k)
    return c


def heart_points(n=96, scale_cells=22.0):
    """Closed heart in CELL units, centred on the origin, lobes UP, tip DOWN.

    SCREEN y GROWS DOWNWARD. The parametric heart

        x = 16 sin^3 t
        y = 13 cos t - 5 cos 2t - 2 cos 3t - cos 4t

    has its CUSP at LOW y in maths space, i.e. at the top once drawn on screen,
    which is an upside-down heart. Measured the hard way: the first draw landed
    with one x-cluster at the top and one at the bottom in the pad's own trace.
    So the y term is negated here, putting the lobes at low y (top of the
    screen) and the cusp at high y (bottom).
    """
    pts = []
    for i in range(n + 1):                     # inclusive: last == first
        t = 2.0 * math.pi * i / n
        x = 16.0 * math.sin(t) ** 3
        y = (13.0 * math.cos(t) - 5.0 * math.cos(2 * t)
             - 2.0 * math.cos(3 * t) - math.cos(4 * t))
        y = -y                                # screen y grows downward
        # 16 and ~17 are the natural extents of the curve; normalise by 17 so
        # scale_cells means "half-width in cells".
        pts.append((x * scale_cells / 17.0, y * scale_cells / 17.0))
    return pts


def to_screen(pts, calib):
    left, top = calib["left"], calib["top"]
    px, py = calib["px_per_cell"], calib.get("px_per_cell_y", calib["px_per_cell"])
    return [(left + x * px, top + y * py) for (x, y) in pts]


def plan(calib, centre_cells=(64.0, 64.0), scale_cells=22.0, n=96):
    """Place the heart's own centre at `centre_cells` on the pad."""
    pts = heart_points(n, scale_cells)
    xs = [p[0] for p in pts]
    ys = [p[1] for p in pts]
    cx = (min(xs) + max(xs)) / 2.0
    cy = (min(ys) + max(ys)) / 2.0
    pts = [(x - cx + centre_cells[0], y - cy + centre_cells[1]) for (x, y) in pts]
    screen = to_screen(pts, calib)
    return pts, screen


def legs(screen):
    """Integer relative reports, accumulated so rounding cannot drift."""
    out = []
    fx, fy = screen[0]
    ix, iy = int(round(fx)), int(round(fy))
    for (sx, sy) in screen[1:]:
        tx, ty = int(round(sx)), int(round(sy))
        out.append((tx - ix, ty - iy))
        ix, iy = tx, ty
    return out


def main(argv):
    calib = load_calib()
    n = int(argv[0]) if argv else 96
    pts, screen = plan(calib, n=n)
    lg = legs(screen)
    dx = sum(d[0] for d in lg)
    dy = sum(d[1] for d in lg)
    start = screen[0]
    print("anchor      :", (calib["left"], calib["top"]))
    print("px/cell     :", (calib["px_per_cell"], calib.get("px_per_cell_y")))
    print("start screen:", (int(round(start[0])), int(round(start[1]))))
    print("legs        :", len(lg))
    print("closure     :", (dx, dy), "(must be 0,0)")
    print("ink extents :", (min(p[0] for p in pts), max(p[0] for p in pts)),
          (min(p[1] for p in pts), max(p[1] for p in pts)), "cells")
    print("tip         : y = %.1f cells (larger y = lower on screen)" % max(p[1] for p in pts))
    print("args        :")
    print(" ".join("drag %d %d" % d for d in lg))
    return 0


if __name__ == "__main__":
    sys.exit(main(sys.argv[1:]))
