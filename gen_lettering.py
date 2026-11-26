#!/usr/bin/env python3
"""Generate kvm mouse commands for 3-line lettering on the inputpad.

Lines: "KEEP CALM" / "AND" / "ENJOY LIFE".

STROKE SHAPE (the user's spec -- the ONLY shape, applied to every stroke):

    ./kvm mouse abs move X Y            (ABSOLUTE reposition to the stroke start)
    ./kvm mouse rel down d1 d2 ... up   (RELATIVE ink while the pen is down)

There is NO relative pen-up navigation anywhere. Between strokes the cursor is
re-anchored with an absolute move -- the previous running-cursor `rel move`
bookkeeping is what scattered the letters ("drunk" drift), and it is gone.
There is exactly ONE emitter (`stroke_cmd`); the old duplicate glyph_cmd/
line_cmd blocks are deleted.

Each stroke is emitted as its own command line (its own process = its own span),
so a partial failure cannot merge strokes.

MEASURING LOGIC:
  * word_strokes returns the TRUE INK WIDTH (max point x), no trailing gap.
  * CAP is derived from the canvas width and the LONGEST line, not a constant.
  * Each leg is subdivided to <=5px steps and the steps sum EXACTLY to the
    leg's integer endpoint (one running integer cursor per stroke).
  * Duplicate points (zero-length legs) are dropped.
  * The verifier (`verify_cmds.py`) re-integrates the emitted commands and
    checks every point against the pad rect -- the generator's own width math
    is NOT trusted as evidence.
"""
import math

def _arc(cx, cy, rx, ry, a0, a1, n=14):
    return [(cx + rx*math.cos(math.radians(a)), cy + ry*math.sin(math.radians(a)))
            for a in [a0 + (a1-a0)*i/n for i in range(n+1)]]

G = {
    'A': [[(0,1),(0.5,0),(1,1)], [(0.25,0.55),(0.75,0.55)]],
    'C': [_arc(0.55, 0.5, 0.5, 0.5, 45, 315)],
    'E': [[(1,0),(0,0),(0,1),(1,1)], [(0,0.5),(0.7,0.5)]],
    'F': [[(1,0),(0,0),(0,1)], [(0,0.5),(0.7,0.5)]],
    'I': [[(0.5,0),(0.5,1)]],
    'J': [[(1,0),(1,0.8)] + _arc(0.5, 0.8, 0.5, 0.2, 0, 180, 8)],
    'K': [[(0,0),(0,1)], [(0,0.55),(1,0)], [(0,0.55),(1,1)]],
    'L': [[(0,0),(0,1),(1,1)]],
    'M': [[(0,1),(0,0),(0.5,0.6),(1,0),(1,1)]],
    'N': [[(0,1),(0,0),(1,1),(1,0)]],
    'O': [_arc(0.5, 0.5, 0.5, 0.5, -90, 270, 20)],
    'Y': [[(0,0),(0.5,0.5),(1,0)], [(0.5,0.5),(0.5,1)]],
    # D: stroke 1 = the stem (top-left down to bottom-left); stroke 2 = the
    # bowl. Each stroke gets its own ABS MOVE anchor (the user's rule).
    'D': [[(0, 0), (0, 1)],
          [(0, 0), (0.15, 0)] + _arc(0.15, 0.5, 0.5, 0.5, -90, 90, 12) + [(0, 1)]],
    # P: stem (baseline to cap) then a bowl that leaves the stem top and
    # rejoins it halfway down.
    'P': [[(0, 1), (0, 0)],
          _arc(0.3, 0.28, 0.3, 0.28, -90, 90, 12) + [(0, 0.56)]],
}

WORD_GAP = 0.55
LETTER_GAP = 0.28

def word_strokes(word, cap_px):
    """Strokes of a word in px, plus the word's TRUE INK WIDTH in px."""
    out = []
    x = 0.0
    max_x = 0.0
    for ch in word:
        if ch == ' ':
            x += WORD_GAP
            continue
        strokes = G[ch.upper()]
        w = max(p[0] for st in strokes for p in st)
        for s in strokes:
            pts = [((x + px)*cap_px, py*cap_px) for px, py in s]
            out.append(pts)
            for p in s:
                max_x = max(max_x, x + p[0])
        x += w + LETTER_GAP
    return out, max_x*cap_px

def stroke_cmd(pts, origin_x, origin_y):
    """ONE stroke -> ONE command line:

        ./kvm mouse abs move SX SY && ./kvm mouse rel down <drags> up

    Absolute anchor first, then relative ink only. The per-step deltas use a
    single running integer cursor so the steps sum EXACTLY to the stroke end.
    """
    sx = round(origin_x + pts[0][0])
    sy = round(origin_y + pts[0][1])
    spans = []
    ix, iy = sx, sy          # running integer cursor, starts at the anchor
    px0, py0 = sx, sy        # previous leg endpoint (absolute)
    for p in pts[1:]:
        tx_abs = round(origin_x + p[0])
        ty_abs = round(origin_y + p[1])
        ex, ey = tx_abs - px0, ty_abs - py0
        dist = math.hypot(float(ex), float(ey))
        if dist < 0.5:
            continue         # duplicate point
        n = max(1, min(int(dist / 5.0) + 1, 60))   # <=5px steps
        for i in range(1, n+1):
            tx = px0 + round(ex*i/n)
            ty = py0 + round(ey*i/n)
            if i == n:
                tx, ty = px0+ex, py0+ey            # last step lands EXACTLY
            stx, sty = tx - ix, ty - iy
            if stx or sty:
                spans.append((int(stx), int(sty)))
            ix, iy = tx, ty
        px0, py0 = px0+ex, py0+ey
    drags = ' '.join(f'drag {a} {b}' for a, b in spans)
    return f"./kvm mouse abs move {sx} {sy} && ./kvm mouse rel down {drags} up"

def line_cmds(word, cap_px, origin_x, origin_y):
    strokes, width = word_strokes(word, cap_px)
    return [stroke_cmd(p, origin_x, origin_y) for p in strokes], width

PAD_X1, PAD_Y1, PAD_X2, PAD_Y2 = 1123, 378, 1758, 1013
PAD_W = PAD_X2 - PAD_X1 + 1
PAD_H = PAD_Y2 - PAD_Y1 + 1

LINE_ADV_RATIO = 1.7
SAFE = 0.88          # fraction of the pad width the longest line may use

def layout():
    lines = ["KEEP CALM", "AND", "ENJOY LIFE"]
    units = []
    for ln in lines:
        _, w = word_strokes(ln, 1.0)
        units.append(w)
    cap = (PAD_W * SAFE) / max(units)
    line_adv = cap * LINE_ADV_RATIO
    total_h = 2*line_adv + cap
    top = PAD_Y1 + (PAD_H - total_h)/2
    all_cmds = []
    for i, ln in enumerate(lines):
        _, w = word_strokes(ln, cap)
        ox = PAD_X1 + (PAD_W - w)/2
        oy = top + i*line_adv
        cmds, _ = line_cmds(ln, cap, ox, oy)
        all_cmds.extend(cmds)
    return all_cmds

if __name__ == '__main__':
    for c in layout():
        print(c)
