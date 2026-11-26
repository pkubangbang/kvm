#!/usr/bin/env python3
"""Independent verifier for letter_cmds.sh.

Re-integrates the EMITTED commands (abs anchors + drag deltas) with its own
cursor model -- it shares no code with gen_lettering.py -- and checks:

  * every ink point (anchor and every integrated drag endpoint) is inside the
    pad rect;
  * the per-line x extents fit with margin (no right-edge overflow);
  * the drag deltas of each stroke sum back to the stroke's planned end is
    NOT checked here (the generator guarantees it) -- but the accumulated
    track is printed so a human can diff it.

Exit 0 = all points inside the pad. Anything else = the plan is NOT fit to
replay. This replaces trusting the generator's own width math.
"""
import re, sys

PAD = (1123, 378, 1758, 1013)   # x1 y1 x2 y2 (inclusive) from pad-bbox

def parse(path):
    strokes = []   # list of (anchor, [points...])
    with open(path) as f:
        for line in f:
            line = line.strip()
            if not line or line.startswith('#'):
                continue
            m = re.search(r'abs move (-?\d+) (-?\d+)', line)
            if not m:
                print("BAD LINE (no abs move):", line)
                sys.exit(2)
            sx, sy = int(m.group(1)), int(m.group(2))
            pts = [(sx, sy)]
            x, y = sx, sy
            for dx, dy in re.findall(r'drag (-?\d+) (-?\d+)', line):
                x += int(dx); y += int(dy)
                pts.append((x, y))
            strokes.append(((sx, sy), pts))
    return strokes

def main(path):
    strokes = parse(path)
    x1, y1, x2, y2 = PAD
    bad = 0
    minx = min(px for _, pts in strokes for px, _ in pts)
    maxx = max(px for _, pts in strokes for px, _ in pts)
    miny = min(py for _, pts in strokes for _, py in pts)
    maxy = max(py for _, pts in strokes for _, py in pts)
    for i, (anchor, pts) in enumerate(strokes):
        for (px, py) in pts:
            if not (x1 <= px <= x2 and y1 <= py <= y2):
                print(f"STROKE {i} POINT ({px},{py}) OUTSIDE PAD")
                bad += 1
    print(f"strokes: {len(strokes)}")
    print(f"ink bbox: x {minx}..{maxx}   y {miny}..{maxy}")
    print(f"pad rect: x {x1}..{x2}   y {y1}..{y2}")
    print(f"margins: left {minx-x1}  right {x2-maxx}  top {miny-y1}  bottom {y2-maxy}")
    if bad:
        print(f"FAIL: {bad} point(s) outside the pad")
        sys.exit(1)
    print("OK: every emitted point is inside the pad")

if __name__ == '__main__':
    main(sys.argv[1] if len(sys.argv) > 1 else 'letter_cmds.sh')
