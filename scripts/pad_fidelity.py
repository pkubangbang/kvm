#!/usr/bin/env python3
"""Shape fidelity of the drawn pad trace against a teacher trace.

Usage: pad_fidelity.py <teacher.json> [<drawn.json>]

Cell-overlap is the wrong score for a 1-cell-wide stem and a smooth curve: a
half-cell placement difference makes a stroke miss a whole row of cells. This
measures the shape instead — every teacher ink sample (1 px resampled) is scored
against the drawn ink with a distance tolerance, after normalising both traces
to their own first point so placement does not enter the score.

Prints:
  * recall    — share of the teacher's ink that lies within TOL of the drawn ink
  * precision — share of the drawn ink that lies within TOL of the teacher's ink
  * worst-missed region — the teacher sample farthest from any drawn ink
"""
import json
import math
import sys
from trace_read import read_trace, points_of
import os

TOL = 0.75   # cells (4.5 px at 6 px/cell)


def load(path, fallback_pad=True):
    if path:
        with open(path) as fh:
            return json.load(fh)
    return read_trace(os.environ.get("KVM_PAD_OUT"))


def ink(trace):
    """Every recorded polyline resampled at ~1 px, normalised to its first point."""
    strokes = []
    for st in trace:
        strokes.append(points_of(st))
    if not strokes or not strokes[0]:
        return []
    ox, oy = strokes[0][0]
    out = []
    for pts in strokes:
        for i in range(1, len(pts)):
            a, b = pts[i - 1], pts[i]
            d = math.hypot(b[0] - a[0], b[1] - a[1])
            n = max(1, int(d * 6))
            for k in range(n + 1):
                t = k / n
                out.append((a[0] + (b[0] - a[0]) * t - ox,
                            a[1] + (b[1] - a[1]) * t - oy))
    return out


def nearest(p, cloud):
    best = 1e9
    for q in cloud:
        d = math.hypot(p[0] - q[0], p[1] - q[1])
        if d < best:
            best = d
            if best < TOL:
                return best
    return best


def main():
    teacher = ink(load(sys.argv[1]))
    drawn = ink(load(sys.argv[2] if len(sys.argv) > 2 else None))
    if not teacher or not drawn:
        print("empty trace: teacher %d samples, drawn %d samples"
              % (len(teacher), len(drawn)))
        return 1
    step = max(1, len(teacher) // 400)
    tsub = teacher[::step]
    dsub = drawn[::step]
    hit = 0
    worst, worst_pt = 0.0, None
    for p in tsub:
        d = nearest(p, dsub)
        if d <= TOL:
            hit += 1
        elif d > worst:
            worst, worst_pt = d, p
    recall = hit / len(tsub)
    dhit = sum(1 for p in dsub if nearest(p, tsub) <= TOL)
    precision = dhit / len(dsub)
    print("teacher samples %d, drawn samples %d (scored %d each)"
          % (len(teacher), len(drawn), len(tsub)))
    print("recall    %.1f %%  (teacher ink reproduced within %.1f cell)" % (recall * 100, TOL))
    print("precision %.1f %%  (drawn ink that lies on the teacher)"
          % (precision * 100))
    if worst_pt:
        print("worst miss %.1f cells (%.0f px) at teacher-relative cell (%.0f, %.0f)"
              % (worst, worst * 6, worst_pt[0], worst_pt[1]))
    return 0 if recall >= 0.9 else 1


if __name__ == "__main__":
    sys.exit(main())
