#!/usr/bin/env python3
"""heart_pad.py -- draw ONE upward heart in the centre of the inputpad canvas.

THE SPACES, STATED ONCE (README "Coordinates", TODO-COORDINATES.md):
    ink-space    = fraction of the canvas CSS box, 0..1, what the trace records
    screen-space = physical desktop pixels, what `kvm mouse abs move` speaks
    bridge       = anchor (screen px of the canvas top-left) + extent (px)

The anchor is MEASURED, never inferred: it comes from scripts/pad_bbox.py,
which decodes the four grey corner fiducials out of a screenshot.

THE HEART IS UPSIDE UP ON PURPOSE. Closed form, evaluated over the WHOLE
t = [0, 2pi) loop (half a sweep yields an arc, a mound -- measured before):

    x(t) = 16 sin^3 t
    y(t) = 13 cos t - 5 cos 2t - 2 cos 3t - cos 4t

MEASURED, NOT ASSUMED (this comment was wrong before, and the wrongness was
visible on the pad). Rendered to an ASCII bitmap, the curve with y EMITTED
UNCHANGED gives: pointed tip at the TOP, dome above it, two legs below --
that is a heart standing on its point, i.e. the upside-down one. The real
heart (two lobes on top, single point at the bottom) needs y NEGATED.

    y emitted unchanged  ->  tip UP, lobes DOWN   (wrong)
    y negated            ->  lobes UP, point DOWN (the heart the user wants)

So the flip below is mandatory. `heart_draw.py` negates y for the same
reason; this script previously did not, which is why its `plan()` could
"assert" tip-up while the pad showed an inverted glyph: min(y) on the
unflipped ring lands on the tip of the WRONG orientation.

CLOSURE IS ASSERTED, NOT INSPECTED. The legs are relative, so only their SUM
means anything: it must be [0, 0] or the stroke does not return to its start
and the shape cannot be a heart.
"""

import argparse
import json
import math
import os
import subprocess
import sys

# ---- the curve --------------------------------------------------------------

def heart_xy(t):
    """Model point at t. +y is the tip; on screen that is the BOTTOM."""
    s = math.sin(t)
    return (16.0 * s * s * s,
            13.0 * math.cos(t) - 5.0 * math.cos(2 * t)
            - 2.0 * math.cos(3 * t) - math.cos(4 * t))


def heart_ring(n=720):
    """The closed ring of n model points (last does NOT repeat the first)."""
    return [heart_xy(2 * math.pi * i / n) for i in range(n)]


def resample_even(pts, step_model):
    """Resample a closed ring at fixed arc-length step. Returns a closed list."""
    acc = [0.0]
    for i in range(1, len(pts)):
        acc.append(acc[-1] + math.hypot(pts[i][0] - pts[i - 1][0],
                                       pts[i][1] - pts[i - 1][1]))
    total = acc[-1]
    count = max(12, int(round(total / step_model)))
    out = []
    j = 0
    for k in range(count):
        target = total * k / count
        while j < len(acc) - 2 and acc[j + 1] < target:
            j += 1
        span = acc[j + 1] - acc[j]
        f = 0.0 if span <= 0 else (target - acc[j]) / span
        out.append((pts[j][0] + (pts[j + 1][0] - pts[j][0]) * f,
                    pts[j][1] + (pts[j + 1][1] - pts[j][1]) * f))
    out.append(out[0])
    return out


# ---- the plan ---------------------------------------------------------------

def plan(width_px, height_px, step_px):
    """Legs for ONE closed heart, sized to width_px/height_px on screen.

    The glyph is placed by its own bounding box, so `width_px` and
    `height_px` are the drawn extent, not a scale factor -- that keeps the
    ink inside the canvas no matter what the model units are.
    """
    ring = heart_ring(1440)
    xs = [p[0] for p in ring]
    ys = [p[1] for p in ring]
    w_model = max(xs) - min(xs)
    h_model = max(ys) - min(ys)
    sx = width_px / w_model
    sy = height_px / h_model
    step_model = step_px / max(sx, sy)

    even = resample_even(ring, step_model)
    # centre the glyph on the origin: the pointer is parked mid-canvas, so a
    # glyph centred on 0 lands centred on the pad.
    cx = (max(xs) + min(xs)) / 2.0
    cy = (max(ys) + min(ys)) / 2.0
    # ---- THE FLIP. Model +y is the tip. Screen y grows down, so the real
    # heart (lobes on top, point at the bottom) needs the model y NEGATED
    # here and only here. Measured, not assumed: with y emitted unchanged the
    # bitmap shows the tip at the TOP and the lobes at the BOTTOM -- the
    # upside-down glyph. See the module docstring for the rendered evidence.
    wps = [(round((x - cx) * sx), round(-(y - cy) * sy)) for x, y in even]
    legs = [(wps[i][0] - wps[i - 1][0], wps[i][1] - wps[i - 1][1])
            for i in range(1, len(wps))]
    residual = [sum(d[0] for d in legs), sum(d[1] for d in legs)]
    if residual != [0, 0]:
        sys.exit("PLAN BUG: legs do not close, residual %r" % (residual,))

    # the tip is the topmost waypoint; the lobes are the bottom (screen) band
    tip = min(wps, key=lambda p: p[1])
    # The pointer is parked on the glyph's FIRST WAYPOINT, not on the bbox
    # centre. `wps[0]` is (0, -60) for the default 260x230 heart, so pressing
    # at the canvas centre put the drawn bbox 60 px BELOW it: measured, the ink
    # spanned y 401..631 around a centre of 456. Returning the offset here keeps
    # "start" meaning what the docstring says -- where the pen goes down -- and
    # lets main() centre the GLYPH instead of its starting point.
    return {
        'legs': legs,
        'reports': len(legs) + 2,
        'width_px': max(p[0] for p in wps) - min(p[0] for p in wps),
        'height_px': max(p[1] for p in wps) - min(p[1] for p in wps),
        'tip_offset': tip,                 # relative to the stroke start
        'start_offset': wps[0],            # first waypoint, relative to the centre
        'closure': residual,
        'wps': wps,
    }


def anchor_from_trace(port=8899):
    raise SystemExit('use --anchor X,Y')


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    # The anchor is MEASURED from the page's painted corner fiducials by
    # scripts/pad_bbox.py. It is never fetched from the server: there is no
    # such thing as a trustworthy read-back of the pad's geometry, so this
    # script only ever accepts the number a human (or pad_bbox.py) put here.
    ap.add_argument('--anchor', required=True,
                    help='canvas top-left in SCREEN px, e.g. 1255,180 (pad_bbox.py)')
    ap.add_argument('--size', default='260x230',
                    help='drawn extent in px, WIDTHxHEIGHT (inside the 553 canvas)')
    ap.add_argument('--step', type=float, default=8.0)
    ap.add_argument('--gap', type=int, default=10)
    ap.add_argument('--kvm', default=r'.\kvm.exe')
    ap.add_argument('--exec', action='store_true')
    ap.add_argument('--json', action='store_true')
    a = ap.parse_args(argv)

    ax, ay = [int(v) for v in a.anchor.split(',')]
    w, h = [int(v) for v in a.size.lower().split('x')]
    p = plan(w, h, a.step)

    cmd = [a.kvm, 'mouse', 'rel', '-human=false', 'down']
    for dx, dy in p['legs']:
        cmd += ['drag', str(dx), str(dy)]
    cmd += ['up']

    # WHERE THE PEN GOES DOWN. plan() centres the glyph on the ORIGIN and its
    # legs start at wps[0] -- (0, -60) for the default 260x230 heart. Pressing
    # at the raw canvas centre therefore drew the bounding box 60 px LOW
    # (ink spanned y 401..631 around a centre of 456): measured, and the user's
    # requirement is "the exact centre of the canvas". Subtracting the first
    # waypoint puts the GLYPH's bbox on the centre while keeping `start` equal
    # to the press point, which is what the docstring above promises.
    canvas = int(round(float(os.environ.get('PAD_CANVAS_PX', '553'))))
    off_x, off_y = p['start_offset']
    out = {
        'anchor': [ax, ay],
        'start': [ax + canvas // 2 - off_x, ay + canvas // 2 - off_y],
        'centre': [ax + canvas // 2, ay + canvas // 2],
        'start_offset': list(p['start_offset']),
        'size': [p['width_px'], p['height_px']],
        'legs': len(p['legs']),
        'reports': p['reports'],
        'duration_ms': p['reports'] * a.gap,
        'closure': p['closure'],
        'tip_y_relative': p['tip_offset'][1],
    }

    if a.json:
        print(json.dumps(out))
        return 0

    print('start  %d %d   size %dx%d   legs %d   ~%d ms'
          % (out['start'][0], out['start'][1], p['width_px'], p['height_px'],
             len(p['legs']), out['duration_ms']))
    print('tip is %s (negative = ABOVE the start, i.e. the heart points UP)'
          % ('above' if p['tip_offset'][1] < 0 else 'below'))

    if not a.exec:
        print(' '.join(cmd))
        return 0

    abs_cmd = [a.kvm, 'mouse', 'abs', 'move', str(out['start'][0]), str(out['start'][1])]
    subprocess.run(abs_cmd, check=True)
    rc = subprocess.run(cmd).returncode
    print('stroke exit', rc)
    return rc


if __name__ == '__main__':
    sys.exit(main())
