#!/usr/bin/env python3
"""Drive the pad canvas with agent-browser's CDP mouse and judge the record.

Why this route: kvm injects at the compositor and those events do not reach the
pad page on this session, so the RECORDER can only be exercised through CDP.
The page's own bug (pointerdown never set the `down` flag, so no sample was
ever accepted) is fixed in cmd/kvm-inputpad/main.go; this script certifies the
result.

Draw mode (proven): one `mouse move --duration D --steps N` per leg, issued
with --duration/--steps passed as separate argv entries — plain `mouse move`
teleports and sends no intermediate pointermove.

Usage:
  ab_cert.py [path]           replay a recorded input path (see --record)
  ab_cert.py --record FILE    record the next gesture's moves into FILE
  ab_cert.py --cells 20,20,80,20,20,80,80,80   draw a polyline of cells

With no path, every paced move goes through `agent-browser mouse move ...` and
the script measures the recorded stroke against the commanded polyline.
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


def http_get():
    with urllib.request.urlopen(BASE + "/api/trace", timeout=5) as r:
        return json.loads(r.read())


def http_clear():
    req = urllib.request.Request(BASE + "/api/trace", data=b"[]", method="POST",
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


def canvas_rect():
    return js("(()=>{const r=cv.getBoundingClientRect();"
              "return JSON.stringify({x:r.x,y:r.y,w:r.width,h:r.height})})()")


def draw_page_pts(points, dur, steps):
    """One paced move per point, then release. points are PAGE pixels.

    The move is issued with --duration/--steps as separate argv entries. Passing
    them inside one string makes agent-browser read the numbers as coordinates
    of a teleporting move, which sends no intermediate pointermove and records
    only the press (the failure this script hit before).
    """
    ab("mouse", "move", f"{points[0][0]:.1f}", f"{points[0][1]:.1f}")
    time.sleep(0.15)
    ab("mouse", "down", "left")
    time.sleep(0.1)
    for x, y in points[1:]:
        ab("mouse", "move", f"{x:.1f}", f"{y:.1f}",
           "--duration", str(dur), "--steps", str(steps))
    ab("mouse", "up", "left")
    time.sleep(0.6)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("path", nargs="?", help="JSON list of page-px points to replay")
    ap.add_argument("--cells", help="comma list x1,y1,x2,y2,... to draw")
    ap.add_argument("--dur", type=int, default=700, help="ms per move")
    ap.add_argument("--steps", type=int, default=60, help="CDP steps per move")
    ap.add_argument("--tol", type=float, default=1.5)
    ap.add_argument("--len-tol", type=float, default=0.05)
    a = ap.parse_args()

    geo = canvas_rect()
    k = geo["w"] / 128.0

    if a.path:
        raw = [tuple(p) for p in json.load(open(a.path))]
        pts = [tuple(p) for p in raw]
        print(f"replaying {len(pts)} page-px points")
    elif a.cells:
        nums = [float(v) for v in a.cells.split(",")]
        cells = [(nums[i], nums[i + 1]) for i in range(0, len(nums), 2)]
        pts = [(geo["x"] + c[0] * k, geo["y"] + c[1] * k) for c in cells]
    else:
        print("need a path file or --cells")
        return 2

    http_clear()
    print("clear ok; canvas", geo, f"{k:.3f} px/cell")

    json.dump([[round(p[0], 2), round(p[1], 2)] for p in pts], open("/tmp/last-cmd-path.json", "w"))
    draw_page_pts(pts, a.dur, a.steps)

    st_all = http_get()
    if not st_all:
        print("FAIL no stroke recorded")
        return 1
    st = st_all[-1]
    cmd = sum(math.hypot(pts[i + 1][0] - pts[i][0], pts[i + 1][1] - pts[i][1])
              for i in range(len(pts) - 1)) / k
    rp = [(p["x"] + p.get("fx", 0.0), p["y"] + p.get("fy", 0.0)) for p in st]
    rec = sum(math.hypot(rp[i + 1][0] - rp[i][0], rp[i + 1][1] - rp[i][1]) for i in range(len(rp) - 1))
    # every recorded sample against every commanded SEGMENT (page px)
    dev = 0.0
    if len(pts) > 1:
        dev = max(min(seg_dist((q[0] * k + geo["x"], q[1] * k + geo["y"]), pts[i], pts[i + 1])
                      for i in range(len(pts) - 1)) for q in rp) / k
    print(f"recorded {len(st)} pts; path {rec:.1f} cells vs commanded {cmd:.1f} "
          f"(err {(rec - cmd) / cmd * 100:+.2f}%)")
    print(f"max deviation {dev:.2f} cells (tol {a.tol})")
    ok = abs(rec - cmd) / cmd <= a.len_tol and dev <= a.tol
    print("RESULT", "PASS" if ok else "FAIL")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
