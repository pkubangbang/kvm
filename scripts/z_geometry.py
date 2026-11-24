#!/usr/bin/env python3
"""Judge the padded Z by GEOMETRY, not by an assumed px/cell.

Reads /api/trace, decomposes the last stroke into legs by turning points,
then derives the px-per-cell scale from the commanded path length
(1100 px = 300 + 400 + 300) and compares leg ratios against 300:400:300.
"""
import json
import sys
import urllib.request

API = "http://127.0.0.1:8898/api/trace"


def load():
    return json.load(urllib.request.urlopen(API))


def pts(stroke):
    return [(p["x"] + p.get("fx", 0.0), p["y"] + p.get("fy", 0.0)) for p in stroke]


def turns(path, min_turn=40.0):
    """Split the polyline into straight legs at direction changes > min_turn deg."""
    import math
    legs = []
    start = 0
    cur = None
    for i in range(1, len(path) - 1):
        ax = path[i][0] - path[start][0]
        ay = path[i][1] - path[start][1]
        bx = path[i + 1][0] - path[i][0]
        by = path[i + 1][1] - path[i][1]
        na = math.hypot(ax, ay)
        nb = math.hypot(bx, by)
        if na < 3 or nb < 0.5:
            continue
        ang = math.degrees(math.acos(max(-1.0, min(1.0, (ax * bx + ay * by) / (na * nb)))))
        if ang > min_turn:
            legs.append((start, i))
            start = i
    legs.append((start, len(path) - 1))
    out = []
    for a, b in legs:
        if b <= a:
            continue
        L = sum(
            ((path[j + 1][0] - path[j][0]) ** 2 + (path[j + 1][1] - path[j][1]) ** 2) ** 0.5
            for j in range(a, b)
        )
        out.append({"from": a, "to": b, "len_cells": L})
    return out


def main():
    strokes = load()
    if not strokes:
        print("NO STROKES")
        return 1
    st = strokes[-1]
    path = pts(st)
    legs = turns(path)
    total = sum(l["len_cells"] for l in legs)
    print(f"strokes={len(strokes)} points={len(st)}")
    print(f"bbox x {min(p[0] for p in path):.1f}..{max(p[0] for p in path):.1f}"
          f"  y {min(p[1] for p in path):.1f}..{max(p[1] for p in path):.1f}")
    print(f"path length = {total:.2f} cells")
    for i, l in enumerate(legs):
        print(f"  leg{i + 1}: idx {l['from']:>3}..{l['to']:<3} len {l['len_cells']:7.2f} cells"
              f"   ({l['len_cells'] / total * 100:5.1f}% of path)")
    # commanded: leg1 300 px, leg2 400 px (diag 500? no: 300 wide,400 tall => 500)
    # commanded straight-line legs: 300, sqrt(300^2+400^2)=500, 300 => 1100 px total
    commanded = [300.0, 500.0, 300.0]
    if len(legs) >= 3:
        scale = 1100.0 / total  # px per cell
        print(f"derived scale = {scale:.2f} px/cell (assumed 6.00)")
        print("leg px recorded vs commanded, ratio:")
        for i, cmd in enumerate(commanded):
            got = legs[i]["len_cells"] * scale
            print(f"  leg{i + 1}: {got:7.1f} vs {cmd:6.1f} px  ratio {got / cmd:5.3f}")
        shape = [legs[i]["len_cells"] / legs[0]["len_cells"] for i in range(3)]
        print(f"shape ratios leg/leg1 = [{shape[0]:.3f}, {shape[1]:.3f}, {shape[2]:.3f}]"
              f"  expected [1.000, 1.667, 1.000]")
    return 0


if __name__ == "__main__":
    sys.exit(main())
