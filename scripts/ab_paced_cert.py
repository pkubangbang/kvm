#!/usr/bin/env python3
"""Certify the pad recorder using agent-browser's paced mouse path.

`agent-browser mouse move X Y --duration D --steps N` generates a real pointer
path (each step is a dispatched move), unlike plain `mouse move` which teleports.
One call per Z leg therefore gives the page a continuous pointermove stream.

Judges the recorded stroke against the commanded Z in CELL space:
  * path length within --len-tol of the commanded polyline
  * every sample within --tol cells of the commanded polyline

Usage: ab_paced_cert.py [--dur MS] [--steps N] [--cells x1,y1,...]
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
    return subprocess.run(AB + list(args), capture_output=True, text=True, timeout=120)


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
    ap.add_argument("--dur", type=int, default=1200, help="ms per leg")
    ap.add_argument("--steps", type=int, default=120)
    ap.add_argument("--cells", default="20,20,80,20,20,80,80,80")
    ap.add_argument("--tol", type=float, default=1.5)
    ap.add_argument("--len-tol", type=float, default=0.05)
    a = ap.parse_args()

    nums = [float(v) for v in a.cells.split(",")]
    cells = [(nums[i], nums[i + 1]) for i in range(0, len(nums), 2)]

    geo = js("(()=>{const r=cv.getBoundingClientRect();return JSON.stringify({x:r.x,y:r.y,w:r.width,h:r.height})})()")
    k = geo["w"] / 128.0

    def P(c):
        return (geo["x"] + c[0] * k, geo["y"] + c[1] * k)

    js("document.getElementById('clear').click(); 'ok'")
    time.sleep(0.3)
    print("clear ->", http("GET", "/api/trace"))

    pts = [P(c) for c in cells]
    cmd = sum(math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1])
              for i in range(len(pts) - 1)) / k
    print(f"canvas {geo}  {k:.3f} px/cell  commanded {cmd:.1f} cells "
          f"({a.dur} ms/leg, {a.steps} steps/leg)")

    ab("mouse", "move", f"{pts[0][0]:.1f}", f"{pts[0][1]:.1f}")
    time.sleep(0.1)
    ab("mouse", "down", "left")
    for i in range(1, len(pts)):
        subprocess.run(AB + ["mouse", "move", f"{pts[i][0]:.1f}", f"{pts[i][1]:.1f}",
                             "--duration", str(a.dur), "--steps", str(a.steps)],
                       capture_output=True, text=True, timeout=120)
    ab("mouse", "up", "left")
    time.sleep(0.6)

    tr = http("GET", "/api/trace")
    if not tr:
        print("FAIL no stroke recorded")
        return 1
    st = tr[-1]
    rp = [(p["x"] + p.get("fx", 0.0), p["y"] + p.get("fy", 0.0)) for p in st]
    rec = sum(math.hypot(rp[i + 1][0] - rp[i][0], rp[i + 1][1] - rp[i][1]) for i in range(len(rp) - 1))
    dev = max(min(seg_dist(q, pts[i], pts[i + 1]) for i in range(len(pts) - 1)) for q in rp) / k
    print(f"recorded {len(st)} pts, path {rec:.1f} cells ({rec / cmd * 100:.1f}% of {cmd:.1f})")
    print(f"max deviation {dev:.2f} cells (tol {a.tol})")
    with open("/tmp/ab-trace.json", "w") as f:
        json.dump(tr, f)
    err = (rec - cmd) / cmd
    ok_len = abs(err) <= a.len_tol
    ok_dev = dev <= a.tol
    print(("PASS" if ok_len else "FAIL") + f" path length err {err * 100:+.2f}%")
    print(("PASS" if ok_dev else "FAIL") + " samples on the commanded path")
    return 0 if (ok_len and ok_dev) else 1


if __name__ == "__main__":
    sys.exit(main())
