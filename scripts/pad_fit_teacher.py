#!/usr/bin/env python3
"""Fit the teacher's glyph parts and emit a drawable plan in pad cells.

Usage: pad_fit_teacher.py <teacher.json> [--out plan.json]

For each stroke of the teacher trace it reports the fitted geometry:
  * a straight stroke -> endpoints and the least-squares line,
  * a loop stroke     -> an ellipse fitted to its points (algebraic fit),
and prints the plan as a list of strokes of (x, y) cell points, resampled at
~1 px, which pad_draw_plan.py turns into kvm drag reports.

The point is that every number the draw uses comes from the teacher's own
trace, never from an eyeballed coordinate.
"""
import json
import math
import sys
from trace_read import points_of


def points(st):
    return points_of(st)


def fit_line(pts):
    n = len(pts)
    mx = sum(p[0] for p in pts) / n
    my = sum(p[1] for p in pts) / n
    sxx = sum((p[0] - mx) ** 2 for p in pts)
    sxy = sum((p[0] - mx) * (p[1] - my) for p in pts)
    syy = sum((p[1] - my) ** 2 for p in pts)
    # principal axis
    theta = 0.5 * math.atan2(2 * sxy, sxx - syy)
    ux, uy = math.cos(theta), math.sin(theta)
    ts = [(p[0] - mx) * ux + (p[1] - my) * uy for p in pts]
    return (mx, my, ux, uy, min(ts), max(ts), math.hypot(sxx, syy))


def fit_ellipse(pts):
    """Algebraic ellipse fit (Halir-Flusser style, simplified least squares)."""
    # centre = centroid is a good start; refine with a linear solve on the
    # offset form A u^2 + B uv + C v^2 = 1 in centred coordinates.
    n = len(pts)
    mx = sum(p[0] for p in pts) / n
    my = sum(p[1] for p in pts) / n
    rows = []
    rhs = []
    for x, y in pts:
        u, v = x - mx, y - my
        rows.append((u * u, u * v, v * v))
        rhs.append(1.0)
    # normal equations (3x3)
    M = [[sum(r[i] * r[j] for r in rows) for j in range(3)] for i in range(3)]
    b = [sum(r[i] * rhs[k] for k, r in enumerate(rows)) for i in range(3)]
    # gaussian elimination
    for i in range(3):
        p = max(range(i, 3), key=lambda k: abs(M[k][i]))
        M[i], M[p] = M[p], M[i]
        b[i], b[p] = b[p], b[i]
        for k in range(i + 1, 3):
            f = M[k][i] / M[i][i]
            for j in range(i, 3):
                M[k][j] -= f * M[i][j]
            b[k] -= f * b[i]
    sol = [0.0] * 3
    for i in reversed(range(3)):
        sol[i] = (b[i] - sum(M[i][j] * sol[j] for j in range(i + 1, 3))) / M[i][i]
    A, B, C = sol
    # convert to axis lengths / rotation
    theta = 0.5 * math.atan2(B, A - C)
    # radii from the quadratic form in rotated coordinates
    ct, st = math.cos(theta), math.sin(theta)
    Ap = A * ct * ct + B * ct * st + C * st * st
    Cp = A * st * st - B * ct * st + C * ct * ct
    ra = 1.0 / math.sqrt(abs(Ap))
    rb = 1.0 / math.sqrt(abs(Cp))
    return mx, my, ra, rb, math.degrees(theta)


def main():
    with open(sys.argv[1]) as fh:
        trace = json.load(fh)
    for i, st in enumerate(trace):
        pts = points(st)
        chord = math.hypot(pts[-1][0] - pts[0][0], pts[-1][1] - pts[0][1])
        path = sum(math.hypot(pts[k][0] - pts[k - 1][0], pts[k][1] - pts[k - 1][1])
                   for k in range(1, len(pts)))
        print("stroke %d: %d pts  path %.1f cells  chord %.1f cells  ratio %.2f"
              % (i, len(pts), path, chord, path / max(chord, 1e-6)))
        if path / max(chord, 1e-6) < 1.3:
            mx, my, ux, uy, t0, t1, _ = fit_line(pts)
            print("   line: centre (%.1f,%.1f) dir (%.2f,%.2f) from %.1f to %.1f cells"
                  " -> endpoints (%.1f,%.1f) -> (%.1f,%.1f)"
                  % (mx, my, ux, uy, t0, t1,
                     mx + ux * t0, my + uy * t0, mx + ux * t1, my + uy * t1))
        else:
            mx, my, ra, rb, ang = fit_ellipse(pts)
            print("   ellipse: centre (%.1f,%.1f) radii (%.1f,%.1f) cells rot %.1f deg"
                  % (mx, my, ra, rb, ang))
            print("   start (%.1f,%.1f) end (%.1f,%.1f)"
                  % (pts[0][0], pts[0][1], pts[-1][0], pts[-1][1]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
