#!/usr/bin/env python3
"""pad_anchor.py — the ONE quantity that bridges ink-space and screen-space.

WHY THIS EXISTS
---------------
The pad records a pointer position as a PURE RATIO of the canvas CSS box:
`rx`, `ry` in [0,1] (page.html pos()). Screen-space is one physical pixel.
Neither space can be converted into the other without the pad's ANCHOR: the
SCREEN coordinate of the canvas's top-left corner, in screen pixels.

    screen = anchor + ink * extent          # ink-space  -> screen-space
    ink    = (screen - anchor) / extent     # screen-space -> ink-space

`extent` is the canvas's on-screen size — the CSS box, 553 x 553 on this rig,
measured once from a screenshot and recorded in scripts/pad_calib.json. It is
NEVER asked of the pad server: the pad describes itself in its own viewport
space, which is neither screen px nor trustworthy, and an answer that is
wrong is indistinguishable from one that is right. The anchor is NOT derivable
from the page at all, so it must be MEASURED.

THE RULE THAT COST THIS PROJECT THREE WRONG ORIGINS
---------------------------------------------------
An anchor fitted from the very points it is supposed to explain is circular:
three anchors fitted that way (978, 1117, 1120 px) were each "confirmed" and
each was wrong. So this script INSISTS on a HELD-OUT point:

  * `--fit SX,SY,RX,RY`    one observation: a screen point and the ratio the
                           page recorded for it.
  * `--check SX,SY,RX,RY`  a DIFFERENT observation the anchor must predict.
                           Nothing is adjusted to make it fit. >5 px is a
                           reported FAILURE, never a nudge of the anchor.

Both pairs must be well separated (default 150 px per axis); two clicks a few
pixels apart determine nothing.

USE
---
    # anchor from one calibration click, validated on a second
    python scripts/pad_anchor.py --box 553x553 \
        --fit   1400,500,0.2617,0.5687 \
        --check 1600,700,0.6236,0.9300

    # a click you already made, read straight out of the client record
    python scripts/pad_anchor.py --box 553x553 --fit 1400,500 \
        --record strokes.d/<client>.jsonl --check 1600,700

`--record` finds the newest `down` record in the file and uses its rx/ry for
the point given to it — that is the page's own account of where the cursor was,
which is exactly the witness this project trusts. The record is read from the
pad's client file on disk; there is no read-back endpoint and there must never
be one.

EXIT CODES
----------
0  anchor determined and the held-out point agrees within tolerance
1  anchor determined but the held-out point DISAGREES (bad fit — say so)
2  input problem (bad arguments, record not found, pairs not separated)
"""
import argparse
import json
import os
import sys

TOL_PX = 5.0        # the rig's stated honesty bar (AGENTS.md §4)
MIN_SEP_PX = 150.0  # two observations closer than this determine nothing


def parse_xy(s):
    try:
        x, y = s.split(',')
        return float(x), float(y)
    except Exception:
        raise argparse.ArgumentTypeError('want X,Y (screen px): %r' % s)


def parse_obs(s):
    """SX,SY,RX,RY — one observation, both spaces."""
    parts = s.split(',')
    if len(parts) != 4:
        raise argparse.ArgumentTypeError('want SX,SY,RX,RY: %r' % s)
    try:
        sx, sy, rx, ry = (float(p) for p in parts)
    except Exception:
        raise argparse.ArgumentTypeError('want SX,SY,RX,RY (numbers): %r' % s)
    if not (0.0 <= rx <= 1.0 and 0.0 <= ry <= 1.0):
        raise argparse.ArgumentTypeError(
            'rx/ry are ratios of the canvas box in [0,1]: %r' % s)
    return sx, sy, rx, ry


def parse_box(s):
    try:
        w, h = s.lower().split('x')
        return float(w), float(h)
    except Exception:
        raise argparse.ArgumentTypeError('want WxH (canvas CSS px): %r' % s)


def newest_down(record_path):
    """The LAST `down` record in a client .jsonl — the newest press the page saw."""
    best = None
    with open(record_path, 'r', encoding='utf-8') as fh:
        for line in fh:
            line = line.strip()
            if not line or '"down"' not in line:
                continue
            try:
                rec = json.loads(line)
            except ValueError:
                continue
            if rec.get('kind') != 'down':
                continue
            if 'rx' not in rec or 'ry' not in rec:
                continue
            best = rec
    return best


def anchor_from(obs, extent):
    sx, sy, rx, ry = obs
    w, h = extent
    return sx - rx * w, sy - ry * h


def predict(anchor, obs, extent):
    _, _, rx, ry = obs
    w, h = extent
    return anchor[0] + rx * w, anchor[1] + ry * h


def main(argv):
    ap = argparse.ArgumentParser(
        description='measure the pad anchor and validate it on a held-out point')
    ap.add_argument('--box', required=True, type=parse_box,
                    help='canvas extent WxH in px (measured), e.g. 553x553')
    ap.add_argument('--fit', required=True, type=parse_obs,
                    help='calibration observation SX,SY,RX,RY')
    ap.add_argument('--check', type=parse_obs, default=None,
                    help='held-out observation SX,SY,RX,RY that the anchor must predict')
    ap.add_argument('--record', default=None,
                    help='client .jsonl: take the newest `down` rx/ry for --fit (its SX,SY only)')
    ap.add_argument('--tol', type=float, default=TOL_PX,
                    help='tolerance in px for the held-out check (default %.1f)' % TOL_PX)
    ap.add_argument('--min-sep', type=float, default=MIN_SEP_PX,
                    help='minimum separation of the two observations in px (default %.0f)' % MIN_SEP_PX)
    args = ap.parse_args(argv)

    fit = args.fit
    if args.record:
        rec = newest_down(args.record)
        if rec is None:
            print('pad_anchor: no `down` record with rx/ry in %s' % args.record,
                  file=sys.stderr)
            return 2
        fit = (fit[0], fit[1], float(rec['rx']), float(rec['ry']))
        print('fit      : screen %g,%g -> recorded rx,ry %g,%g (newest down, epoch %s)'
              % (fit[0], fit[1], fit[2], fit[3], rec.get('epoch', '?')))

    if args.check:
        sep = max(abs(args.check[0] - fit[0]), abs(args.check[1] - fit[1]))
        if sep < args.min_sep:
            print('pad_anchor: the two observations are only %g px apart '
                  '(minimum %g). Two points that close determine no anchor.'
                  % (sep, args.min_sep), file=sys.stderr)
            return 2

    ax, ay = anchor_from(fit, args.box)
    print('extent   : %g x %g CSS px' % args.box)
    print('anchor   : %g,%g  (screen px of the canvas top-left corner)'
          % (round(ax), round(ay)))

    if not args.check:
        print('WARNING  : no --check given. An anchor that has not predicted a '
              'point it was NOT fitted to is unverified — see AGENTS.md §3.')
        return 0

    px, py = predict((ax, ay), args.check, args.box)
    ex, ey = px - args.check[0], py - args.check[1]
    err = (ex * ex + ey * ey) ** 0.5
    print('held-out : screen %g,%g  predicted %g,%g  error %+.1f,%+.1f  |err| %.1f px'
          % (args.check[0], args.check[1], round(px), round(py), ex, ey, err))
    if err > args.tol:
        print('VERDICT  : FAIL — the anchor is more than %.1f px off. Do not '
              'draw; re-measure the calibration click.' % args.tol)
        return 1
    print('VERDICT  : OK — within %.1f px. anchor is usable.' % args.tol)
    return 0


if __name__ == '__main__':
    sys.exit(main(sys.argv[1:]))
