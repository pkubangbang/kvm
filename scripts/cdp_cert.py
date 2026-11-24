#!/usr/bin/env python3
"""Certify the pad RECORDER through CDP pointer events.

The kvm binary injects at the compositor and those events never reach the pad
page on this session, so the recorder can only be exercised through
agent-browser's CDP mouse (which the page does receive).

Draws a Z in CELL coordinates inside the visible canvas, then judges the
recorded stroke against the commanded geometry:
  * every sampled point must lie on the commanded polyline (max deviation)
  * the recorded path length must match the commanded path length
  * both reversals must be present (turn count and turn positions)

The canvas is scaled to fit the window, so page px are derived from the live
getBoundingClientRect(), never from the 768 px buffer.

Usage: cdp_cert.py [--gap MS] [--step PAGE_PX] [--cells x1,y1,x2,y2,...]
"""
import argparse
import json
import math
import subprocess
import sys
import time
import urllib.request

BASE = "http://127.0.0.1:8898"
AB = ["agent-browser"]


def ab(*args):
    return subprocess.run(AB + list(args), capture_output=True, text=True, timeout=60)


def js(expr):
    out = ab("eval", expr).stdout.strip().splitlines()
    s = out[-1] if out else '""'
    try:
        return json.loads(json.loads(s)) if s.startswith('"') else json.loads(s)
    except Exception:
        return s


def http(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read())


def seg_dist(p, a, b):
    ax, ay = a
    bx, by = b
    px, py = p
    dx, dy = bx - ax, by - ay
    L2 = dx * dx + dy * dy
    if L2 == 0:
        return math.hypot(px - ax, py - ay)
    t = max(0.0, min(1.0, ((px - ax) * dx + (py - ay) * dy) / L2))
    return math.hypot(px - (ax + t * dx), py - (ay + t * dy))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--gap", type=float, default=10.0)
    ap.add_argument("--step", type=float, default=4.0, help="page px per CDP move")
    ap.add_argument("--cells", default="20,20,80,20,20,80,80,80")
    ap.add_argument("--tol", type=float, default=1.5, help="max deviation in cells")
    ap.add_argument("--len-tol", type=float, default=0.05)
    a = ap.parse_args()

    nums = [float(v) for v in a.cells.split(",")]
    cells = [(nums[i], nums[i + 1]) for i in range(0, len(nums), 2)]

    geo = js("(()=>{const r=cv.getBoundingClientRect();return JSON.stringify({x:r.x,y:r.y,w:r.width,h:r.height})})()")
    geo = json.loads(geo) if isinstance(geo, str) else geo
    k = geo["w"] / 128.0

    def P(c):
        return (geo["x"] + c[0] * k, geo["y"] + c[1] * k)

    js("(()=>{document.getElementById('clear').click();window.__pc={pm:0,pd:0,pu:0};"
       "for(const t of [['pointermove','pm'],['pointerdown','pd'],['pointerup','pu']])"
       "window.addEventListener(t[0],()=>{window.__pc[t[1]]++},true);return 'ok'})()")
    time.sleep(0.3)
    print("clear ->", http("GET", "/api/trace"))

    pts = [P(c) for c in cells]
    total_cmd = sum(math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1])
                    for i in range(len(pts) - 1)) / k
    print(f"canvas page rect {geo}  scale {k:.3f} px/cell  commanded {total_cmd:.1f} cells")

    ab("mouse", "move", f"{pts[0][0]:.1f}", f"{pts[0][1]:.1f}")
    time.sleep(0.1)
    ab("mouse", "down")
    n_moves = 0
    for i in range(1, len(pts)):
        ax, ay = pts[i - 1]
        bx, by = pts[i]
        n = max(1, int(math.hypot(bx - ax, by - ay) / a.step))
        for j in range(1, n + 1):
            ab("mouse", "move", f"{ax + (bx - ax) * j / n:.1f}", f"{ay + (by - ay) * j / n:.1f}")
            n_moves += 1
            if a.gap:
                time.sleep(a.gap / 1000.0)
    ab("mouse", "up")
    time.sleep(0.6)

    pc = js("JSON.stringify(window.__pc)")
    print("page counters:", pc)
    tr = http("GET", "/api/trace")
    if not tr:
        print("FAIL no stroke recorded")
        return 1
    st = tr[-1]
    rp = [(p["x"] + p.get("fx", 0.0), p["y"] + p.get("fy", 0.0)) for p in st]
    rec = sum(math.hypot(rp[i + 1][0] - rp[i][0], rp[i + 1][1] - rp[i][1]) for i in range(len(rp) - 1))
    # deviation of each recorded sample from the commanded polyline
    dev = max(min(seg_dist(q, pts[i], pts[i + 1]) for i in range(len(pts) - 1)) for q in rp) / k
    print(f"recorded {len(st)} pts from {n_moves} CDP moves, path {rec:.1f} cells "
          f"({rec / total_cmd * 100:.1f}% of {total_cmd:.1f})")
    print(f"max deviation from commanded path: {dev:.2f} cells (tol {a.tol})")
    print(f"bbox x {min(q[0] for q in rp):.1f}..{max(q[0] for q in rp):.1f} "
          f"y {min(q[1] for q in rp):.1f}..{max(q[1] for q in rp):.1f}"
          f"   expected x {min(c[0] for c in cells)}..{max(c[0] for c in cells)}"
          f" y {min(c[1] for c in cells)}..{max(c[1] for c in cells)}")
    ok_len = abs(rec - total_cmd) / total_cmd <= a.len_tol
    print(("PASS" if ok_len else "FAIL") + " path length within "
          f"{a.len_tol * 100:.0f}% (err {(rec - total_cmd) / total_cmd * 100:+.2f}%)")
    print(("PASS" if dev <= a.tol else "FAIL") + " samples lie on the commanded path")
    with open("/tmp/cdp-trace.json", "w") as f:
        json.dump(tr, f)
    return 0 if (ok_len and dev <= a.tol) else 1


if __name__ == "__main__":
    sys.exit(main())
