#!/usr/bin/env python3
"""Draw a glyph by copying a teacher's recorded strokes through kvm.

Usage: pad_copy_teacher.py <teacher.json> [--ox N --oy N]

The teacher file is a pad trace: a list of strokes, each a list of points with
cell coordinates (x, y) plus optional in-cell offsets (fx, fy). Each teacher
stroke becomes ONE held span: the pointer is parked on the stroke's first point
(absolute), then every further point is sent as a relative `drag` of the integer
difference, finishing with `up`. This reproduces the teacher's path shape
faithfully instead of approximating it with a handful of long segments.

Afterwards it prints the drawn trace's cell coverage next to the teacher's.
"""
import json
import math
import subprocess
import sys
from trace_read import read_trace, points_of, px_per_cell
import os

KVM = "./kvm"
PX = px_per_cell()   # measured, from pad_calib.json


def sh(args, timeout=120):
    return subprocess.run([KVM, "mouse"] + args, capture_output=True, text=True,
                          timeout=timeout)


def cells(trace):
    seen = set()
    for st in trace:
        pts = points_of(st)
        for i in range(1, len(pts)):
            a, b = pts[i - 1], pts[i]
            d = math.hypot(b[0] - a[0], b[1] - a[1])
            n = max(1, int(d * PX))
            for k in range(n + 1):
                t = k / n
                seen.add((int(a[0] + (b[0] - a[0]) * t), int(a[1] + (b[1] - a[1]) * t)))
    return seen


def main():
    with open(sys.argv[1]) as fh:
        teacher = json.load(fh)
    ox = oy = 0
    args = sys.argv[2:]
    for i, a in enumerate(args):
        if a == "--ox":
            ox = int(args[i + 1])
        if a == "--oy":
            oy = int(args[i + 1])

    # The teacher is in cell space; the canvas origin maps cell -> screen px. It
    # is MEASURED (scripts/pad_bbox.py, or whatever wrote the calibration file)
    # and handed in — the pad server is never asked where its own canvas is,
    # because a stale or cached answer there displaces the whole glyph.
    anchor = os.environ.get("PAD_ANCHOR")
    if not anchor:
        raise SystemExit("set PAD_ANCHOR=X,Y — the canvas top-left in screen px")
    cx, cy = [int(v) for v in anchor.split(",")]

    for n, st in enumerate(teacher):
        pts = [(x + ox, y + oy)
               for p in st]
        # Park: absolute screen px of the first point, rounded.
        sx = int(round(cx + pts[0][0] * PX))
        sy = int(round(cy + pts[0][1] * PX))
        sh(["abs", "move", str(sx), str(sy)])
        # ALIGN: an absolute move is not repeatable to the pixel on this host, so
        # read where the wire actually landed and walk the residual off before
        # pressing. Without this the whole glyph is displaced and a cell-overlap
        # score measures placement instead of shape.
        for _ in range(3):
            wire = sh(["pos"]).stdout.split()
            if len(wire) != 2:
                break
            ddx, ddy = sx - int(wire[0]), sy - int(wire[1])
            if abs(ddx) <= 2 and abs(ddy) <= 2:
                break
            sh(["rel", "move", str(ddx), str(ddy)])
        verbs = ["down"]
        last = pts[0]
        for p in pts[1:]:
            dx = int(round((p[0] - last[0]) * PX))
            dy = int(round((p[1] - last[1]) * PX))
            if dx == 0 and dy == 0:
                continue
            verbs += ["drag", str(dx), str(dy)]
            last = p
        verbs.append("up")
        sh(["rel"] + verbs)
        print("stroke %d: %d points -> %d reports" % (n, len(pts), (len(verbs) - 2) // 2))
        # Re-sync BEFORE the next press: a report stream is relative, so any
        # drift in the drawn stroke would be carried into the next stroke. The
        # wire position after `up` is read and the stroke is replayed onto it.
        wire = sh(["pos"]).stdout.split()
        drift = (int(wire[0]) - sx, int(wire[1]) - sy) if len(wire) == 2 else (0, 0)
        print("  wire vs park drift %+d,%+d px (%.1f cells)"
              % (drift[0], drift[1], math.hypot(*drift) / PX))

    drawn = read_trace(os.environ.get("KVM_PAD_OUT"))
    tc, dc = cells(teacher), cells(drawn)
    hit = len(tc & dc)
    print("teacher %d cells, drawn %d cells, overlap %d = %.1f %% of the teacher"
          % (len(tc), len(dc), hit, hit / len(tc) * 100))
    miss = sorted(tc - dc)
    extra = sorted(dc - tc)
    print("missed %d cells%s" % (len(miss), (": " + str(miss[:12])) if miss else ""))
    print("extra  %d cells%s" % (len(extra), (": " + str(extra[:12])) if extra else ""))
    return 0 if hit / len(tc) >= 0.9 else 1


if __name__ == "__main__":
    sys.exit(main())
