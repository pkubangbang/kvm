#!/usr/bin/env python3
"""Temporal path correlation: do two traces walk the same path in the same order?

Usage: pad_corr.py <teacher.json> [<drawn.json>]

This is the metric the shape actually needs. Cell overlap measures area; this
measures the PATH, in time order:

  1. both traces are normalised to their own first point (placement out of the
     score) and resampled at equal arc-length steps (so sample density and
     timing do not bias it);
  2. every teacher sample is paired with the drawn sample at the SAME normalised
     arc position — i.e. "what was being drawn at 30 % of the stroke should be
     where the teacher was at 30 % of its stroke";
  3. the correlation is reported as a distance profile: mean error, 90th
     percentile, and the fraction of paired points within 1 cell (6 px).

A trace that draws the same strokes in the wrong order, or closes a loop at the
wrong time, cannot score high here even if the ink covers the same cells.
"""
import json
import math
import subprocess
import sys
from trace_read import read_trace, points_of, px_per_cell
import os

SAMPLE = 200          # equal steps along each path
PX = px_per_cell()   # measured, from pad_calib.json


def load(path):
    if path:
        with open(path) as fh:
            return json.load(fh)
    return read_trace(os.environ.get("KVM_PAD_OUT"))


def points(trace):
    out = []
    for st in trace:
        for p in st:
            out.append(points_of([p])[0])
    return out


def resample(pts, n=SAMPLE):
    """Equal arc-length samples, normalised to the first point."""
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


def main():
    A = resample(points(load(sys.argv[1])))
    B = resample(points(load(sys.argv[2] if len(sys.argv) > 2 else None)))
    if not A or not B:
        print("empty trace: teacher %d, drawn %d samples" % (len(A), len(B)))
        return 1
    errs = [math.hypot(a[0] - b[0], a[1] - b[1]) * PX for a, b in zip(A, B)]
    errs_sorted = sorted(errs)
    mean = sum(errs) / len(errs)
    p90 = errs_sorted[int(len(errs) * 0.9)]
    within = sum(1 for e in errs if e <= PX) / len(errs)
    # Where along the stroke does it diverge worst?
    worst_i = max(range(len(errs)), key=lambda i: errs[i])
    print("paired samples %d (equal arc-length, first point normalised)" % len(errs))
    print("mean error      %6.1f px" % mean)
    print("90th percentile %6.1f px" % p90)
    print("within 1 cell   %6.1f %%  (%d/%d)" % (within * 100, sum(1 for e in errs if e <= PX), len(errs)))
    print("worst divergence %5.1f px at %3.0f %% along the stroke (drawn cell %.1f,%.1f vs teacher %.1f,%.1f)"
          % (errs[worst_i], worst_i / len(errs) * 100,
             B[worst_i][0], B[worst_i][1], A[worst_i][0], A[worst_i][1]))
    # First point where the two paths separate by more than 2 cells and never
    # come back — that is the first real shape difference.
    print("\n  % along | drawn cell      | teacher cell    | error px")
    for k in range(0, len(errs), len(errs) // 10):
        print("  %6.0f %% | (%6.1f,%6.1f) | (%6.1f,%6.1f) | %6.1f"
              % (k / len(errs) * 100, B[k][0], B[k][1], A[k][0], A[k][1], errs[k]))
    return 0 if within >= 0.9 else 1


if __name__ == "__main__":
    sys.exit(main())
