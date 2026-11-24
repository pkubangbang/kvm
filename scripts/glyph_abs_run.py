#!/usr/bin/env python3
"""Draw one glyph with GENTLE, EXACT motion: absolute hop between sample points.

WHY ABSOLUTE HOPS (measured on this host, 2026-11-24):

  Relative reports are not a reliable unit of distance here. The SAME 213 px
  request lands 284-324 px however the button is set (1.5x), and slowing the
  stream does not fix it: 35 x 6 px at 16 ms delivered 70 px, the same at 8 ms
  delivered 0, and 6 px reports still overshoot 1.21x. The host accelerates a
  relative STREAM and drops reports below ~30 px.

  A single absolute (0x04) report is exact and idempotent: AbsGoto(1500,500)
  measures (1501,500), AbsGoto(300,800) measures (301,800). So the glyph is
  drawn as: press at the stroke's first point, then one absolute hop per
  SAMPLE_SPACING px of the stroke, then release. The pad keeps accumulating
  samples while the button is held, so a hop chain inks a continuous stroke
  (verified: press + 6 hops + release records one stroke, not six).

usage: glyph_abs_run.py <glyph> [--dry-run]
"""
import json
import glob
import os
import subprocess
import sys
import time

KVM = "./kvm"
PAD_BOX = (1068, 162, 758, 758)   # x, y, w, h of the pad canvas on screen
GRID = 128                        # cells across the pad page
SAMPLE_SPACING = 40               # px between ink samples (~7 cells)
PARK_ROUNDS = 6


def cell_size():
    _, _, w, h = PAD_BOX
    return w / float(GRID), h / float(GRID)


def run(*a):
    return subprocess.run([KVM] + list(a), capture_output=True, text=True)


def park(x, y):
    """Converge the pointer on (x,y) with the exact absolute report."""
    for _ in range(PARK_ROUNDS):
        run("mouse", "abs", "move", str(int(round(x))), str(int(round(y))))
        p = run("mouse", "pos").stdout.split()
        if len(p) == 2 and abs(int(p[0]) - x) <= 2 and abs(int(p[1]) - y) <= 2:
            return (int(p[0]), int(p[1]))
        time.sleep(0.03)
    return None


def walk(points, spacing):
    """Resample a pixel polyline to points at most `spacing` px apart."""
    out = [points[0]]
    for a, b in zip(points, points[1:]):
        dx, dy = b[0] - a[0], b[1] - a[1]
        n = max(1, int(round(max(abs(dx), abs(dy)) / spacing)))
        for i in range(1, n + 1):
            out.append((a[0] + dx * i / float(n), a[1] + dy * i / float(n)))
    return out


def main():
    if len(sys.argv) < 2:
        print("usage: glyph_abs_run.py <glyph> [--dry-run]")
        return 2
    sys.path.insert(0, "scripts")
    from glyphs import GLYPHS
    name = sys.argv[1]
    dry = "--dry-run" in sys.argv
    if name not in GLYPHS:
        print("unknown glyph %r; have: %s" % (name, ", ".join(sorted(GLYPHS))))
        return 2
    kx, ky = cell_size()
    bx, by = PAD_BOX[0], PAD_BOX[1]
    strokes = GLYPHS[name]
    print("%s: %d stroke(s), %.3f/%.3f px per cell, origin (%d,%d), spacing %d px"
          % (name, len(strokes), kx, ky, bx, by, SAMPLE_SPACING))
    plans = []
    for i, cells in enumerate(strokes):
        pts = [(bx + x * kx, by + y * ky) for x, y in cells]
        hops = walk(pts, SAMPLE_SPACING)
        plans.append(hops)
        print("  stroke %d: %d cells, %d hops, start (%.0f,%.0f) end (%.0f,%.0f)"
              % (i, len(cells), len(hops), pts[0][0], pts[0][1], pts[-1][0], pts[-1][1]))
    if dry:
        return 0

    for i, hops in enumerate(plans):
        got = park(*hops[0])
        if got is None:
            print("    no park at (%.0f,%.0f) — not pressing" % hops[0])
            return 1
        print("    parked at (%d,%d)" % got)
        run("mouse", "rel", "down", "drag", "0", "0")
        for hx, hy in hops[1:]:
            if park(hx, hy) is None:
                print("    hop to (%.0f,%.0f) failed — releasing" % (hx, hy))
                run("mouse", "rel", "drag", "0", "0", "up")
                return 1
        run("mouse", "rel", "drag", "0", "0", "up")
        time.sleep(0.25)
    print("drawn. certify with:")
    print("  N=$(ls -t /tmp/padtrace/*.json | head -1)")
    print("  python3 scripts/stroke_direction.py \"$N\"")
    return 0


if __name__ == "__main__":
    sys.exit(main())
