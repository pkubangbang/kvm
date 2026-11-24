#!/usr/bin/env python3
"""Replay/score a pad trace file the server wrote (one stroke per pen lift).

The pad now flushes every pen lift to a trace file as a JSON array of strokes,
each stroke an array of {ts,x,y,fx,fy}. This reads that file directly — the
agent never asks the page for its history — and can compare it with a teacher
trace: `pad_tracecheck.py <file> [<teacher>]`.

With a teacher it prints, per stroke pair, the point counts and the extents, and
the overall mean error / within-one-cell share of pad_corr.py's pairing.
"""
import json
import math
import sys
from trace_read import points_of, px_per_cell

PX = px_per_cell()   # measured, from pad_calib.json


def load(path):
    with open(path) as fh:
        return json.load(fh)


def points(stroke):
    return points_of(stroke)


def resample(pts, n=200):
    if len(pts) < 2:
        return []
    ox, oy = pts[0]
    P = [(x - ox, y - oy) for x, y in pts]
    cum = [0.0]
    for i in range(1, len(P)):
        cum.append(cum[-1] + math.hypot(P[i][0] - P[i - 1][0], P[i][1] - P[i - 1][1]))
    total = cum[-1]
    if total == 0:
        return []
    out, j = [], 0
    for k in range(n):
        target = total * k / (n - 1)
        while j < len(cum) - 2 and cum[j + 1] < target:
            j += 1
        seg = cum[j + 1] - cum[j]
        t = 0.0 if seg == 0 else (target - cum[j]) / seg
        out.append((P[j][0] + (P[j + 1][0] - P[j][0]) * t,
                    P[j][1] + (P[j + 1][1] - P[j][1]) * t))
    return out


def summarize(trace):
    print("%d stroke(s), %d point(s)" % (len(trace), sum(len(s) for s in trace)))
    for i, s in enumerate(trace):
        p = points(s)
        if not p:
            continue
        xs = [q[0] for q in p]
        ys = [q[1] for q in p]
        print("  %2d: n=%4d x %6.1f..%6.1f y %6.1f..%6.1f ts %s..%s"
              % (i, len(p), min(xs), max(xs), min(ys), max(ys),
                 s[0].get("ts"), s[-1].get("ts")))


def main():
    trace = load(sys.argv[1])
    summarize(trace)
    if len(sys.argv) < 3:
        return 0
    teacher = load(sys.argv[2])
    A = resample([q for s in teacher for q in points(s)])
    B = resample([q for s in trace for q in points(s)])
    if not A or not B:
        print("cannot pair: teacher %d, drawn %d" % (len(A), len(B)))
        return 1
    errs = [math.hypot(a[0] - b[0], a[1] - b[1]) * PX for a, b in zip(A, B)]
    errs.sort()
    within = sum(1 for e in errs if e <= PX) / len(errs)
    print("\nmean %.1f px, p90 %.1f px, within 1 cell %.1f %% (%d/%d)"
          % (sum(errs) / len(errs), errs[int(len(errs) * 0.9)],
             within * 100, sum(1 for e in errs if e <= PX), len(errs)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
