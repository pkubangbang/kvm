#!/usr/bin/env python3
"""pad_bbox.py -- derive the inputpad canvas rectangle from a screenshot, no LLM.

The pad page (cmd/kvm-inputpad/page.html, `marker()`) paints a REAL QR finder
pattern in THREE corners of its canvas -- TL, TR and BL, exactly the arrangement
a QR symbol has -- each 7x7 cells of concentric rings at 1:1:3:1:1 (solid 3x3
centre, one light ring, one dark ring) inside the mandatory one-cell light
separator, so each mark is 9x9 cells. The fourth (BR) corner is bare: three
marks already fix both axes and the scale, so a fourth would carry no
information.

HOW THE MARKS ARE READ -- run lengths, the way a QR reader does it
-----------------------------------------------------------------
The first version of this script assumed a grid: it took a connected component
of the marker's grey colour, divided its bounding box into 7x7 cells and
sampled each cell centre. That is "assume a grid, then sample", and it is what
real QR detection deliberately does NOT do (ZXing `FinderPatternFinder`): the
approach is brittle under anti-aliasing, colour management and any shear or
clip, because a one-pixel error at the box edge moves every sampled cell.

A QR reader instead walks RUN LENGTHS along a scan line and asks whether five
consecutive runs are in the proportion 1:1:3:1:1 (dark, light, dark, light,
dark). Nothing is assumed about scale, grid origin or exact colour:

  * the module size is MEASURED, `module = total / 7`, never nominal;
  * a row hit is accepted only after it is CROSS-CHECKED vertically through the
    same centre, then horizontally again at the refined centre, then
    diagonally -- four lines through a closed ring agree or the hit is refused;
  * the tolerance is half a module per run (ZXing's `foundPatternCross`), which
    is what keeps ordinary data runs out of the candidate list.

THE SQUARE INVARIANT (the user's rule)
--------------------------------------
The canvas is an exact square BY CONSTRUCTION: page.html sets `max-width` and
`max-height` to the SAME expression plus `aspect-ratio:1/1`, and the three
marks sit on a square 128-lattice at TL, TR and BL. So:

    w != h  =>  the IMAGE is distorted (or a mark was misread / clipped),
                 never the page.

Therefore width and height are measured INDEPENDENTLY -- w from the TL..TR
baseline, h from the TL..BL baseline -- and a rect is emitted only when they
agree to within `--square-tol` px AND the three marks' module sizes agree. A
disagreement produces NO rect and names the marks that disagreed: a wrong
answer must not be able to look like a right one (AGENTS.md §4).

Finding those marks in a screenshot gives the canvas rect in SCREEN pixels --
exactly the `anchor` that TODO-COORDINATES.md demands must be MEASURED and not
back-solved from the trace:

    screen = anchor + ink * extent

Why this is not circular: the marks are painted by the page itself, so one
screenshot yields the canvas rect independently of any click, any trace and any
previous estimate.

Usage:
    python scripts/pad_bbox.py SHOT.png [--json] [--grey 154] [--tol 10]
                                         [--square-tol 1.5] [--debug] [-v]

Output (stdout): one JSON object with keys
    canvas {x,y,w,h}, cell_px, anchor [x,y], extent [w,h],
    markers {TL,TR,BL}, decode "3/3", confident true/false,
    and on refusal:  refused {reason, detail}

Exit codes: 0 = a rect was formed from a SQUARE reading of all THREE marks,
            1 = no rect (missing marks, or a non-square/distorted reading),
            2 = usage/IO error.

Coordinates are SCREEN pixels (desktop space), the same space `kvm mouse abs
move X Y` speaks -- NOT ink-space ratios. See README "Coordinates".
"""
import argparse
import json
import sys

import numpy as np
from PIL import Image

N_CELLS = 128          # canvas is a 128x128 cell grid
MK_RING = 7            # the QR finder pattern
MK_SEP = 1             # cells of light quiet zone on each side of it
MK_CELLS = MK_RING + 2 # + one separator cell each side
GREY = 0x9A            # #9a9a9a, the marker fill colour

# The finder as rows, 1 = dark, matching page.html marker(): a 7x7 square ring
# of 1:1:3:1:1 proportions with a 3x3 solid centre.
MK = [
    '1111111',
    '1000001',
    '1011101',
    '1011101',
    '1011101',
    '1000001',
    '1111111',
]

RATIO = (1, 1, 3, 1, 1)     # dark, light, dark, light, dark
RUNS = len(RATIO)


# ---------------------------------------------------------------------------
# image loading and binarization
# ---------------------------------------------------------------------------

def load_gray(path):
    """The screenshot as a 2-D uint8 luminance array, in ORIGINAL pixels.

    No resize, no crop, no square squash: every coordinate this script reports
    is a pixel coordinate of the file handed in. `np.asarray` exposes the
    decoded array and cannot change its shape.
    """
    im = Image.open(path).convert('RGB')
    return np.asarray(im).astype(np.float32).mean(axis=2)


def otsu(gray):
    """A global Otsu threshold on the luminance histogram.

    A fixed colour match (`abs(px - 0x9A) <= 10`) was the old gate; it ties the
    decoder to one nominal grey and to the display's colour management. Otsu
    picks the split between the two dominant populations (dark page / light
    glyph, or the reverse) from the image itself. Returns (threshold, dark_is_low).
    """
    hist, _ = np.histogram(gray, bins=256, range=(0, 256))
    hist = hist.astype(np.float64)
    total = hist.sum()
    if total == 0:
        return 128.0, True
    levels = np.arange(256, dtype=np.float64)
    w0 = np.cumsum(hist)
    w1 = total - w0
    m0 = np.cumsum(hist * levels)
    m1 = m0[-1] - m0
    with np.errstate(divide='ignore', invalid='ignore'):
        between = (m0[-1] * w0 / total - m0) ** 2 / (w0 * w1)
    between = np.nan_to_num(between)
    t = int(np.argmax(between))
    # The majority population is "background"; the minority one is the ink.
    lo = hist[:t + 1].sum()
    hi = hist[t + 1:].sum()
    return float(t), bool(lo <= hi)


def dark_mask(gray, thresh, dark_is_low):
    return (gray <= thresh) if dark_is_low else (gray > thresh)


# ---------------------------------------------------------------------------
# run-length 1:1:3:1:1 finder detection
# ---------------------------------------------------------------------------

def runs_at(mask, x, y, step_x, step_y, limit=256):
    """The run lengths of same-valued pixels walking from (x,y) along a step.

    Returns (runs, end) where runs alternates values starting with the value at
    (x,y), and `end` is one past the last position visited inside the image.
    `limit` bounds the window, which matters on a solid field.
    """
    h, w = mask.shape
    if not (0 <= y < h and 0 <= x < w):
        return [], (x, y)
    value = bool(mask[y, x])
    out = []
    n = 0
    cx, cy = x, y
    while 0 <= cy < h and 0 <= cx < w and bool(mask[cy, cx]) == value and n < limit:
        n += 1
        cx += step_x
        cy += step_y
    out.append(n)
    while len(out) < RUNS and 0 <= cy < h and 0 <= cx < w:
        value = not value
        n = 0
        while 0 <= cy < h and 0 <= cx < w and bool(mask[cy, cx]) == value and n < limit:
            n += 1
            cx += step_x
            cy += step_y
        out.append(n)
    return out, (cx, cy)


def is_finder_ratio(runs):
    """ZXing's `foundPatternCross`: 1:1:3:1:1 within half a module per run."""
    if len(runs) != RUNS or any(r <= 0 for r in runs):
        return False
    total = sum(runs)
    if total < 7:
        return False
    module = total / 7.0
    tol = module / 2.0
    for r, k in zip(runs, RATIO):
        if abs(k * module - r) >= k * tol:
            return False
    return True


def centre_of(runs, end_x, end_y):
    """The centre of the middle (3-module) run, given the walk's end position."""
    dx = end_x
    dy = end_y
    return None  # replaced by the caller, which knows the step direction


def ratio_centre(start, step, runs):
    """Centre of the 1:1:3:1:1 window whose first pixel is `start`, walking `step`."""
    centre_offset = runs[0] + runs[1] + runs[2] / 2.0
    cx = start[0] + step[0] * centre_offset
    cy = start[1] + step[1] * centre_offset
    return cx, cy


def scan_line(mask, start, step, limit):
    """One line: find every 1:1:3:1:1 window along it.

    Yields (centre, runs) for each window. The scan starts at a run boundary --
    the caller passes the first pixel of the line, and the walk begins at the
    first pixel whose value differs from its predecessor so a window never
    starts mid-run, which is what ZXing's state machine achieves by counting
    from the row start.
    """
    h, w = mask.shape
    x, y = start
    sx, sy = step
    if not (0 <= y < h and 0 <= x < w):
        return
    # Advance to the first value change so the window starts on a run boundary.
    prev = bool(mask[y, x])
    while 0 <= y < h and 0 <= x < w and bool(mask[y, x]) == prev:
        x += sx
        y += sy
    while 0 <= y < h and 0 <= x < w:
        runs, _ = runs_at(mask, x, y, sx, sy, limit)
        if len(runs) == RUNS and is_finder_ratio(runs):
            yield ratio_centre((x, y), step, runs), runs
            # step past the first two runs so overlapping windows are not re-read
            skip = runs[0] + runs[1]
            x += sx * skip
            y += sy * skip
        else:
            skip = runs[0] if runs else 1
            x += sx * skip
            y += sy * skip


def cross_check(mask, cx, cy, expect_module, tol_factor=0.5):
    """Vertical, horizontal and diagonal agreement through a row hit.

    Returns (ox, oy, module) at the refined centre, or None. Each line must read
    1:1:3:1:1 with half a module per run -- the same test the row hit passed --
    and its module must match the row's within `tol_factor` modules, which is
    what refuses a texture that happens to align in one direction.
    """
    ix, iy = int(round(cx)), int(round(cy))
    h, w = mask.shape
    if not (0 <= iy < h and 0 <= ix < w):
        return None

    def read(step):
        # Walk back to the start of the centre run, then read the five runs.
        sx, sy = step
        value = bool(mask[iy, ix])
        bx, by = ix, iy
        while 0 <= by - sy < h and 0 <= bx - sx < w and bool(mask[by - sy, bx - sx]) == value:
            bx -= sx
            by -= sy
        runs, _ = runs_at(mask, bx, by, sx, sy)
        if len(runs) != RUNS or not is_finder_ratio(runs):
            return None
        module = sum(runs) / 7.0
        if module <= 0 or abs(module - expect_module) > tol_factor * module:
            return None
        return ratio_centre((bx, by), step, runs), module

    vert = read((0, 1))
    if vert is None:
        return None
    (cx1, cy1), m1 = vert
    ix, iy = int(round(cx1)), int(round(cy1))
    if not (0 <= iy < h and 0 <= ix < w):
        return None
    horiz = read((1, 0))
    if horiz is None:
        return None
    (cx2, cy2), m2 = horiz
    ix, iy = int(round(cx2)), int(round(cy2))
    if not (0 <= iy < h and 0 <= ix < w):
        return None
    diag = read((1, 1)) and read((1, -1))
    if not diag:
        return None
    module = (expect_module + m1 + m2) / 3.0
    return float(cx2), float(cy2), module


def candidates(mask, row_stride=2):
    """Every cross-checked finder candidate, kept once per centre."""
    h, w = mask.shape
    found = []
    for y in range(1, h - 1, row_stride):
        # Dark pixel runs only: a finder's first run is dark. `dark_mask` is the
        # ink mask, so a window starting on light is the same window one run later.
        for (cx, cy), runs in scan_line(mask, (0, y), (1, 0), 4 * N_CELLS):
            got = cross_check(mask, cx, cy, sum(runs) / 7.0)
            if got is None:
                continue
            gx, gy, module = got
            for f in found:
                if abs(f['x'] - gx) <= module and abs(f['y'] - gy) <= module:
                    n = f['n']
                    f['x'] = (f['x'] * n + gx) / (n + 1)
                    f['y'] = (f['y'] * n + gy) / (n + 1)
                    f['module'] = (f['module'] * n + module) / (n + 1)
                    f['n'] = n + 1
                    break
            else:
                found.append({'x': gx, 'y': gy, 'module': module, 'n': 1})
    return found


def assign_corners(cands):
    """Name three finder centres TL / TR / BL, refusing anything else.

    A QR triad is three marks at three corners of a square. The test is the
    geometry a QR reader applies to `selectBestPatterns`: similar module sizes
    and an isosceles right triangle. A fourth mark, or a triangle that is not
    that shape, is a stray -- refused rather than averaged in.
    """
    if len(cands) < 3:
        return {}, 'only %d finder candidate(s)' % len(cands)
    top = sorted(cands, key=lambda c: -c['n'])[:6]
    best = None
    for i in range(len(top)):
        for j in range(i + 1, len(top)):
            for k in range(j + 1, len(top)):
                trio = [top[i], top[j], top[k]]
                ms = [t['module'] for t in trio]
                if max(ms) > 1.4 * min(ms):
                    continue
                d = []
                for a in range(3):
                    for b in range(a + 1, 3):
                        pa, pb = trio[a], trio[b]
                        d.append(((pa['x'] - pb['x'])**2 + (pa['y'] - pb['y'])**2) ** 0.5)
                d.sort()
                # isosceles right: the two short sides equal, the long side sqrt(2) x
                score = abs(d[0] - d[1]) + abs(d[2] - d[0] * 2 ** 0.5)
                if best is None or score < best[0]:
                    best = (score, trio, d)
    if best is None:
        return {}, 'no three candidates with agreeing module sizes'
    score, trio, d = best
    if d[0] <= 0 or abs(d[0] - d[1]) > 0.05 * d[0] or abs(d[2] - d[0] * 2 ** 0.5) > 0.05 * d[2]:
        return {}, ('triangle is not a square corner (sides %.1f %.1f %.1f)'
                    % (d[0], d[1], d[2]))
    # TL is the corner opposite the hypotenuse, i.e. the one at both the shortest
    # x..x pair and the shortest y..y pair.
    by_x = sorted(trio, key=lambda c: c['x'])
    by_y = sorted(trio, key=lambda c: c['y'])
    tl = {id(by_x[0]), id(by_x[1])} & {id(by_y[0]), id(by_y[1])}
    if len(tl) != 1:
        return {}, 'cannot identify the top-left corner'
    t = [c for c in trio if id(c) in tl][0]
    others = [c for c in trio if c is not t]
    tr = [c for c in others if c['x'] > t['x']][0] if any(c['x'] > t['x'] for c in others) \
        else max(others, key=lambda c: c['x'])
    bl = [c for c in others if c is not tr][0]
    named = {'TL': t, 'TR': tr, 'BL': bl}
    return named, None


def canvas_rect(named, square_tol=1.5):
    """The canvas rect from three finder CENTRES, or (None, refusal).

    Each centre is the middle of the mark's 3x3 core, three cells inside the
    mark from each side, and the mark's own cell 0 sits AT the canvas edge. So
    the canvas corners are:

        TL corner = TL centre - 3.5 module (in both axes)
        width     = TR centre - TL centre    (the horizontal baseline)
        height    = BL centre - TL centre    (the vertical baseline)

    Width and height are measured INDEPENDENTLY and must agree: the canvas is a
    square by construction, so `w != h` is a distorted image, not a page shape.
    """
    if len(named) < 3:
        missing = [k for k in ('TL', 'TR', 'BL') if k not in named]
        return None, {'reason': 'missing marks', 'detail': {'missing': missing}}
    tl, tr, bl = named['TL'], named['TR'], named['BL']
    modules = {k: named[k]['module'] for k in ('TL', 'TR', 'BL')}
    m = (modules['TL'] + modules['TR'] + modules['BL']) / 3.0
    spread = max(modules.values()) - min(modules.values())
    if spread > 0.25 * m:
        return None, {'reason': 'module sizes disagree',
                      'detail': {'modules': {k: round(v, 3) for k, v in modules.items()}}}

    w = tr['x'] - tl['x']
    h = bl['y'] - tl['y']
    if abs(w - h) > square_tol:
        return None, {'reason': 'reading is not square (distorted image)',
                      'detail': {'w': round(w, 2), 'h': round(h, 2),
                                 'delta': round(w - h, 2),
                                 'square_tol': square_tol,
                                 'modules': {k: round(v, 3) for k, v in modules.items()}}}
    x = tl['x'] - 3.5 * m
    y = tl['y'] - 3.5 * m
    return (x, y, w, h), None


def find_markers(path, grey=GREY, tol=10, row_stride=2):
    """Load, binarize and find cross-checked finder candidates.

    `grey`/`tol` are accepted for interface compatibility but no longer gate the
    decode: binarization is Otsu on the image itself (see `otsu`).
    """
    gray = load_gray(path)
    thresh, dark_is_low = otsu(gray)
    mask = dark_mask(gray, thresh, dark_is_low)
    return gray, mask, thresh


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument('shot')
    ap.add_argument('--grey', type=int, default=GREY,
                    help='accepted for compatibility; decoding is Otsu-binarized')
    ap.add_argument('--tol', type=int, default=10,
                    help='accepted for compatibility; decoding is Otsu-binarized')
    ap.add_argument('--square-tol', type=float, default=1.5,
                    help='max |w-h| in px before the reading is refused as distorted')
    ap.add_argument('--row-stride', type=int, default=2,
                    help='scan every Nth row for 1:1:3:1:1 windows')
    ap.add_argument('--json', action='store_true')
    ap.add_argument('-v', '--verbose', action='store_true')
    a = ap.parse_args(argv)

    try:
        gray, mask, thresh = find_markers(a.shot, a.grey, a.tol, a.row_stride)
    except (OSError, ValueError) as e:
        print('pad_bbox: cannot read %s: %s' % (a.shot, e), file=sys.stderr)
        return 2

    cands = candidates(mask, a.row_stride)
    named, why = assign_corners(cands)
    rect, refusal = canvas_rect(named, a.square_tol)

    out = {
        'shot': a.shot,
        'image_wh': [int(gray.shape[1]), int(gray.shape[0])],
        'threshold': round(thresh, 2),
        'dark_pixels': int(mask.sum()),
        'candidates': len(cands),
        'decode': '%d/3' % len(named),
        'markers': {k: {'x': round(m['x'], 2), 'y': round(m['y'], 2),
                        'module': round(m['module'], 3), 'n': m['n']}
                    for k, m in sorted(named.items())},
    }
    if why:
        out['refused'] = {'reason': why}
    if refusal:
        out['refused'] = refusal
    if rect is not None:
        x, y, w, h = rect
        out['canvas'] = {'x': int(round(x)), 'y': int(round(y)),
                         'w': int(round(w)), 'h': int(round(h))}
        out['cell_px'] = round(w / float(N_CELLS), 4)
        out['anchor'] = [int(round(x)), int(round(y))]
        out['extent'] = [int(round(w)), int(round(h))]
    out['confident'] = bool(rect is not None and len(named) >= 3)

    if a.json:
        print(json.dumps(out, sort_keys=True))
    else:
        print('shot        %s  (%dx%d)  threshold %.1f'
              % (a.shot, out['image_wh'][0], out['image_wh'][1], thresh))
        print('dark px     %d   candidates %d   decode %s'
              % (out['dark_pixels'], out['candidates'], out['decode']))
        for k, m in sorted(named.items()):
            print('  %s  centre (%7.2f,%7.2f)  module %.3f  hits %d'
                  % (k, m['x'], m['y'], m['module'], m['n']))
        if rect is None:
            print('canvas      REFUSED: %s' % (refusal or why or 'no rect'))
            if refusal and 'detail' in refusal:
                print('            %s' % (refusal['detail'],))
        else:
            print('canvas      x=%d y=%d w=%d h=%d  (%.4f px/cell)'
                  % (out['canvas']['x'], out['canvas']['y'],
                     out['canvas']['w'], out['canvas']['h'], out['cell_px']))
        if a.verbose:
            for m in cands:
                print('  cand (%7.2f,%7.2f) module %.3f n=%d'
                      % (m['x'], m['y'], m['module'], m['n']))
    return 0 if out['confident'] else 1


if __name__ == '__main__':
    sys.exit(main())
