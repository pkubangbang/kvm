#!/usr/bin/env python3
"""Draw the teacher's b, converting cells to screen with the MEASURED pad mapping.

The pad page maps pointer pixels with its own live getBoundingClientRect, and it
is NOT the -box rect: measured on this screen the canvas is ~6.536 px per cell
and its left edge is at ~1035 px, not 6 px / 1067 px (analysis.md §4: the canvas
CSS box is driven by the viewport and centred, so it is 8.8 % larger and ~25 px
left of the pinned rect). Drawing with the pinned 6 px puts every glyph at the
wrong place and the wrong scale.

The mapping lives in scripts/pad_calib.json, measured by moving the pointer to
two known screen positions and reading the cell the page reports for each. Re-run
that probe whenever the window changes size.

Emission (analysis.md): ONE held down..up span per stroke, reports of ~6 px at a
22 ms (stem) / 26 ms (bowl) gap so the stroke is in flight ~1.5 s / 2.5 s and the
page samples once per frame; `wait` inside the held run is the per-report cadence.

Usage: pad_glyph.py [stem] [bowl] [-n]
"""
import json
import math
import os
import subprocess
import sys
from trace_read import points_of

KVM = "./kvm"
TEACHER = "reference/pad-trace-raw.json"
CALIB = "scripts/pad_calib.json"
REPORT_PX = 6
GAPS = {"stem": 22, "bowl": 26}


def sh(args, timeout=180):
    return subprocess.run([KVM, "mouse"] + args, capture_output=True, text=True,
                          timeout=timeout)


def calib():
    with open(CALIB) as fh:
        c = json.load(fh)
    # X and Y do NOT share a scale: the file carries 6.024 and 5.955, and using
    # the X value for both put a 67-cell stem 4.6 px (0.77 cell) long.
    kx = c.get("px_per_cell", 6.0)
    ky = c.get("px_per_cell_y", kx)
    # left/top are the canvas's own screen origin, MEASURED and written into the
    # calibration file. There is no fallback to the server: the pad is never
    # asked to describe itself, because a wrong answer there is indistinguishable
    # from a right one and silently displaces every glyph.
    left = c.get("left")
    top = c.get("top")
    if left is None or top is None:
        raise SystemExit("no calibration: %s needs measured left/top" % CALIB)
    return kx, ky, left, top


def pos():
    w = sh(["pos"]).stdout.split()
    if len(w) != 2:
        return None
    try:
        return int(w[0]), int(w[1])
    except ValueError:
        return None


def teacher_strokes():
    with open(TEACHER) as fh:
        trace = json.load(fh)
    return [points_of(st)
            for st in trace]


def resample(pts, step_px, px):
    out = [pts[0]]
    acc = 0.0
    for a, b in zip(pts, pts[1:]):
        seg = math.hypot(b[0] - a[0], b[1] - a[1]) * px
        if seg <= 0:
            continue
        t = 0.0
        while acc + (seg - t) >= step_px:
            t += step_px - acc
            f = t / seg
            out.append((a[0] + (b[0] - a[0]) * f, a[1] + (b[1] - a[1]) * f))
            acc = 0.0
        acc += seg - t
    if out[-1] != pts[-1]:
        out.append(pts[-1])
    return out


def park(target):
    sh(["abs", "move", str(target[0]), str(target[1])])
    for _ in range(8):
        p = pos()
        if p is None:
            return
        ddx, ddy = target[0] - p[0], target[1] - p[1]
        if abs(ddx) <= 1 and abs(ddy) <= 1:
            return
        sh(["rel", "move", str(ddx), str(ddy)])


def stroke(path_px, gap_ms, start=None):
    """One held span. The delta accumulator starts at the ACTUAL pointer
    position (`start`), not at (0,0): starting at zero made the first drag the
    stroke's absolute screen coordinate (~1000 px) — a held jump from the park
    position, across and off the pad, before the glyph began."""
    verbs = ["down"]
    fx, fy = (start if start else path_px[0])
    for x, y in path_px[1:]:
        dx, dy = x - fx, y - fy
        if dx == 0 and dy == 0:
            continue
        verbs += ["drag", str(dx), str(dy), "wait", str(gap_ms)]
        fx, fy = x, y
    verbs.append("up")
    r = subprocess.run([KVM, "mouse", "rel"] + verbs, capture_output=True,
                       text=True, timeout=180)
    if r.returncode != 0:
        sys.stderr.write(r.stderr[-400:])
    return r.returncode


def main():
    argv = list(sys.argv[1:])
    plan_only = "-n" in argv
    which = [a for a in argv if not a.startswith("-")] or ["stem", "bowl"]
    kx, ky, left, top = calib()
    print("calibration: %.3f px/cell x, %.3f px/cell y, origin (%.1f, %.1f)"
          % (kx, ky, left, top))
    strokes = teacher_strokes()
    names = ["stem", "bowl"]
    for name in which:
        pts = resample(strokes[names.index(name)], REPORT_PX, kx)
        path_px = [(int(round(left + p[0] * kx)), int(round(top + p[1] * ky)))
                   for p in pts]
        print("%s: %d reports, gap %d ms, cells (%.1f,%.1f)..(%.1f,%.1f) -> "
              "screen (%d,%d)..(%d,%d)"
              % (name, len(path_px) - 1, GAPS[name], pts[0][0], pts[0][1],
                 pts[-1][0], pts[-1][1], path_px[0][0], path_px[0][1],
                 path_px[-1][0], path_px[-1][1]), flush=True)
        if plan_only:
            continue
        park(path_px[0])
        p = pos()                       # seed from the pointer we actually have
        if stroke(path_px, GAPS[name], start=p or path_px[0]) != 0:
            return 1
    return 0


if __name__ == "__main__":
    sys.exit(main())
