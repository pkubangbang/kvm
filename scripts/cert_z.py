#!/usr/bin/env python3
"""Certify the Zibra-Z acceptance run — no screen reads.

The operator may be blind (no screen probe). Everything below is measured from
the two witnesses that do not need eyes:

  * the WIRE    — the cursor ring the compositor publishes (kvm mouse pos reads it)
  * the RECORD  — the pad page's own stroke record, POSTed to /api/trace on every
                  stroke end (the page commits each stroke as it finishes)

Procedure, one run:

  1. CLEAN: POST [] to /api/trace (server record) and re-read it to prove empty;
     report how many strokes the page still holds (the page's own counter is not
     resettable from outside, so a page-side Clear must be pressed by the
     operator when the counter is non-zero).
  2. PARK: `kvm mouse abs move` to the first corner, sample the ring until it is
     stable, require |error| <= parkTol.
  3. DRAW: ONE held span, reports <= maxStepPx, back to back:
       kvm mouse rel -gap 2 down drag .. drag .. drag .. up
  4. VERIFY (all must pass): park error, one new recorded stroke, the stroke's
     cell path matches the four corners, and the ring's endpoint is within
     endTol of the last corner.

Usage: cert_z.py [--ox OY ...] — the screen->cell map is measured separately and
passed in; the defaults are this session's measurement (screen = (1068,136) +
cell*6).
"""
import argparse
import json
import math
import struct
import subprocess
import sys
import time
import urllib.request

BASE = "http://127.0.0.1:8898"
RING = "/run/user/1000/kvm-cursor.ring"
N = 128
CORNERS_CELL = [(5, 11), (86, 11), (5, 94), (86, 94)]


def http(method, path, body=None):
    data = None if body is None else json.dumps(body).encode()
    req = urllib.request.Request(BASE + path, data=data, method=method,
                                 headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=5) as r:
        return json.loads(r.read())


def ring_newest():
    with open(RING, "rb") as f:
        d = f.read()
    best = None
    for i in range((len(d) - 64) // 24):
        seq, x, y, t = struct.unpack_from("<IiiQ", d, 64 + i * 24)
        if t and (best is None or t > best[0]):
            best = (t, x, y)
    return (best[1], best[2]) if best else (None, None)


def ring_stable(timeout=0.6, quiet=0.16):
    """Sample the ring until two consecutive readings agree for `quiet`."""
    t0 = time.time()
    prev = ring_newest()
    last_change = time.time()
    while time.time() - t0 < timeout:
        time.sleep(0.01)
        cur = ring_newest()
        if cur != prev:
            prev, last_change = cur, time.time()
        elif time.time() - last_change >= quiet:
            return prev
    return prev


def kvm(*args, timeout=30):
    return subprocess.run(["./kvm", "mouse", *args], capture_output=True,
                          text=True, timeout=timeout)


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--ox", type=int, default=1068)
    ap.add_argument("--oy", type=int, default=136)
    ap.add_argument("--scale", type=int, default=6)
    ap.add_argument("--gap", type=int, default=2)
    ap.add_argument("--park-tol", type=int, default=6)
    ap.add_argument("--end-tol", type=int, default=10)
    ap.add_argument("--cell-tol", type=float, default=5.0)
    ap.add_argument("--cap", type=float, default=5.0)
    a = ap.parse_args()

    def S(c):
        return a.ox + c[0] * a.scale, a.oy + c[1] * a.scale

    fails = []

    def verdict(ok, name, detail=""):
        print(("PASS " if ok else "FAIL ") + name + ((" | " + detail) if detail else ""))
        if not ok:
            fails.append(name)

    # 1. clean the server record
    http("POST", "/api/trace", [])
    time.sleep(0.2)
    pre = http("GET", "/api/trace")
    verdict(len(pre) == 0, "server record empty before the run", f"{len(pre)} strokes")
    print("NOTE page-side record cannot be inspected from here; if the operator "
          "reports a non-zero page counter, press the page's Clear first "
          "(it empties both page and server).")

    # 2. park
    t0 = time.time()
    ax, ay = S(CORNERS_CELL[0])
    r = kvm("abs", "move", str(ax), str(ay))
    p = ring_stable()
    perr = (p[0] - ax, p[1] - ay)
    print(f"park: cmd=({ax},{ay}) rc={r.returncode} ring={p} err={perr} {time.time()-t0:.2f}s")
    verdict(abs(perr[0]) <= a.park_tol and abs(perr[1]) <= a.park_tol,
            "park within tolerance", f"|err|=({abs(perr[0])},{abs(perr[1])}) tol={a.park_tol}")
    if fails:
        print("RESULT FAIL (do not press on an unconfirmed park)")
        return 1

    # 3. one held span through the corners
    pts = [S(c) for c in CORNERS_CELL]
    legs = []
    for i in range(1, len(pts)):
        legs += ["drag", str(pts[i][0] - pts[i - 1][0]), str(pts[i][1] - pts[i - 1][1])]
    t1 = time.time()
    r = kvm("rel", "-gap", str(a.gap), "down", *legs, "up")
    end = ring_stable()
    draw_t = time.time() - t1
    total = time.time() - t0
    eerr = (end[0] - pts[-1][0], end[1] - pts[-1][1])
    print(f"draw: rc={r.returncode} {draw_t:.2f}s ring_end={end} err_from_last_corner={eerr} "
          f"{r.stderr.strip()[:140]}")
    print(f"total move+draw {total:.2f}s (cap {a.cap})")
    verdict(total <= a.cap, "under the time cap", f"{total:.2f}s <= {a.cap}s")
    verdict(abs(eerr[0]) <= a.end_tol and abs(eerr[1]) <= a.end_tol,
            "wire endpoint at the last corner", f"|err|=({abs(eerr[0])},{abs(eerr[1])}) tol={a.end_tol}")

    # 4. the recorded stroke
    time.sleep(0.5)
    post = http("GET", "/api/trace")
    new = post[len(pre):]
    verdict(len(new) == 1, "exactly one new recorded stroke", f"{len(pre)} -> {len(post)}")
    if new:
        s = new[0]
        xs = [q["x"] for q in s]
        ys = [q["y"] for q in s]
        print(f"stroke: {len(s)} pts bbox x {min(xs)}-{max(xs)} y {min(ys)}-{max(ys)} "
              f"first={s[0]} last={s[-1]}")
        for name, c in zip("ABCD", CORNERS_CELL):
            d = min(math.hypot(q["x"] - c[0], q["y"] - c[1]) for q in s)
            print(f"  nearest to {name}{c}: {d:.1f} cells")
            verdict(d <= a.cell_tol, f"stroke reaches {name}{c}", f"{d:.1f} cells tol {a.cell_tol}")
        top = [q for q in s if q["x"] > 60 and abs(q["y"] - 11) <= 3]
        bot = [q for q in s if q["x"] > 60 and abs(q["y"] - 94) <= 3]
        diag = [q for q in s if 25 < q["x"] < 66 and 30 < q["y"] < 80]
        print(f"  shape: top-bar={len(top)} bottom-bar={len(bot)} diagonal={len(diag)}")
        verdict(bool(top and bot and diag), "Z shape present (top bar, diagonal, bottom bar)")

    print("RESULT", "PASS" if not fails else "FAIL " + ", ".join(fails))
    return 0 if not fails else 1


if __name__ == "__main__":
    sys.exit(main())
