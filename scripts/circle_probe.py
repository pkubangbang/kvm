#!/usr/bin/env python3
"""Measure what a held-span draw produces, on both witnesses, keyed by timestamp.

Draws one shape with `kvm mouse rel -gap 2 down drag .. up` (ONE invocation, one
held span) and reports the WIRE travel and the pad's INK for THAT stroke only.

The stroke identity is the timestamp window, never an index: the page re-posts
its WHOLE record on every stroke end, so `post[len(pre):]` is not "the new
stroke" (it hands back whatever the list grew by, which after a repost is old
ink). Any stroke whose first point is newer than the draw's start is ours.

  WIRE — pointer before and after, read with `kvm mouse pos`.
  INK  — the matching recorded strokes, in cells and in pad pixels.

Usage: circle_probe.py [--shape circle|zigzag|line] [--r 50] [--n 24] [--len 300]
Run from the repository root (needs ./kvm built there and the pad on :8898).
"""
import argparse
import json
import math
import subprocess
import time
import urllib.request

BASE = "http://127.0.0.1:8898"


def http(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read())


def kvm(*args, timeout=60):
    return subprocess.run(["./kvm", "mouse", *args], capture_output=True,
                          text=True, timeout=timeout)


def pos():
    r = kvm("pos")
    parts = r.stdout.split()
    if r.returncode != 0 or len(parts) < 2:
        raise SystemExit("kvm mouse pos failed: " + r.stdout + r.stderr)
    return int(parts[0]), int(parts[1])


def page_now():
    """Read the page's own stroke timestamps so the next window starts after them."""
    rec = http("GET", "/api/trace")
    return max((s[0]["ts"] for s in rec if s), default=0)


def legs_for(shape, a):
    if shape == "circle":
        pts = []
        for k in range(a.n + 1):
            th = 2 * math.pi * k / a.n
            pts.append((int(round(a.r * math.cos(th))), int(round(a.r * math.sin(th)))))
    elif shape == "zigzag":
        h = a.len // 2
        pts = [(0, 0), (h, a.len), (2 * h, 0)]
    else:
        pts = [(0, 0), (a.len, 0)]
    legs = []
    for i in range(1, len(pts)):
        legs.append((pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]))
    return pts, legs


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--shape", default="circle", choices=["circle", "zigzag", "line"])
    ap.add_argument("--r", type=int, default=50, help="circle radius, screen px")
    ap.add_argument("--n", type=int, default=24, help="arcs around the circle")
    ap.add_argument("--len", type=int, default=300, help="leg length, screen px")
    ap.add_argument("--cx", type=int, default=1330)
    ap.add_argument("--cy", type=int, default=480)
    a = ap.parse_args()

    box = http("GET", "/api/box")
    px_cell = box["w"] / 128  # the page's own map: 128 cells across cv.width

    http("POST", "/api/trace", [])
    time.sleep(0.2)

    pts, legs = legs_for(a.shape, a)
    start = (a.cx + pts[0][0] - (a.r if a.shape == "circle" else 0) + (a.r if a.shape == "circle" else 0),
             a.cy + pts[0][1])
    # for the circle, pts[0] is (r, 0): shift so the circle is centred on cx,cy
    if a.shape == "circle":
        start = (a.cx + a.r, a.cy)
    kvm("abs", "move", str(start[0]), str(start[1]))
    time.sleep(0.3)
    p0 = pos()

    t_draw = page_now()
    time.sleep(0.05)

    args = []
    for dx, dy in legs:
        args += ["drag", str(dx), str(dy)]
    t0 = time.time()
    r = kvm("rel", "-gap", "2", "down", *args, "up")
    dt = time.time() - t0
    time.sleep(0.3)
    p1 = pos()
    print(f"draw  rc={r.returncode} legs={len(legs)} {dt:.2f}s {r.stderr.strip()[:110]}")
    print(f"wire  {p0} -> {p1}  delta=({p1[0]-p0[0]},{p1[1]-p0[1]})")
    cmd_len = sum(math.hypot(dx, dy) for dx, dy in legs)
    print(f"wire  commanded path length {cmd_len:.0f}px, delivered {math.hypot(p1[0]-p0[0],p1[1]-p0[1]):.0f}px displacement")

    time.sleep(0.4)
    rec = http("GET", "/api/trace")
    mine = [s for s in rec if s and s[0]["ts"] > t_draw]
    print(f"ink   {len(rec)} strokes in record, {len(mine)} from this draw")
    if not mine:
        print("RESULT FAIL no ink recorded for the draw")
        return 1
    for s in mine:
        xs = [q["x"] for q in s]
        ys = [q["y"] for q in s]
        bw, bh = (max(xs) - min(xs)), (max(ys) - min(ys))
        print(f"ink   {len(s)} pts bbox x {min(xs)}-{max(xs)} y {min(ys)}-{max(ys)} "
              f"=> {bw} x {bh} cells = {bw*px_cell:.0f} x {bh*px_cell:.0f} px (px/cell {px_cell:.2f})")
        print(f"      first={(s[0]['x'],s[0]['y'])} last={(s[-1]['x'],s[-1]['y'])}")
        if a.shape == "circle":
            nom = 2 * a.r / px_cell
            print(f"circle nominal {2*a.r}px = {nom:.1f} cells; "
                  f"excess per side x {(bw-nom)/2:+.1f} y {(bh-nom)/2:+.1f} cells")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
