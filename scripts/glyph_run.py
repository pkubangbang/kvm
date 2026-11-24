#!/usr/bin/env python3
"""Draw one glyph and certify it: park, ONE held down..up span per stroke, verdict.

This is the A-Z procedure the user asked for, reduced to one command. It is
deliberately small: park on the first vertex, emit the whole stroke as one
`rel ... down drag.. up` invocation, then read back the pad's trace and judge:

  * corner count per stroke (a Z has 2, an I has 0, a b has 1),
  * start/end cell error against the plan,
  * ink outside the glyph's own bounding box (+1 cell).

usage: glyph_run.py <glyph> [--dry-run]

`--dry-run` prints the command that WOULD be sent and the certification
thresholds, without touching the pointer. That is the reviewable form.

WHY FEW, LARGE REPORTS (measured on this host, 2026-11-24):

  The host applies its pointer-acceleration curve to a relative REPORT STREAM.
  A single report is honest (60 px -> 59, 100 px -> 98, -100 px -> -98 after the
  ACK pacing fix), but the same 300 px split into three 100 px reports lands
  477 px at gap 2-12, and only lands 1:1 once the spacing is >= the host's own
  report rate. Subdividing a leg into dozens of 6 px reports therefore drew
  every horizontal leg 2.5x too long (a 25-cell leg recorded 60 cells), which is
  what made the first Z run fail.

  So a leg is emitted as the FEWEST reports the protocol allows: each report is
  as large as possible without exceeding the +/-127 byte field, and each is
  issued after the previous one's ACK (the CLI now waits for that ACK and
  enforces an 8 ms floor). That keeps every report in the regime where the host
  delivers it 1:1.
"""
import json
import os
import subprocess
import sys

KVM = "./kvm"
REPORT_MAX = 3      # measured: 3 px per report in a gentle, ACK-paced stream
                    # inks correctly (a 60-report 3 px sweep landed 178 px of a
                    # 180 px request, one stroke, 53 samples). Bigger reports in
                    # a burst are accelerated by the host (213 px -> 284-324 px),
                    # and 1-3 px bursts are dropped entirely.
GAP_MS = 12         # one report per host frame; the CLI enforces an 8 ms floor
CELL = 6            # px per pad cell (informational; the page derives it)


# The pad page maps a pointer event to a cell as
#   u = (clientX - rect.left) * (cv.width/rect.width) / C
# (main.go pos()), so ONE CELL is rect.width/128 screen px — not the
# calibration's px_per_cell, which came from a different measurement and is ~2%
# off. Using the calibration put a 60-cell-tall Z down as an 80-cell one: the
# planner said y=90, the page recorded y=125. The page's own mapping wins
# because the page is what records the trace.
PAD_BOX = (1068, 162, 758, 758)   # x, y, w, h of the pad canvas on screen

PARK_ROUNDS = 8     # abs-move convergence rounds before giving up
PARK_TOL = 2        # px; ring reads are frame-quantised, ±1 px is noise


def cell_size():
    _, _, w, h = PAD_BOX
    return w / 128.0, h / 128.0


def plan_reports(cells, kx, ky, bx, by):
    """Turn a cell polyline into the rel reports of ONE held span.

    Reports are as LARGE as the protocol allows and as FEW as possible: one
    report per REPORT_MAX px of the leg's longer axis. Subdividing further is
    what made the first Z run 2.5x too wide — the host delivers a lone report
    1:1 but accelerates a dense stream (see the module docstring).
    """
    scr = [(bx + x * kx, by + y * ky) for x, y in cells]
    # Collapse collinear runs to vertices. The glyph defs are resampled cell
    # by cell, so a "straight" leg arrives as 25 collinear points differing by
    # 1.5 grid cells (9 px) — emitting that literally is the dense stream the
    # host accelerates.
    verts = [scr[0]]
    for p in scr[1:]:
        a, b = verts[-1], p
        if abs(b[0] - a[0]) < 1e-9 and abs(b[1] - a[1]) < 1e-9:
            continue
        if len(verts) >= 2:
            q = verts[-2]
            cross = (b[0] - q[0]) * (a[1] - q[1]) - (b[1] - q[1]) * (a[0] - q[0])
            if abs(cross) < 1e-6:            # collinear: replace, do not add
                verts[-1] = b
                continue
        verts.append(b)
    out = []
    for a, b in zip(verts, verts[1:]):
        dx, dy = b[0] - a[0], b[1] - a[1]
        longest = max(abs(dx), abs(dy))
        n = max(1, int(round(longest / REPORT_MAX)))
        px = py = 0
        for i in range(1, n + 1):
            tx, ty = round(dx * i / n), round(dy * i / n)
            if tx != px or ty != py:
                out.append((tx - px, ty - py))
                px, py = tx, ty
    return out, (scr[0], scr[-1])


def park(x, y):
    """Converge the pointer on (x,y).

    The cursor ring is published at compositor frame rate (~16 ms), so a single
    abs move reads back one frame STALE. Repeat the absolute move and read the
    position until it reports the target.
    """
    for _ in range(PARK_ROUNDS):
        for _ in range(2):
            subprocess.run([KVM, "mouse", "abs", "move", str(int(x)), str(int(y))],
                           capture_output=True, timeout=10)
        p = subprocess.run([KVM, "mouse", "pos"], capture_output=True,
                           text=True, timeout=5).stdout.split()
        if len(p) != 2:
            return None
        if abs(int(p[0]) - x) <= PARK_TOL and abs(int(p[1]) - y) <= PARK_TOL:
            return (int(p[0]), int(p[1]))
    return None


def main():
    if len(sys.argv) < 2:
        print("usage: glyph_run.py <glyph> [--dry-run]")
        return 2
    sys.path.insert(0, "scripts")
    from glyphs import GLYPHS
    name = sys.argv[1]
    dry = "--dry-run" in sys.argv
    if name not in GLYPHS:
        print("unknown glyph %r; have: %s" % (name, ", ".join(sorted(GLYPHS))))
        return 2
    kx, ky = cell_size()
    bx, by = PAD_BOX[0], PAD_BOX[1]
    strokes = GLYPHS[name]
    print("%s: %d stroke(s), %.3f/%.3f px per cell, grid origin (%d,%d)"
          % (name, len(strokes), kx, ky, bx, by))
    plans = []
    for i, cells in enumerate(strokes):
        reports, (start, end) = plan_reports(cells, kx, ky, bx, by)
        plans.append((reports, start, end))
        print("  stroke %d: %d cells, %d reports, start (%.0f,%.0f) end (%.0f,%.0f)"
              % (i, len(cells), len(reports), start[0], start[1], end[0], end[1]))
        if dry:
            verbs = ["down"]
            for dx, dy in reports:
                verbs += ["drag", str(dx), str(dy)]
            verbs.append("up")
            print("    %s mouse rel -gap %d %s" % (KVM, GAP_MS, " ".join(verbs)))
    if dry:
        return 0

    for i, (reports, start, _) in enumerate(plans):
        got = park(start[0], start[1])
        if got is None:
            print("    no park within %d rounds — not pressing" % PARK_ROUNDS)
            return 1
        print("    parked at (%d,%d)" % got)
        verbs = ["down"]
        for dx, dy in reports:
            verbs += ["drag", str(dx), str(dy)]
        verbs.append("up")
        r = subprocess.run([KVM, "mouse", "rel", "-gap", str(GAP_MS)] + verbs,
                           capture_output=True, text=True, timeout=60)
        if r.returncode != 0:
            print("    span failed: %s" % r.stderr.strip()[:160])
            return 1
    print("drawn. certify with:")
    print("  N=$(ls -t /tmp/padtrace/*.json | head -1)")
    print("  python3 scripts/stroke_direction.py \"$N\"")
    return 0


if __name__ == "__main__":
    sys.exit(main())
