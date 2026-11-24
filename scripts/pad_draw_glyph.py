#!/usr/bin/env python3
"""Draw a glyph from FITTED geometry (never eyeballed coordinates).

Usage: pad_draw_glyph.py stem bowl [--legs N]

Every number comes from scripts/pad_fit_teacher.py on the teacher trace:
  stem: centre (40.0,60.4) dir (0.04,1.00) from -33.9 to 30.3 cells
  bowl: centre (56.8,74.6) radii (17.2,21.0) rot 9.6 deg, starting at the
        stem side (39.5,63.5) and closing at (41.8,92.3).

The stem is one held span (top -> bottom). The bowl is one held span that walks
the ellipse from its stem-side start, clockwise past the right extreme, along
the bottom and back up to the stem -- each step <= 6 px, which is the report size
libinput delivers 1:1.

Placement: park with `abs`, read the wire, correct the residual with `rel move`,
and only then press, so the drawn stroke starts on the teacher's start cell.
"""
import json
import math
import subprocess
import sys
from trace_read import points_of, px_per_cell

KVM = "./kvm"
PX = px_per_cell()   # measured, from pad_calib.json
PAD = "http://127.0.0.1:8898"

# Fitted from reference/pad-trace-raw.json by scripts/pad_fit_teacher.py.
STEM = ((38.7, 26.5), (41.1, 90.7))
BOWL_C = (56.8, 74.6)
BOWL_R = (17.2, 21.0)
BOWL_ROT = 9.6
TEACHER_BOWL = []


def sh(args, timeout=120):
    return subprocess.run([KVM, "mouse"] + args, capture_output=True, text=True,
                          timeout=timeout)


def box():
    return json.loads(subprocess.run(["curl", "-s", PAD + "/api/box"],
                                     capture_output=True, text=True, timeout=10).stdout)


def ellipse_angle(cell_xy):
    """Angle (deg) of a point on the fitted ellipse, measured from its centre."""
    th = math.radians(BOWL_ROT)
    dx, dy = cell_xy[0] - BOWL_C[0], cell_xy[1] - BOWL_C[1]
    u = dx * math.cos(th) + dy * math.sin(th)
    v = -dx * math.sin(th) + dy * math.cos(th)
    return math.degrees(math.atan2(v / BOWL_R[1], u / BOWL_R[0]))


def ellipse_point(a):
    th = math.radians(BOWL_ROT)
    u = BOWL_R[0] * math.cos(math.radians(a))
    v = BOWL_R[1] * math.sin(math.radians(a))
    return (BOWL_C[0] + u * math.cos(th) - v * math.sin(th),
            BOWL_C[1] + u * math.sin(th) + v * math.cos(th))


def path_points():
    """Stem then bowl, as polylines in pad cells."""
    # The stem is SAMPLED, not two endpoints: the page records one point per
    # pointer event, so a two-point stroke reaches the pad as a bare line and
    # carries no sample density to correlate against the teacher's 75 points.
    nstem = max(2, int(round(math.hypot(STEM[1][0] - STEM[0][0],
                                        STEM[1][1] - STEM[0][1]) * PX / 6)) + 1)
    stem = [(STEM[0][0] + (STEM[1][0] - STEM[0][0]) * k / (nstem - 1),
             STEM[0][1] + (STEM[1][1] - STEM[0][1]) * k / (nstem - 1))
            for k in range(nstem)]
    # The bowl is NOT an ellipse fit: the teacher's loop is rounder and wider
    # than any ellipse through it (it reaches x 70.7 and starts with a nearly
    # horizontal run), and a fit shifted start/end by four cells. Copy the
    # teacher's own polyline, resampled at equal arc length (~1 px), which is
    # what "draw the same shape" means.
    global TEACHER_BOWL
    bowl = TEACHER_BOWL
    return [stem, bowl]


def draw(pts, cx, cy, gap_px=6):
    sx = int(round(cx + pts[0][0] * PX))
    sy = int(round(cy + pts[0][1] * PX))
    sh(["abs", "move", str(sx), str(sy)])
    for _ in range(4):                      # place exactly: abs is not repeatable
        w = sh(["pos"]).stdout.split()
        if len(w) != 2:
            break
        ddx, ddy = sx - int(w[0]), sy - int(w[1])
        if abs(ddx) <= 2 and abs(ddy) <= 2:
            break
        sh(["rel", "move", str(ddx), str(ddy)])
    verbs = ["down"]
    last = pts[0]
    for p in pts[1:]:
        # chop each step into <= gap_px reports so the page samples the glide
        dx = (p[0] - last[0]) * PX
        dy = (p[1] - last[1]) * PX
        n = max(1, int(math.ceil(max(abs(dx), abs(dy)) / gap_px)))
        for k in range(1, n + 1):
            vx = int(round(dx * k / n)) - int(round(dx * (k - 1) / n))
            vy = int(round(dy * k / n)) - int(round(dy * (k - 1) / n))
            if vx or vy:
                verbs += ["drag", str(vx), str(vy)]
        last = p
    verbs.append("up")
    sh(["rel"] + verbs)


def load_teacher(path="reference/pad-trace-raw.json"):
    """Resample the teacher's bowl stroke at equal arc length (~1 px)."""
    with open(path) as fh:
        trace = json.load(fh)
    pts = points_of(trace[1])
    out = [pts[0]]
    for a, b in zip(pts, pts[1:]):
        d = math.hypot(b[0] - a[0], b[1] - a[1]) * PX
        n = max(1, int(round(d / 6.0)))
        for k in range(1, n + 1):
            out.append((a[0] + (b[0] - a[0]) * k / n, a[1] + (b[1] - a[1]) * k / n))
    return out


def main():
    global TEACHER_BOWL
    TEACHER_BOWL = load_teacher()
    b = box()
    cx, cy = b.get("x", 0), b.get("y", 0)
    plan = path_points()
    for i, pts in enumerate(plan):
        draw(pts, cx, cy)
        print("stroke %d: %d points, start cell (%.1f,%.1f) end (%.1f,%.1f)"
              % (i, len(pts), pts[0][0], pts[0][1], pts[-1][0], pts[-1][1]))
    return 0


if __name__ == "__main__":
    sys.exit(main())
