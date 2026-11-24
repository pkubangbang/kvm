#!/usr/bin/env python3
"""Sequence-correlation test: anticipated glyph path vs what the pad recorded.

Usage: pad_seq_corr.py <ox> <oy> <glyph> ...

<glyph> is one of Z B R A, drawn in a square of LEG px anchored at (ox,oy) in
screen px. Each glyph is drawn as one held stroke per segment (down drag up),
which is the form the pad has been proven to record faithfully.

Reported per glyph:
  * points recorded / ms,
  * per-segment recorded displacement vs commanded (px and %),
  * turning-corner match: the recorded corner nearest each commanded vertex,
  * sequence correlation: the fraction of commanded waypoints visited in
    order along the recorded polyline (1.0 = perfect order, no crossings),
  * slot containment: every recorded point inside the glyph's allotted square.

Exit 0 only when every glyph's segment error is within 8 % and the sequence
correlation is >= 0.9 and all points lie inside the slot.
"""
import json
import math
import subprocess
import sys
from trace_read import read_trace, points_of, px_per_cell
import os

KVM = "./kvm"
PX = px_per_cell()   # measured, from pad_calib.json
PAD = "http://127.0.0.1:8898"
LEG = 210.0          # glyph square side in px
GAP = 30.0           # gap between glyphs (px)


def run(*args, timeout=60):
    return subprocess.run([KVM, "mouse"] + list(args), capture_output=True,
                          text=True, timeout=timeout)


def pos():
    out = run("pos").stdout.split()
    return int(out[0]), int(out[1])


def trace():
    return read_trace(os.environ.get("KVM_PAD_OUT"))


def glyph_path(g, x, y, s):
    l = s * 0.32       # b/r/a have short bowls
    if g == "Z":
        return [(x, y), (x + s, y), (x, y + s), (x + s, y + s)]
    if g == "B":
        return [(x, y + s), (x, y), (x + s, y), (x + s, y + s * 0.5),
                (x, y + s * 0.5), (x + s, y + s * 0.5), (x + s, y + s), (x, y + s)]
    if g == "R":
        return [(x, y + s), (x, y), (x + s, y), (x + s, y + l * 1.6),
                (x, y + l * 1.6), (x + s, y + s)]
    if g == "A":
        return [(x + s, y + s), (x + s, y), (x, y), (x, y + l * 1.4),
                (x + s, y + l * 1.4), (x + s, y + s * 0.6), (x, y + s * 0.6),
                (x, y + s)]
    raise SystemExit("unknown glyph %r" % g)


def draw(path):
    """Park with abs, then one held span per segment."""
    x0, y0 = path[0]
    run("abs", "move", str(int(x0)), str(int(y0)))
    start = pos()
    for (ax, ay), (bx, by) in zip(path, path[1:]):
        dx, dy = int(round(bx - ax)), int(round(by - ay))
        if dx == 0 and dy == 0:
            continue
        run("rel", "down", "drag", str(dx), str(dy), "up", timeout=60)
    return start, pos()


def main():
    ox, oy = float(sys.argv[1]), float(sys.argv[2])
    glyphs = sys.argv[3:]
    before = len(trace())
    ok = True
    total_ms = 0
    for i, g in enumerate(glyphs):
        x = ox + (i % 2) * (LEG + GAP)
        y = oy + (i // 2) * (LEG + GAP)
        path = glyph_path(g, x, y, LEG)
        start, end = draw(path)
        strokes = trace()[before:]
        before += len(strokes)
        pts = []
        ms = 0
        for st in strokes:
            for p in st:
                pts.append(points_of([p])[0])
            if len(st) > 1:
                ms += st[-1]["ts"] - st[0]["ts"]
        total_ms += ms
        print("== %s @ (%d,%d): park %s -> wire %s, %d strokes, %d pts, %d ms"
              % (g, x, y, start, (int(path[-1][0]), int(path[-1][1])),
                 len(strokes), len(pts), ms))
        if len(strokes) != len(path) - 1:
            print("   FAIL: %d segments expected, %d strokes recorded"
                  % (len(path) - 1, len(strokes)))
            ok = False
        # per-segment displacement from each stroke
        for k, st in enumerate(strokes):
            (ax, ay), (bx, by) = path[k], path[k + 1]
            a, b = points_of([st[0]])[0], points_of([st[-1]])[0]
            got = math.hypot((b[0] - a[0]) * PX, (b[1] - a[1]) * PX)
            want = math.hypot(bx - ax, by - ay)
            err = (got - want) / want * 100.0 if want else 0.0
            ang_g = math.degrees(math.atan2((b[1] - a[1]) * PX, (b[0] - a[0]) * PX))
            ang_w = math.degrees(math.atan2(by - ay, bx - ax))
            d_ang = abs((ang_g - ang_w + 180) % 360 - 180)
            if abs(err) > 8 or d_ang > 12:
                ok = False
            print("   seg%d %-22s got %6.1f want %6.1f (%+5.1f %%), dir %6.1f/%6.1f deg"
                  % (k + 1, "(%.0f,%.0f)->(%.0f,%.0f)" % (ax, ay, bx, by),
                     got, want, err, ang_g, ang_w))
        # sequence correlation + slot containment
        if pts:
            cum = [0.0]
            for j in range(1, len(pts)):
                cum.append(cum[-1] + math.hypot(pts[j][0] - pts[j - 1][0],
                                                pts[j][1] - pts[j - 1][1]))
            last = -1
            hits = 0
            for (wx, wy) in path:
                c = (wx / PX, wy / PX)
                best, bestd = -1, 1e9
                for j, p in enumerate(pts):
                    d = math.hypot(p[0] - c[0], p[1] - c[1])
                    if d < bestd:
                        best, bestd = j, d
                if bestd * PX <= 12.0 and best > last:
                    hits += 1
                    last = best
            print("   sequence correlation %.2f (%d/%d waypoints in order), max corner err %.1f px"
                  % (hits / len(path), hits, len(path), bestd * PX))
            if hits / len(path) < 0.9:
                ok = False
            lo = ((x - ox) / PX - 1.0, (y - oy) / PX - 1.0)
            hi = ((x + LEG - ox) / PX + 1.0, (y + LEG - oy) / PX + 1.0)
            out = [p for p in pts if p[0] < lo[0] or p[1] < lo[1] or p[0] > hi[0] or p[1] > hi[1]]
            print("   slot containment: %d/%d points inside (%.1f,%.1f)-(%.1f,%.1f)"
                  % (len(pts) - len(out), len(pts), lo[0], lo[1], hi[0], hi[1]))
            if out:
                ok = False
    print("TOTAL glyph time %d ms (cap 10000 ms)" % total_ms)
    if total_ms >= 10000:
        ok = False
    print("VERDICT:", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
