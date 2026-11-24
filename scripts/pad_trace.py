#!/usr/bin/env python3
"""Draw the teacher's b point by point, through a linear cells->screen map.

The user's recipe:
  * cell -> screen is LINEAR (x' = kx*x + bx, y' = ky*y + by), measured on
    screen and kept in scripts/pad_calib.json;
  * EVERY point of the teacher's trace is a drag target, in time order;
  * one teacher point = one report (`drag dx dy`), the delta being measured from
    the previous emitted screen point.

No /api/trace: the pad's canvas is the evidence, read with the screen tool, and
a draw is cleared with the pad's Clear button (the only thing that repaints an
empty canvas — a server-side clear leaves the page's ink on screen).
"""
import json
import math
import subprocess
import sys
from trace_read import points_of

KVM = "./kvm"
TEACHER = "reference/pad-trace-raw.json"
CALIB = "scripts/pad_calib.json"
CLEAR_BTN = (1825, 142)
# Longest single HID report, in screen px. The host's gain holds only inside a
# report-size band; a teacher segment sent as one long report over-travels, so
# each segment is cut into reports of at most this size.
REPORT_PX = 2
# Milliseconds to wait after each report. The page samples the pointer once per
# display frame (~16.7 ms), so a stroke sent in a few milliseconds records only a
# handful of points — a 96-cell bowl came back as 72 cells because its right-hand
# sweep passed inside two frames. 20 ms per report keeps every report on its own
# frame and puts the stroke on a human timescale (the teacher's bowl took 2.5 s).
REPORT_WAIT_MS = 20
# Per-stroke compensation for the pad's delivered-vs-commanded size, applied
# about the stroke's own start point (so a stroke still STARTS on the teacher's
# first cell). 1.0 = command the teacher's own points unchanged.
GAIN = {"stem": 1.0, "bowl": 1.0}


def sh(args, timeout=180):
    return subprocess.run([KVM, "mouse"] + args, capture_output=True, text=True,
                          timeout=timeout)


def pos():
    w = sh(["pos"]).stdout.split()
    if len(w) != 2:
        return None
    try:
        return int(w[0]), int(w[1])
    except ValueError:
        return None


def calib():
    with open(CALIB) as fh:
        c = json.load(fh)
    return c["px_per_cell"], c["px_per_cell_y"], c["left"], c["top"], \
        c.get("gain_x", 1.0), c.get("gain_y", 1.0)


def teacher_strokes():
    with open(TEACHER) as fh:
        t = json.load(fh)
    return [points_of(st)
            for st in t]


def park(target):
    """Put the pointer on target (button up), correcting to within 1 px."""
    tx, ty = int(round(target[0])), int(round(target[1]))
    sh(["abs", "move", str(tx), str(ty)])
    for _ in range(8):
        p = pos()
        if p is None:
            return
        dx, dy = tx - p[0], ty - p[1]
        if abs(dx) <= 1 and abs(dy) <= 1:
            return
        sh(["rel", "move", str(dx), str(dy)])


def draw(cells, kx, ky, bx, by):
    """One stroke: press, then the teacher's points as small reports, release.

    Two properties matter and both were measured, not assumed:

      * REPORT SIZE. A teacher segment sent as ONE long report over-travels — the
        host's gain is not uniform across report sizes — so each segment is cut
        into reports of at most REPORT_PX.
      * TIME IN FLIGHT. The page samples the pointer once per display frame
        (~16.7 ms), so a stroke that goes out in a few milliseconds records only
        a handful of points: a 96-cell bowl came back as 72 cells because its
        right-hand sweep went by inside two frames and was never sampled. A
        small `wait` after every report keeps the stroke on a human timescale
        (the teacher's own bowl took 2.5 s) and gives every report a frame of
        its own.
    """
    sx = [int(round(bx + x * kx)) for x, _ in cells]
    sy = [int(round(by + y * ky)) for _, y in cells]
    park((sx[0], sy[0]))
    verbs = ["down"]
    lx, ly = sx[0], sy[0]
    for x, y in zip(sx[1:], sy[1:]):
        dx, dy = x - lx, y - ly
        n = max(1, int(math.ceil(max(abs(dx), abs(dy)) / REPORT_PX)))
        for i in range(1, n + 1):
            tx, ty = round(dx * i / n), round(dy * i / n)
            px_, py_ = round(dx * (i - 1) / n), round(dy * (i - 1) / n)
            if tx != px_ or ty != py_:
                verbs += ["drag", str(tx - px_), str(ty - py_),
                          "wait", str(REPORT_WAIT_MS)]
        lx, ly = x, y
    verbs.append("up")
    r = subprocess.run([KVM, "mouse", "rel"] + verbs, capture_output=True,
                       text=True, timeout=180)
    if r.returncode != 0:
        sys.stderr.write(r.stderr[-300:])


def clear_pad():
    sh(["abs", "move", str(CLEAR_BTN[0]), str(CLEAR_BTN[1])])
    sh(["rel", "down", "up"])


def main():
    which = [a for a in sys.argv[1:] if not a.startswith("-")] or ["stem", "bowl"]
    kx, ky, bx, by, gx, gy = calib()
    strokes = teacher_strokes()
    names = ["stem", "bowl"]
    print("map: x' = %.3f*x + %.1f , y' = %.3f*y + %.1f" % (kx, bx, ky, by))
    clear_pad()
    for name in which:
        cells = strokes[names.index(name)]
        # The pad paints each commanded pixel slightly FURTHER than asked:
        # measured delivered/commanded gain 1.072 (x) and 1.066 (y) over a clean
        # two-stroke draw. That is the scale term of the user's x' = kx + b, so
        # it is divided out here, about the stroke's OWN first cell: the stroke
        # still starts on the teacher's start cell and only its extent is scaled
        # back. One gain for the whole glyph — never a per-stroke constant.
        x0, y0 = cells[0]
        cells = [(x0 + (x - x0) / gx, y0 + (y - y0) / gy) for x, y in cells]
        draw(cells, kx, ky, bx, by)
        print("%s: %d teacher points, cells (%.1f,%.1f)..(%.1f,%.1f) gain x%.3f y%.3f"
              % (name, len(cells), cells[0][0], cells[0][1],
                 cells[-1][0], cells[-1][1], gx, gy), flush=True)
    return 0


if __name__ == "__main__":
    sys.exit(main())
