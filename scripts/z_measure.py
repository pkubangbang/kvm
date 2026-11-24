#!/usr/bin/env python3
"""Draw the operator's Z on a TRULY empty pad and measure its sample density.

A clear that only POSTs [] does not hold: the page reposts its whole in-memory
record on every stroke, so old strokes come back. This script therefore:

  1. reloads the pad page (agent-browser), which starts an EMPTY in-memory record;
  2. POSTs [] to the server and re-reads GET until it is genuinely empty
     (up to a few tries — a stroke committing mid-clear can repopulate it);
  3. only then draws, and measures from the strokes NEWER than the draw start.

The anticipation is stated before firing and printed with the result:

    command     kvm mouse abs move X Y && kvm mouse rel down drag .. up
    legs        +300,0 / -300,+400 / +300,0  (1100 px == 183.3 cells at 6 px/cell)
    anticipate  183 samples for 100 % coverage; 60 for a once-per-frame sampler

Usage: z_measure.py [--start 1100,200] [--gap 0] [--reload/--no-reload]
"""
import argparse
import json
import math
import subprocess
import time
import urllib.request
from trace_read import px_per_cell

BASE = "http://127.0.0.1:8898"
LEGS = [(300, 0), (-300, 400), (300, 0)]
PX_PER_CELL = px_per_cell()   # measured, from pad_calib.json


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
    out = kvm("pos").stdout.split()
    return int(out[0]), int(out[1]) if len(out) > 1 else (None, None)


def reload_page():
    subprocess.run(["agent-browser", "eval", "location.reload(); 'ok'"],
                   capture_output=True, text=True, timeout=45)
    time.sleep(2.5)


def clear(timeout=4.0):
    """Empty the record for real; returns the stroke count actually left."""
    deadline = time.time() + timeout
    left = None
    while time.time() < deadline:
        http("POST", "/api/trace", [])
        time.sleep(0.25)
        left = len(http("GET", "/api/trace"))
        if left == 0:
            return 0
    return left


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--start", default="1100,200")
    ap.add_argument("--gap", type=int, default=0)
    ap.add_argument("--reload", dest="reload", action="store_true", default=True)
    ap.add_argument("--no-reload", dest="reload", action="store_false")
    a = ap.parse_args()
    sx, sy = (int(v) for v in a.start.split(","))

    path_px = sum(math.hypot(dx, dy) for dx, dy in LEGS)
    anticipated = round(path_px / PX_PER_CELL)

    print("=== plan (stated before firing) ===")
    print("  command   kvm mouse abs move %d %d && kvm mouse rel down %s up"
          % (sx, sy, " ".join("drag %d %d" % l for l in LEGS)))
    print("  legs      " + " / ".join("%+d,%+d" % l for l in LEGS))
    print("  path      %.0f px = %d cells at %.0f px/cell" % (path_px, anticipated, PX_PER_CELL))
    print("  anticipate %d samples for 100%% coverage (60 for a once-per-frame sampler)"
          % anticipated)
    print("  traces    one stroke, bbox ~50 x 67 cells, corners at press / +50,0 / +50,67 / +100,67")
    print("  coverage  points / %d" % anticipated)

    if a.reload:
        reload_page()
        print("  page      reloaded (empty in-memory record)")
    left = clear()
    if left:
        print("ABORT: record still holds %d stroke(s) after clear — not drawing on a dirty pad" % left)
        return 2
    print("  record    empty (verified by GET)")

    kvm("abs", "move", str(sx), str(sy))
    time.sleep(0.3)
    p0 = pos()
    t_draw = max((s[0]["ts"] for s in http("GET", "/api/trace") if s), default=0)

    args = []
    for dx, dy in LEGS:
        args += ["drag", str(dx), str(dy)]
    t0 = time.time()
    r = kvm("rel", "-gap", str(a.gap), "down", *args, "up")
    dt = time.time() - t0
    time.sleep(0.9)
    p1 = pos()

    print("=== measured ===")
    print("  draw      rc=%d %.2fs" % (r.returncode, dt))
    print("  wire      %s -> %s  delta=(%d,%d)" % (p0, p1, p1[0] - p0[0], p1[1] - p0[1]))

    rec = http("GET", "/api/trace")
    mine = [s for s in rec if s and s[0]["ts"] > t_draw]
    print("  record    %d stroke(s) total, %d from this draw" % (len(rec), len(mine)))
    if not mine:
        print("RESULT FAIL no ink recorded")
        return 1
    s = mine[-1]
    xs = [q["x"] + q.get("fx", 0) for q in s]
    ys = [q["y"] + q.get("fy", 0) for q in s]
    length = sum(math.hypot(xs[i] - xs[i - 1], ys[i] - ys[i - 1]) for i in range(1, len(s)))
    fx_n = sum(1 for q in s if "fx" in q)
    print("  samples   %d (with fx: %d)  span %d ms" % (len(s), fx_n, s[-1]["ts"] - s[0]["ts"]))
    print("  bbox      cells x %.1f..%.1f  y %.1f..%.1f"
          % (min(xs), max(xs), min(ys), max(ys)))
    print("  path      %.1f cells (anticipated %d)" % (length, anticipated))
    print("  COVERAGE  %.1f %%   LENGTH ERR %+.2f %%"
          % (len(s) / anticipated * 100, (length - anticipated) / anticipated * 100))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
