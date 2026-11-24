#!/usr/bin/env python3
"""Scan a pad trace for sharp corners (kinks) in time order.

A 'kink' is a direction change >= ANG degrees between consecutive
smoothed segments, EXCLUDING reversals that come from integer-cell
quantisation on a near-straight local run (those are artifacts).

usage: python3 scripts/kink_scan.py file.json [--ang 40] [--win 2]
"""
import json
import math
import sys


def load(path):
    d = json.load(open(path))
    strokes = []
    cur = []
    last_epoch = None
    for p in d:
        if isinstance(p, list):
            strokes.append(p)
            continue
        if last_epoch is not None and p.get("epoch") != last_epoch and cur:
            strokes.append(cur)
            cur = []
        last_epoch = p.get("epoch")
        cur.append(p)
    if cur:
        strokes.append(cur)
    return strokes


def kinks(pts, ang=40.0, win=2):
    out = []
    n = len(pts)
    for i in range(win, n - win):
        a = pts[i - win]
        b = pts[i]
        c = pts[i + win]
        v1 = (b["x"] - a["x"], b["y"] - a["y"])
        v2 = (c["x"] - b["x"], c["y"] - b["y"])
        n1 = math.hypot(*v1)
        n2 = math.hypot(*v2)
        if n1 < 1e-6 or n2 < 1e-6:
            continue
        cosv = max(-1.0, min(1.0, (v1[0] * v2[0] + v1[1] * v2[1]) / (n1 * n2)))
        deg = math.degrees(math.acos(cosv))
        if deg >= ang:
            out.append((i, round(b["x"], 1), round(b["y"], 1), round(deg, 1),
                        b.get("ts")))
    return out


def main():
    path = sys.argv[1]
    ang = 40.0
    win = 2
    args = sys.argv[2:]
    for i, a in enumerate(args):
        if a == "--ang":
            ang = float(args[i + 1])
        elif a == "--win":
            win = int(args[i + 1])
    strokes = load(path)
    print(f"{len(strokes)} stroke(s)")
    for si, pts in enumerate(strokes):
        print(f"\n== stroke {si}: n={len(pts)} "
              f"start=({pts[0]['x']},{pts[0]['y']}) "
              f"end=({pts[-1]['x']},{pts[-1]['y']}) "
              f"ts {pts[0].get('ts')}..{pts[-1].get('ts')}")
        ks = kinks(pts, ang, win)
        for i, x, y, deg, ts in ks:
            print(f"   idx {i:4d} ({x:5.1f},{y:5.1f}) {deg:6.1f} deg t={ts}")


if __name__ == "__main__":
    main()
