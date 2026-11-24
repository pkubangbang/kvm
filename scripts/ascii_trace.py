#!/usr/bin/env python3
"""Render a recorded pad stroke as ASCII, with the segments drawn in.

The record only stores one sample per CELL, so a raw scatter of its points
under-represents the ink: the diagonal of a Z is 4 points. The page draws the
path it *thinks* it drew by joining consecutive samples with lines, so the
faithful rendering interpolates between samples and draws that. What this shows
is the polyline geometry the pad committed — not the continuous pointer path,
which only a finer sampler can capture.

Usage: ascii_trace.py [--stroke -1] [--cols 44]
"""
import argparse
import json
import urllib.request
from trace_read import read_trace, points_of
import os

BASE = "http://127.0.0.1:8898"


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--stroke", type=int, default=-1, help="index, -1 = newest")
    ap.add_argument("--cols", type=int, default=44, help="characters across")
    ap.add_argument("--base", default=BASE)
    a = ap.parse_args()

    rec = read_trace(os.environ.get("KVM_PAD_OUT"))
    if not rec:
        print("record is empty")
        return 1
    s = rec[a.stroke]
    xs = [q["x"] for q in s]
    ys = [q["y"] for q in s]
    x0, x1, y0, y1 = min(xs), max(xs), min(ys), max(ys)
    span = max(x1 - x0, 1)
    # one character covers `step` cells; keep the aspect roughly square
    step = max(1, round(span / a.cols))
    W = (x1 - x0) // step + 1
    H = (y1 - y0) // step + 1
    g = [[" "] * W for _ in range(H)]

    def put(cx, cy):
        col = int(round((cx - x0) / step))
        row = int(round((cy - y0) / step))
        if 0 <= row < H and 0 <= col < W:
            g[row][col] = "#"

    prev = None
    for q in s:
        if prev is not None:
            px, py = prev
            n = max(abs(q["x"] - px), abs(q["y"] - py)) * 4
            for i in range(n + 1):
                put(px + (q["x"] - px) * i / n, py + (q["y"] - py) * i / n)
        prev = (q["x"], q["y"])
    # mark the sample points so quantisation is visible
    for q in s:
        col = int(round((q["x"] - x0) / step))
        row = int(round((q["y"] - y0) / step))
        if 0 <= row < H and 0 <= col < W and g[row][col] == "#":
            pass

    print(f"stroke {a.stroke if a.stroke >= 0 else len(rec) + a.stroke}: "
          f"{len(s)} samples, {x1-x0+1}x{y1-y0+1} cells, "
          f"1 char = {step} cell(s)")
    print("+" + "-" * W + "+")
    for row in g:
        print("|" + "".join(row) + "|")
    print("+" + "-" * W + "+")
    print("samples:", [(q["x"], q["y"]) for q in s])
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
