#!/usr/bin/env python3
"""Drive the pad canvas through agent-browser's CDP mouse (real page pointer
events) and judge the recorded stroke geometry.

The kvm binary injects at the compositor; on this session those events never
reach the pad page (page-side pointer counters stay 0). agent-browser mouse
goes through CDP Input.dispatchMouseEvent, which the page does receive, so it
is the only route that can certify the RECORDER.

Usage: cdp_draw.py [--gap MS] [--legs dx dy dx dy ...]
Writes the recorded trace to /tmp/cdp-trace.json and prints the leg report.
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


def sh(*args, check=False):
    return subprocess.run(AB + list(args), capture_output=True, text=True, timeout=60, check=check)


def post_clear():
    req = urllib.request.Request(BASE + "/api/trace", data=b"[]", method="POST",
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read())


def get_trace():
    with urllib.request.urlopen(BASE + "/api/trace", timeout=5) as r:
        return json.loads(r.read())


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--gap", type=float, default=12.0, help="ms between CDP moves")
    ap.add_argument("--step", type=float, default=3.0, help="px between CDP moves")
    ap.add_argument("--legs", type=float, nargs="+", default=[300, 0, -300, 400, 300, 0])
    ap.add_argument("--start", type=float, nargs=2, default=[120, 60],
                    help="page px; the canvas top is y=40 and only ~537 px of its "
                         "768 px height sit above the viewport bottom, so the "
                         "whole stroke must stay in page y 40..575")
    a = ap.parse_args()

    ev = sh("eval", "(()=>{window.__c={pm:0,pd:0,pu:0};"
                    "for(const t of ['pointermove','pointerdown','pointerup'])"
                    "window.addEventListener(t,()=>window.__c[t.slice(7)]++,true);"
                    "return 'armed'})()")
    print("arm:", ev.stdout.strip().splitlines()[-1][:80])
    print("clear:", post_clear())

    x, y = a.start
    sh("mouse", "move", str(x), str(y))
    time.sleep(0.15)
    sh("mouse", "down")
    pts = []
    legs = a.legs
    for i in range(0, len(legs), 2):
        dx, dy = legs[i], legs[i + 1]
        n = max(1, int(math.hypot(dx, dy) / a.step))
        for k in range(1, n + 1):
            pts.append((x + dx * k / n, y + dy * k / n))
    for px, py in pts:
        sh("mouse", "move", f"{px:.1f}", f"{py:.1f}")
        if a.gap:
            time.sleep(a.gap / 1000.0)
    sh("mouse", "up")
    time.sleep(0.5)

    c = sh("eval", "JSON.stringify(window.__c)").stdout.strip().splitlines()[-1]
    print("page counters:", c)

    tr = get_trace()
    with open("/tmp/cdp-trace.json", "w") as f:
        json.dump(tr, f)
    if not tr:
        print("NO STROKE RECORDED")
        return 1
    s = tr[-1]
    path = [(p["x"] + p.get("fx", 0.0), p["y"] + p.get("fy", 0.0)) for p in s]
    print(f"recorded points={len(s)}  commanded moves={len(pts)}")
    L = sum(math.hypot(path[i + 1][0] - path[i][0], path[i + 1][1] - path[i][1]) for i in range(len(path) - 1))
    cmd = sum(abs(v) for v in legs)
    print(f"recorded path {L:.1f} cells = {L * 6:.0f} px   commanded {cmd:.0f} px   ratio {L * 6 / cmd:.3f}")
    print(f"bbox x {min(p[0] for p in path):.1f}..{max(p[0] for p in path):.1f} "
          f"y {min(p[1] for p in path):.1f}..{max(p[1] for p in path):.1f}")
    return 0


if __name__ == "__main__":
    sys.exit(main())
