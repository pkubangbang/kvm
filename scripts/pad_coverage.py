#!/usr/bin/env python3
"""Coverage of the pad record: which 6 px cells the ink actually visits.

Usage: pad_coverage.py [<trace.json>]

Reads /api/trace (or the given file) and reports, per stroke and in total:
  * the cells touched by the recorded polyline (interpolated at 1 px, so a
    glide crossing a cell counts it even if no sample landed inside it),
  * the bounding box of those cells,
  * the ink length in px and cells, and the fill ratio (cells touched /
    cells in the bounding box) — 1.0 for a solid horizontal or vertical line of
    one cell thick, ~0.5 for a clean diagonal.
"""
import json
import math
import subprocess
import sys
from trace_read import read_trace, points_of, px_per_cell
import os

PX = px_per_cell()   # measured, from pad_calib.json
N = 128


def load(path):
    if path:
        with open(path) as fh:
            return json.load(fh)
    return read_trace(os.environ.get("KVM_PAD_OUT"))


def cells_of(stroke):
    pts = points_of(stroke)
    seen = set()
    length = 0.0
    for i in range(1, len(pts)):
        a, b = pts[i - 1], pts[i]
        d = math.hypot(b[0] - a[0], b[1] - a[1])
        length += d
        steps = max(1, int(d * PX))          # ~1 px per step
        for k in range(steps + 1):
            t = k / steps
            cx = a[0] + (b[0] - a[0]) * t
            cy = a[1] + (b[1] - a[1]) * t
            c = (int(cx), int(cy))
            if 0 <= c[0] < N and 0 <= c[1] < N:
                seen.add(c)
    if len(pts) == 1:
        c = (int(pts[0][0]), int(pts[0][1]))
        if 0 <= c[0] < N and 0 <= c[1] < N:
            seen.add(c)
    return seen, length * PX


def main():
    trace = load(sys.argv[1] if len(sys.argv) > 1 else None)
    if not trace:
        print("no strokes recorded")
        return 1
    total = set()
    for i, st in enumerate(trace):
        seen, px = cells_of(st)
        total |= seen
        if seen:
            xs = [c[0] for c in seen]
            ys = [c[1] for c in seen]
            bw, bh = max(xs) - min(xs) + 1, max(ys) - min(ys) + 1
            print("stroke %d: %4d cells  bbox (%d,%d) %dx%d  fill %.2f  ink %.0f px  %.1f cells"
                  % (i, len(seen), min(xs), min(ys), bw, bh,
                     len(seen) / (bw * bh), px, px / PX))
        else:
            print("stroke %d: outside the grid" % i)
    if total:
        xs = [c[0] for c in total]
        ys = [c[1] for c in total]
        bw, bh = max(xs) - min(xs) + 1, max(ys) - min(ys) + 1
        print("TOTAL: %d cells of %d (%.2f %% of the pad)  bbox (%d,%d) %dx%d  fill %.2f"
              % (len(total), N * N, len(total) / (N * N) * 100,
                 min(xs), min(ys), bw, bh, len(total) / (bw * bh)))
    return 0


if __name__ == "__main__":
    sys.exit(main())
