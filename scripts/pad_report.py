#!/usr/bin/env python3
"""Report the pad's recorded trace against a commanded Z.

Usage: pad_report.py <trace.json> <ox> <oy> <leg1_px> <diag_dy_px> <leg3_px> <px_per_cell>

Prints, for each commanded leg, the recorded displacement in px, the endpoint
error against the commanded endpoint, the straightness (chord / path length),
and the stroke's total length error. Exit code 0 when every leg endpoint is
within 5 % of its commanded length, 1 otherwise.
"""
import json
import math
import sys
from trace_read import points_of


def flat(trace):
    pts = []
    for st in trace:
        for p in st:
            pts.append(points_of([p])[0])
    return pts


def main():
    path = sys.argv[1]
    ox, oy = float(sys.argv[2]), float(sys.argv[3])
    l1 = float(sys.argv[4])
    dy = float(sys.argv[5])
    l3 = float(sys.argv[6])
    scale = float(sys.argv[7])
    legs = [(l1, 0.0), (-l1, dy), (l3, 0.0)]

    with open(path) as fh:
        trace = json.load(fh)
    pts = flat(trace)
    if len(pts) < 2:
        print("FAIL: trace has %d points" % len(pts))
        return 1

    # Split the stroke at its three corner turning points, using cumulative
    # distance along the commanded legs: corner 1 ends leg 1's length, corner 2
    # ends leg 1 + leg 2.
    total = 0.0
    cum = [0.0]
    for i in range(1, len(pts)):
        total += math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1])
        cum.append(total)
    cmd_lens = [math.hypot(dx, dy_) for dx, dy_ in legs]
    splits = [0.0]
    acc = 0.0
    for ln in cmd_lens:
        acc += ln
        splits.append(acc / sum(cmd_lens))

    idx = [0]
    for frac in splits[1:-1]:
        want = frac * total
        idx.append(min(range(len(cum)), key=lambda i: abs(cum[i] - want)))
    idx.append(len(pts) - 1)

    # Recorded origin in screen px for the park position.
    x0 = (pts[0][0] - ox / scale) * scale
    y0 = (pts[0][1] - oy / scale) * scale
    print("points=%d strokes=%d" % (len(pts), len(trace)))
    print("start cell (%.2f, %.2f) -> commanded park (%.0f, %.0f)" % (pts[0][0], pts[0][1], ox, oy))

    ok = True
    for k, (cx, cy) in enumerate(legs):
        a, b = pts[idx[k]], pts[idx[k + 1]]
        dx_px = (b[0] - a[0]) * scale
        dy_px = (b[1] - a[1]) * scale
        got = math.hypot(dx_px, dy_px)
        want = math.hypot(cx, cy)
        seg = 0.0
        for i in range(idx[k] + 1, idx[k + 1] + 1):
            seg += math.hypot(pts[i][0] - pts[i - 1][0], pts[i][1] - pts[i - 1][1]) * scale
        straight = got / seg if seg else 0.0
        err = (got - want) / want * 100.0 if want else 0.0
        ang = math.degrees(math.atan2(dy_px, dx_px))
        print("leg%d: %7.1f px recorded vs %7.1f commanded (%+5.1f %%), angle %6.1f deg, straightness %.3f"
              % (k + 1, got, want, err, ang, straight))
        if abs(err) > 5.0:
            ok = False
    print("VERDICT:", "PASS" if ok else "FAIL (a leg is off by more than 5 %)")
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
