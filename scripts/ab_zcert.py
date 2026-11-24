#!/usr/bin/env python3
"""Certify the pad RECORDER end-to-end through agent-browser's CDP mouse.

Reloading the pad page: `agent-browser open <url>`, or `agent-browser reload`.

The input sequence below is the one proven to record: press, then ONE paced
`mouse move --duration D --steps N` per leg (duration/steps passed as separate
argv entries — folded into one string they become teleport coordinates and the
page sees no intermediate pointermove), then release.

Judged against the commanded polyline in CELL space: path length error and max
deviation of every recorded sample from the commanded path.

Exit 0 = certified (both tolerances met) / 1 = not certified.

Usage:
  ab_zcert.py                       # the Z: (20,20)(80,20)(20,80)(80,80)
  ab_zcert.py --cells x1,y1,...     # any polyline
  ab_zcert.py --dur 900 --steps 90
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
NCELL = 128


def ab(*args):
    return subprocess.run(AB + list(args), capture_output=True, text=True, timeout=180)


def js(expr):
    out = ab("eval", expr).stdout.strip().splitlines()
    s = out[-1] if out else '""'
    try:
        v = json.loads(s)
        return json.loads(v) if isinstance(v, str) else v
    except Exception:
        return None


def http(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read())


def seg_dist(p, a, b):
    (px, py), (ax, ay), (bx, by) = p, a, b
    dx, dy = bx - ax, by - ay
    L2 = dx * dx + dy * dy
    if L2 == 0:
        return math.hypot(px - ax, py - ay)
    t = max(0.0, min(1.0, ((px - ax) * dx + (py - ay) * dy) / L2))
    return math.hypot(px - (ax + t * dx), py - (ay + t * dy))


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--cells", default="20,20,80,20,20,80,80,80",
                    help="Z by default: two bars and the diagonal")
    ap.add_argument("--dur", type=int, default=900, help="ms per leg")
    ap.add_argument("--steps", type=int, default=90, help="CDP steps per leg")
    ap.add_argument("--tol", type=float, default=1.5, help="max deviation, cells")
    ap.add_argument("--len-tol", type=float, default=0.08, help="path-length error")
    a = ap.parse_args()

    nums = [float(v) for v in a.cells.split(",")]
    cells = [(nums[i], nums[i + 1]) for i in range(0, len(nums), 2)]

    geo = js("(()=>{const r=cv.getBoundingClientRect();"
             "return JSON.stringify({x:r.x,y:r.y,w:r.width,h:r.height})})()")
    k = geo["w"] / float(NCELL)                      # page px per cell
    P = lambda c: (geo["x"] + c[0] * k, geo["y"] + c[1] * k)
    pts = [P(c) for c in cells]

    http("POST", "/api/trace", [])
    time.sleep(0.25)
    pre = http("GET", "/api/trace")

    cmd = sum(math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1])
              for i in range(len(pts) - 1)) / k
    print(f"canvas {geo['x']:.0f},{geo['y']:.0f} {geo['w']:.0f}x{geo['h']:.0f}"
          f"  {k:.3f} px/cell  commanded {cmd:.1f} cells over {len(pts) - 1} legs")

    ab("mouse", "move", f"{pts[0][0]:.1f}", f"{pts[0][1]:.1f}")
    time.sleep(0.2)
    ab("mouse", "down", "left")
    time.sleep(0.15)
    for i in range(1, len(pts)):
        ab("mouse", "move", f"{pts[i][0]:.1f}", f"{pts[i][1]:.1f}",
           "--duration", str(a.dur), "--steps", str(a.steps))
    ab("mouse", "up", "left")
    time.sleep(0.7)

    tr = http("GET", "/api/trace")
    new = tr[len(pre):]
    print(f"strokes {len(pre)} -> {len(tr)}")
    if len(new) != 1:
        print(f"FAIL expected exactly one new stroke, got {len(new)}")
        return 1
    st = new[0]
    rp = [(p["x"] + p.get("fx", 0.0), p["y"] + p.get("fy", 0.0)) for p in st]
    rec = sum(math.hypot(rp[i + 1][0] - rp[i][0], rp[i + 1][1] - rp[i][1]) for i in range(len(rp) - 1))
    dev = max(min(seg_dist(q, pts[i], pts[i + 1]) for i in range(len(pts) - 1)) for q in rp)
    # deviation is measured in page px above; convert to cells
    dev_c = dev / k
    err = (rec - cmd) / cmd
    print(f"recorded {len(st)} pts  path {rec:.1f} cells ({rec / cmd * 100:.1f}% of {cmd:.1f})"
          f"  err {err * 100:+.2f}%")
    print(f"max deviation {dev_c:.2f} cells (tol {a.tol})")
    print("bbox x %.1f..%.1f y %.1f..%.1f  expected x %g..%g y %g..%g"
          % (min(q[0] for q in rp), max(q[0] for q in rp), min(q[1] for q in rp), max(q[1] for q in rp),
             min(c[0] for c in cells), max(c[0] for c in cells),
             min(c[1] for c in cells), max(c[1] for c in cells)))
    with open("/tmp/zcert-trace.json", "w") as f:
        json.dump(tr, f)
    ok = abs(err) <= a.len_tol and dev_c <= a.tol
    print("RESULT", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
