#!/usr/bin/env python3
"""Judge the TURNS of a handwritten stroke — the thing that makes a glyph look
written rather than plotted.

A turn is not "any direction change". On a quantised pad, a slow straight run
zig-zags ±22-45° every sample and a flat run flips ±180°; counting those makes
every straight line look like a polygon. A real turn has three properties the
user's own trace shows:

  1. SUSTAINED — the heading keeps going the same way for several samples (a
     signed turn accumulated over a window, not a per-sample difference);
  2. SLOW ENOUGH TO SEE — the hand dwells there: the samples covering the turn
     take longer than the surrounding motion (curvature costs time);
  3. SUB-CELL — it does not sit on a lattice crossing: the ink carries sub-cell
     offsets (fx, fy), so a genuine curve visits the same cell twice from
     different sides.

usage: padturns.py <trace.json> [stroke-index]
Prints, per stroke: net signed turn, total |turn|, the turn list, and a verdict
on whether the stroke's turns look like handwriting (a few sustained turns) or
like a polyline plot (many equal corners) or like quantisation noise (many ±180°
flips that carry no time).
"""
import json
import math
import os
import sys

from trace_read import read_trace, points_of

MIN_SUSTAIN_DEG = 15.0   # a turn must accumulate this much inside the window
WINDOW = 4               # samples either side of a turn's centre
SLOW_FACTOR = 1.2        # the turn must take >= 1.2x the median ms per sample
MIN_SPAN_PX = 3.0        # below this the samples are cell quantisation, not ink


def load(path=None):
    """A trace file, or (path None) the pad's newest client trace."""
    if path and os.path.isdir(path):
        return read_trace(path)
    if path:
        with open(path) as fh:
            return json.load(fh)
    return read_trace(os.environ.get("KVM_PAD_OUT"))


def pts(stroke):
    return [(x, y, (p.get("ts", 0) if isinstance(p, dict) else 0))
            for (x, y), p in zip(points_of(stroke), stroke)]


def heading(a, b):
    return math.atan2(b[1] - a[1], b[0] - a[0])


def norm(deg):
    while deg > 180:
        deg -= 360
    while deg < -180:
        deg += 360
    return deg


def seg_px(a, b, px=6.0):
    return math.hypot(b[0] - a[0], b[1] - a[1]) * px


def analyse(stroke, px=6.0):
    P = pts(stroke)
    n = len(P)
    if n < 3 * WINDOW:
        return None
    dts = [P[i + 1][2] - P[i][2] for i in range(n - 1)]
    dts = [d for d in dts if d > 0]
    med_dt = sorted(dts)[len(dts) // 2] if dts else 16.0

    length = sum(seg_px(P[i], P[i + 1], px) for i in range(n - 1))
    turns = []
    total = 0.0
    net = 0.0
    for c in range(WINDOW, n - WINDOW):
        a, b = P[c - WINDOW], P[c + WINDOW]
        span = math.hypot(b[0] - a[0], b[1] - a[1]) * px
        if span < MIN_SPAN_PX:
            continue
        # signed turn accumulated across the window, sample by sample
        acc = 0.0
        for i in range(c - WINDOW, c + WINDOW - 1):
            h1 = heading(P[i], P[i + 1])
            h2 = heading(P[i + 1], P[i + 2])
            d = norm(math.degrees(h2 - h1))
            # A ~180 deg flip between two ADJACENT TINY samples is quantisation
            # jitter on a straight run, not a turn. Testing the MAGNITUDE alone
            # (>90) also deleted every real corner, which is the one feature this
            # script exists to measure: a 90 deg corner is exactly 90. So the test
            # is "reversal AND the two legs are too short to be ink".
            if abs(d) > 150 and seg_px(P[i], P[i + 1]) < MIN_SPAN_PX \
                    and seg_px(P[i + 1], P[i + 2]) < MIN_SPAN_PX:
                d = 0.0
            acc += d
        total += abs(acc)
        net += acc
        if abs(acc) >= MIN_SUSTAIN_DEG:
            dt = P[c + WINDOW][2] - P[c - WINDOW][2]
            turns.append({"i": c, "x": round(P[c][0], 1), "y": round(P[c][1], 1),
                          "deg": round(acc, 1), "ms": dt,
                          "slow": dt >= SLOW_FACTOR * med_dt * 2 * WINDOW})
    # A raw sum of |turn| grows with the number of samples, so two drawings of the
    # same glyph at different pacing could never be compared. Normalise by the
    # path length in cells: deg per cell is a property of the SHAPE.
    per_cell = (total / length) if length > 0.5 else 0.0
    return {"n": n, "median_dt": med_dt, "net": net, "total": total,
            "per_cell": per_cell, "length_cells": length / px, "turns": turns}


def main():
    args = [a for a in sys.argv[1:] if not a.startswith("-")]
    trace = load(args[0] if args else None)
    only = int(args[1]) if len(args) > 1 else None
    if not trace:
        print("no strokes in the trace (nothing drawn, or the pad cleared it)")
        return 2
    worst = 0
    for i, s in enumerate(trace):
        if only is not None and i != only:
            continue
        r = analyse(s)
        if r is None:
            print("stroke %d: too few samples" % i)
            worst = max(worst, 2)
            continue
        slow = sum(1 for t in r["turns"] if t["slow"])
        print("stroke %d: %d samples, median %.0f ms/sample, net %+.0f deg, "
              "total |turn| %.0f deg (%.1f deg/cell over %.1f cells)"
              % (i, r["n"], r["median_dt"], r["net"], r["total"],
                 r["per_cell"], r["length_cells"]))
        for t in r["turns"][:12]:
            print("   turn at (%5.1f,%5.1f) idx %3d: %+6.1f deg over %3d ms%s"
                  % (t["x"], t["y"], t["i"], t["deg"], t["ms"],
                     "  <- dwells" if t["slow"] else ""))
        if len(r["turns"]) > 12:
            print("   ... %d more" % (len(r["turns"]) - 12))
        # verdict: handwriting vs plot vs noise
        sustained = r["turns"]
        if not sustained:
            v = "straight: no sustained turn (handwriting may have none here)"
        elif slow >= max(1, len(sustained) // 2):
            v = "HANDWRITING: %d sustained turns, %d of them dwell in time" \
                % (len(sustained), slow)
        elif abs(r["net"]) > 30:
            v = "CURVED BUT FAST: %d turns, none slow — drawn at constant speed" \
                % len(sustained)
        else:
            v = "PLOT-LIKE: %d equal corners, no dwell — this is a polyline" \
                % len(sustained)
            worst = max(worst, 1)
        print("   -> " + v)
    # A verdict is a RESULT, so a PLOT-LIKE stroke must not exit 0: a wrapper that
    # only checks the exit code would call a polyline a pass.
    return worst


if __name__ == "__main__":
    sys.exit(main())
