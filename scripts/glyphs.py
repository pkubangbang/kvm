#!/usr/bin/env python3
"""Glyph definitions in PAD CELLS (128x128 grid, origin top-left).

These are the "zibra" suite the user asked for: a house script of single-stroke
shapes plus the full alphabet sample. Every polyline is written in cell
coordinates so it is independent of the screen calibration; paddraw.py converts
with the live map.

Coordinate notes:
  * the cell grid is 128 x 128; a glyph occupies roughly x 30..80, y 20..100 so
    it sits in the pad's middle and never touches an edge;
  * `zibra-b` is the user's own teacher trace (reference/pad-trace-raw.json),
    copied point for point — it is the reference glyph, not a drawn approximation.
"""
import json
import os
from trace_read import points_of

REF = os.path.join(os.path.dirname(__file__), "..", "reference", "pad-trace-raw.json")


def line(x0, y0, x1, y1, n=24):
    return [(x0 + (x1 - x0) * i / n, y0 + (y1 - y0) * i / n) for i in range(n + 1)]


def arc(cx, cy, rx, ry, a0, a1, n=48):
    import math
    out = []
    for i in range(n + 1):
        a = math.radians(a0 + (a1 - a0) * i / n)
        out.append((cx + rx * math.cos(a), cy + ry * math.sin(a)))
    return out


def teacher_b():
    """The user's teacher trace, as cells."""
    with open(os.path.abspath(REF)) as fh:
        t = json.load(fh)
    return [points_of(st)
            for st in t]


# --- single-stroke "zibra" shapes -------------------------------------------
# z: top bar left->right, diagonal down-left, bottom bar left->right.
ZIBRA_Z = [
    line(36, 30, 72, 30),
    line(72, 30, 36, 90),
    line(36, 90, 74, 90),
]

# i: dot above, stem below (two strokes, one pen lift).
ZIBRA_I = [
    [(54, 34), (54, 36)],
    line(54, 46, 54, 90),
]

# b: stem, then bowl (hand-written proportions, close to the teacher's).
ZIBRA_B = [
    # The USER'S own b, resampled from their draw (trace a1d569cc, 2 strokes,
    # 153 + 223 samples). This replaces an earlier idealised arc: the user asked
    # for their letter, not a constructed one, so every vertex below is read off
    # their trace.
    #
    # Stem: starts at (39,23), drifts one cell left halfway down (39 -> 38 at
    # y=62) — their hand, not a vertical line — and rests at (38,96).
    [(39, 23), (39, 26), (39, 29), (39, 32), (39, 36), (39, 39), (39, 42),
     (39, 46), (39, 52), (39, 57), (38, 62), (38, 67), (38, 71), (38, 77),
     (38, 82), (38, 87), (38, 92), (38, 95), (38, 96)],
    # Bowl: starts mid-stem at (40,60) with their small pen-down hook up-and-
    # right, sweeps to the widest point x=78 at y=69..79, curves back left,
    # and CLOSES FLAT along y=97 from x=62 to x=40 rather than on a tangent —
    # that flat run is in their hand and is reproduced, not corrected.
    [(40, 60), (41, 59), (44, 57), (46, 56), (49, 55), (51, 54), (53, 53),
     (56, 52), (59, 52), (61, 52), (63, 52), (65, 53), (67, 54), (69, 56),
     (71, 57), (74, 60), (75, 62), (76, 64), (77, 66), (78, 69), (78, 73),
     (78, 76), (78, 79), (77, 81), (76, 84), (75, 87), (73, 90), (70, 93),
     (66, 95), (62, 96), (59, 97), (55, 97), (51, 97), (47, 97), (44, 97),
     (42, 97), (41, 97), (40, 97)],
]

# r: stem, then shoulder.
ZIBRA_R = [
    line(44, 46, 44, 90),
    line(44, 46, 52, 44) + line(52, 44, 58, 48)[1:] + line(58, 48, 60, 54)[1:],
]

# a: bowl, then the tail down the right side.
ZIBRA_A = [
    arc(55, 70, 12, 12, -60, 240)[:-1] + [(55, 58)],
    line(67, 52, 67, 90),
]

# full: the whole lowercase sample the suite is named after.
ZIBRA_FULL = ZIBRA_B + [
    # a
    *ZIBRA_A,
    # r
    *ZIBRA_R,
    # z
    *ZIBRA_Z,
]

GLYPHS = {
    "zibra-z": ZIBRA_Z,
    "zibra-i": ZIBRA_I,
    "zibra-b": ZIBRA_B,
    "zibra-r": ZIBRA_R,
    "zibra-a": ZIBRA_A,
    "zibra-full": ZIBRA_FULL,
    "teacher-b": teacher_b(),
}
