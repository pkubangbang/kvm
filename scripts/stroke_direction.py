#!/usr/bin/env python3
"""Judge a stroke's DIRECTION: how many real turns it has, and which way each leg runs.

A letter has a small, fixed number of corners. Z has two (top-right, bottom-left).
A trace that reports four has either a bad emitter or a bad plan — and a planner
that emits a leg in the wrong direction produces a shape that is not the letter
at all, while every metric that only measures "path length" or "bbox" still looks
fine. This script exists to catch exactly that: it prints, per stroke, the
sequence of constant-heading legs and the signed turn between consecutive legs.

usage: stroke_direction.py <trace.json> [stroke-index]

The trace is a JSON array of strokes; a stroke is either a list of points or an
object with a "points" list, each point carrying x, y (in pad cells).
"""
import json
import math
import sys

LEG_MERGE_DEG = 25.0   # headings within this of each other are the same leg
MIN_CORNER_DEG = 25.0  # a change below this is quantisation, not a corner
MIN_LEG_LEN = 1.0      # cells; a shorter leg is a jitter artefact


def points_of(stroke):
    return stroke["points"] if isinstance(stroke, dict) else stroke


def legs(pts):
    """Collapse the sample stream into constant-heading legs."""
    runs = []
    for i in range(1, len(pts)):
        dx = pts[i]["x"] - pts[i - 1]["x"]
        dy = pts[i]["y"] - pts[i - 1]["y"]
        if dx == 0 and dy == 0:
            continue
        h = math.degrees(math.atan2(dy, dx))
        if runs:
            start, end, hh, length = runs[-1]
            d = abs(((h - hh + 180.0) % 360.0) - 180.0)
            if d <= LEG_MERGE_DEG:
                runs[-1] = (start, i, hh, length + math.hypot(dx, dy))
                continue
        runs.append((i, i, h, math.hypot(dx, dy)))
    return [r for r in runs if r[3] >= MIN_LEG_LEN]


def turns(runs):
    """The signed turn between consecutive legs, with the corner coordinates."""
    out = []
    for a, b in zip(runs, runs[1:]):
        d = b[2] - a[2]
        while d > 180:
            d -= 360
        while d < -180:
            d += 360
        if abs(d) >= MIN_CORNER_DEG:
            out.append((b[0], round(d, 1), round(b[2], 1)))
    return out


def main():
    trace = json.load(open(sys.argv[1]))
    only = int(sys.argv[2]) if len(sys.argv) > 2 else None
    for idx, stroke in enumerate(trace):
        if only is not None and idx != only:
            continue
        pts = points_of(stroke)
        if len(pts) < 3:
            continue
        runs = legs(pts)
        ts = turns(runs)
        print("stroke %d: %d points, %d legs, %d corners"
              % (idx, len(pts), len(runs), len(ts)))
        for r in runs:
            p = pts[r[0]]
            print("   leg %+8.1f deg  len %6.1f cells  from (%5.1f,%5.1f)"
                  % (r[2], r[3], p["x"], p["y"]))
        for t in ts:
            p = pts[t[0]]
            print("   corner %+7.1f deg at (%5.1f,%5.1f)  heading -> %+7.1f"
                  % (t[1], p["x"], p["y"], t[2]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
