#!/usr/bin/env python3
"""pad_bbox.py -- derive the inputpad canvas rectangle from a screenshot, no LLM.

The pad page (cmd/kvm-inputpad/page.html, `marker()`) paints a REAL QR finder
pattern in THREE corners of its canvas -- TL, TR and BL, exactly the arrangement
a QR symbol has -- each 7x7 cells of concentric rings at 1:1:3:1:1 (solid 3x3
centre, one light ring, one dark ring) inside the mandatory one-cell light
separator, so each mark is 9x9 cells. The fourth (BR) corner is bare: three
marks already fix both axes and the scale, so a fourth would carry no
information. The user's correction: "角上的二维码，你在4个角都画了，这是不对的".

Finding those marks in a screenshot gives the canvas rect in SCREEN pixels --
exactly the `anchor` that TODO-COORDINATES.md demands must be MEASURED and not
back-solved from the trace:

    screen = anchor + ink * extent

Why this is not circular: the marks are painted by the page itself, so one
screenshot yields the canvas rect independently of any click, any trace and any
previous estimate.

Usage:
    python scripts/pad_bbox.py SHOT.png [--json] [--grey 154] [--tol 10] [-v]

Output (stdout): one JSON object with keys
    canvas {x,y,w,h}, cell_px, anchor [x,y], extent [w,h],
    markers {TL,TR,BL}, decode "3/3", confident true/false

Exit codes: 0 = a rect was formed from all THREE decoded marks, 1 = no rect,
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
MK_CELLS = MK_RING + 2 # + one separator cell each side
GREY = 0x9A            # #9a9a9a, the marker fill colour


def expected_rows():
    """The 7x7 finder as ASCII rows (1 = dark), matching page.html mkFinder()."""
    rows = []
    for y in range(MK_RING):
        row = ''
        for x in range(MK_RING):
            d = max(abs(x - 3), abs(y - 3))
            row += '1' if (d == 3 or d <= 1) else '0'
        rows.append(row)
    return rows


MK = expected_rows()
# The separator is a LIGHT ring around the dark border: the sampled cell just
# outside row 0 must be 0, which is what rejects a solid grey block.
MK_SEP_ROW = '000000000'


def grey_mask(arr, grey=GREY, tol=10):
    return (np.abs(arr - int(grey)).max(axis=2) <= int(tol))


def components(mask, min_px=25):
    """4-neighbour connected components. Returns [(x0,y0,x1,y1,n)]."""
    h, w = mask.shape
    seen = np.zeros_like(mask, dtype=bool)
    out = []
    ys, xs = np.nonzero(mask)
    for sy, sx in zip(ys.tolist(), xs.tolist()):
        if seen[sy, sx]:
            continue
        stack = [(sy, sx)]
        seen[sy, sx] = True
        x0 = x1 = sx
        y0 = y1 = sy
        n = 0
        while stack:
            y, x = stack.pop()
            n += 1
            if x < x0: x0 = x
            if x > x1: x1 = x
            if y < y0: y0 = y
            if y > y1: y1 = y
            for ny, nx in ((y, x-1), (y, x+1), (y-1, x), (y+1, x)):
                if 0 <= ny < h and 0 <= nx < w and mask[ny, nx] and not seen[ny, nx]:
                    seen[ny, nx] = True
                    stack.append((ny, nx))
        if n >= min_px:
            out.append((x0, y0, x1, y1, n))
    return out


def sample_ring(mask, x0, y0, x1, y1):
    """Sample the box as a 9x9 grid; return the centre 7x7 rows and the
    separator evidence (the corner cell and the edge mid-cells must be light)."""
    cw = (x1 - x0 + 1) / float(MK_CELLS)
    ch = (y1 - y0 + 1) / float(MK_CELLS)

    def cell(i, j):
        cy = int(y0 + (j + 0.5) * ch)
        cx = int(x0 + (i + 0.5) * cw)
        return 1 if mask[cy, cx] else 0

    rows = []
    for j in range(MK_SEP + 0, MK_SEP + MK_RING):
        rows.append(''.join(str(cell(i, j)) for i in range(MK_SEP, MK_SEP + MK_RING)))
    sep = [cell(0, 0), cell(8, 0), cell(0, 8), cell(8, 8),
           cell(4, 0), cell(0, 4)]
    return rows, sep


def find_markers(path, grey=GREY, tol=10):
    arr = np.asarray(Image.open(path).convert('RGB')).astype(int)
    mask = grey_mask(arr, grey, tol)
    cands = []
    for b in components(mask):
        x0, y0, x1, y1, n = b
        bw, bh = x1 - x0 + 1, y1 - y0 + 1
        if bw < 9 or bh < 9 or not (0.55 <= bw / float(bh) <= 1.8):
            continue
        rows, sep = sample_ring(mask, x0, y0, x1, y1)
        ok = rows == MK and sum(sep) == 0
        cands.append({'box': b, 'rows': rows, 'ok': ok, 'w': bw, 'h': bh, 'n': n})
    # The finder's light ring splits its silhouette into the outer dark frame
    # plus the solid centre, so keep only the LARGEST box containing the others.
    def contains(a, b):
        return (a[0] <= b[0] and a[1] <= b[1] and a[2] >= b[2] and a[3] >= b[3])
    cands.sort(key=lambda m: -m['box'][4])
    kept = []
    for m in cands:
        if not any(contains(k['box'], m['box']) for k in kept):
            kept.append(m)
    kept.sort(key=lambda m: (m['box'][1], m['box'][0]))
    return arr, mask, kept


def assign_corners(markers):
    """Name the decoded marks TL / TR / BL. THREE marks is the whole set: the
    page paints a QR-style triad (TL, TR, BL) and leaves BR bare, and three
    corners fix both axes and the scale, so a fourth would add nothing. A
    'BR' hit is therefore not required -- and if one ever shows up it is a
    stray grey block, not a marker we asked for."""
    good = [m for m in markers if m['ok']]
    if len(good) < 3:
        return {}
    xs = [m['box'][0] for m in good]
    ys = [m['box'][1] for m in good]
    mx = (min(xs) + max(xs)) / 2.0
    my = (min(ys) + max(ys)) / 2.0
    named = {}
    for m in good:
        name = ('B' if m['box'][1] >= my else 'T') + ('R' if m['box'][0] >= mx else 'L')
        named.setdefault(name, m)
    named.pop('BR', None)   # not part of the triad; three is the design
    return named


def canvas_rect(named):
    def edge(m, side):
        x0, y0, x1, y1, _ = m['box']
        return {'left': x0, 'right': x1, 'top': y0, 'bottom': y1}[side]
    if 'TL' in named and 'BR' in named:
        x, y = edge(named['TL'], 'left'), edge(named['TL'], 'top')
        return x, y, edge(named['BR'], 'right') - x + 1, edge(named['BR'], 'bottom') - y + 1
    if 'TL' not in named:
        return None
    if 'TR' in named:
        x = edge(named['TL'], 'left')
        w = edge(named['TR'], 'right') - x + 1
    elif 'BL' in named:
        x = edge(named['BL'], 'left')
        w = edge(named['BL'], 'right') - x + 1
    else:
        return None
    if 'BL' in named:
        y = edge(named['TL'], 'top')
        h = edge(named['BL'], 'bottom') - y + 1
    elif 'TR' in named:
        y = edge(named['TR'], 'top')
        h = edge(named['TR'], 'bottom') - y + 1
    else:
        return None
    return x, y, w, h


def main(argv=None):
    ap = argparse.ArgumentParser(description=__doc__.splitlines()[0])
    ap.add_argument('shot')
    ap.add_argument('--grey', type=int, default=GREY)
    ap.add_argument('--tol', type=int, default=10)
    ap.add_argument('--json', action='store_true')
    ap.add_argument('-v', '--verbose', action='store_true')
    a = ap.parse_args(argv)

    try:
        arr, mask, cands = find_markers(a.shot, a.grey, a.tol)
    except (OSError, ValueError) as e:
        print('pad_bbox: cannot read %s: %s' % (a.shot, e), file=sys.stderr)
        return 2

    named = assign_corners(cands)
    # THREE marks is the whole design (TL, TR, BL). Counting them against 3 --
    # not 4 -- is what makes "fully decoded" mean what it says; a `4/4` target
    # would report a correct page as 3/4 and unconfident forever.
    good = len(named)
    rect = canvas_rect(named)

    out = {
        'shot': a.shot,
        'image_wh': [int(arr.shape[1]), int(arr.shape[0])],
        'grey_pixels': int(mask.sum()),
        'candidates': len(cands),
        'decode': '%d/3' % good,
        'markers': {k: {'x': m['box'][0], 'y': m['box'][1],
                        'w': m['w'], 'h': m['h']} for k, m in sorted(named.items())},
    }
    if rect is not None:
        x, y, w, h = rect
        out['canvas'] = {'x': int(x), 'y': int(y), 'w': int(w), 'h': int(h)}
        out['cell_px'] = round(w / float(N_CELLS), 4)
        out['anchor'] = [int(x), int(y)]
        out['extent'] = [int(w), int(h)]
    out['confident'] = bool(rect is not None and good >= 3)

    if a.json:
        print(json.dumps(out, sort_keys=True))
    else:
        print('shot        %s  (%dx%d)' % (a.shot, out['image_wh'][0], out['image_wh'][1]))
        print('grey px     %d   candidates %d   decode %s'
              % (out['grey_pixels'], out['candidates'], out['decode']))
        for k, m in sorted(named.items()):
            print('  %s  (%4d,%4d)  %dx%d' % (k, m['box'][0], m['box'][1], m['w'], m['h']))
        if rect is None:
            print('canvas      NOT FOUND')
        else:
            print('canvas      x=%d y=%d w=%d h=%d  (%.3f px/cell)'
                  % (x, y, w, h, out['cell_px']))
        if a.verbose:
            for m in cands:
                print('  cand (%d,%d %dx%d n=%d) match=%s rows0=%s'
                      % (m['box'][0], m['box'][1], m['w'], m['h'], m['n'],
                         m['ok'], m['rows'][0] if m['rows'] else '-'))
    return 0 if out['confident'] else 1


if __name__ == '__main__':
    sys.exit(main())
