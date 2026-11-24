#!/usr/bin/env python3
"""Draw stroke polylines on the pad as many SHORT held segments.

MEASURED (this is why the drawer looks like this):
  * a long held stroke (`down drag 120 0 up`) records all 20 cells of its travel,
    but a held stroke with SEVERAL long drags keeps only ~3 samples per drag — the
    bowl's 31-cell sweep came back as 18 cells while its start and end were exact.
  * five short held segments (30 px each) covered 24.2 of 24.9 commanded cells
    (97 %) — short segments are what the recorder captures faithfully.
So a stroke is emitted as a chain of small `down drag up` spans, ~SEG_PX apart,
with the button released between them and the pointer landed exactly on the next
point while up (the landing never leaves ink).

Usage: paddraw.py <glyph> [-n]
       paddraw.py --points "x,y;x,y;..."     (cells, one stroke per 'stroke:')

Glyphs live in scripts/glyphs.py.
"""
import json
import math
import subprocess
import sys

KVM = "./kvm"
CALIB = "scripts/pad_calib.json"
CLEAR_BTN = (1825, 142)
SEG_PX = 30          # screen px per held segment (measured: 97 % delivery)


def sh(args, timeout=180):
    return subprocess.run([KVM, "mouse"] + args, capture_output=True, text=True,
                          timeout=timeout)


def pos():
    w = sh(["pos"]).stdout.split()
    if len(w) != 2:
        return None
    try:
        return int(w[0]), int(w[1])
    except ValueError:
        return None


def calib():
    with open(CALIB) as fh:
        c = json.load(fh)
    return c["px_per_cell"], c["px_per_cell_y"], c["left"], c["top"]


def park(target):
    tx, ty = int(round(target[0])), int(round(target[1]))
    sh(["abs", "move", str(tx), str(ty)])
    for _ in range(8):
        p = pos()
        if p is None:
            return
        dx, dy = tx - p[0], ty - p[1]
        if abs(dx) <= 1 and abs(dy) <= 1:
            return
        sh(["rel", "move", str(dx), str(dy)])


def land(target):
    """Put the pointer exactly on target with the button UP."""
    tx, ty = int(round(target[0])), int(round(target[1]))
    for _ in range(3):
        p = pos()
        if p is None:
            return
        dx, dy = tx - p[0], ty - p[1]
        if abs(dx) <= 1 and abs(dy) <= 1:
            return
        sh(["rel", "move", str(dx), str(dy)])


def split(pts, step):
    """Walk the polyline emitting a point every ~step px of arc length."""
    out = [pts[0]]
    for a, b in zip(pts, pts[1:]):
        d = math.hypot(b[0] - a[0], b[1] - a[1])
        if d <= 0:
            continue
        n = max(1, int(round(d / step)))
        for i in range(1, n + 1):
            out.append((a[0] + (b[0] - a[0]) * i / n, a[1] + (b[1] - a[1]) * i / n))
    return out


def draw_polyline(cells, kx, ky, bx, by):
    scr = [(bx + x * kx, by + y * ky) for x, y in cells]
    pts = split(scr, SEG_PX)
    park(pts[0])
    for a, b in zip(pts, pts[1:]):
        dx, dy = int(round(b[0] - a[0])), int(round(b[1] - a[1]))
        if dx or dy:
            sh(["rel", "down", "drag", str(dx), str(dy), "up"])
        land(b)


def clear_pad():
    sh(["abs", "move", str(CLEAR_BTN[0]), str(CLEAR_BTN[1])])
    sh(["rel", "down", "up"])


def main():
    sys.path.insert(0, "scripts")
    from glyphs import GLYPHS

    args = [a for a in sys.argv[1:] if not a.startswith("-")]
    plan_only = "-n" in sys.argv[1:]
    if not args:
        print("glyphs: " + ", ".join(sorted(GLYPHS)))
        return 0
    kx, ky, bx, by = calib()
    plan = GLYPHS[args[0]] if args[0] in GLYPHS else None
    if plan is None:
        print("unknown glyph %r" % args[0])
        return 1
    total = sum(len(s) for s in plan)
    print("glyph %s: %d stroke(s), %d points, map %.3fx+%.1f / %.3fy+%.1f"
          % (args[0], len(plan), total, kx, bx, ky, by))
    if plan_only:
        for i, s in enumerate(plan):
            print("  stroke %d: %d pts (%.1f,%.1f)..(%.1f,%.1f)"
                  % (i, len(s), s[0][0], s[0][1], s[-1][0], s[-1][1]))
        return 0
    clear_pad()
    for i, s in enumerate(plan):
        draw_polyline(s, kx, ky, bx, by)
        print("  stroke %d drawn (%d segments of ~%d px)"
              % (i, max(1, len(s) - 1), SEG_PX), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
