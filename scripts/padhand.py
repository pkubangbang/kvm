#!/usr/bin/env python3
"""Draw a stroke the way a hand does it: one held span, paced to the teacher's
speed, released exactly on the target — inside a hard time budget.

This file exists because the user's two rules are design constraints, not tuning
knobs:
  * ONE letter is ONE held down..up span (not a chain of segments — that turned
    a single letter into ~60 pen lifts and ~45 s);
  * every letter has a HARD 5 s budget measured from pointer-down; the drawing
    process is killed if it overruns.

The numbers come from analysis.md (graphologist, measured on the teacher trace):
  * the page samples once per ~16.7 ms frame, so the pointer must advance
    ~0.25 px/ms to record one sample per 5-6 px of ink;
  * a report under ~4 px is eaten by the host, so reports stay 4-6 px;
  * a 6 px report every 22 ms (stem) / 26 ms (bowl) reproduces the teacher's
    speed exactly: 68 reports x 22 ms = 1.5 s, 97 x 26 ms = 2.5 s;
  * the stroke must STOP on its own last target. The stray lower-right tail came
    from continuing past it (a landing move / a settle with the button held); the
    release happens on the target and nothing is drawn afterwards.
"""
import json
import math
import subprocess
import sys
import time

KVM = "./kvm"
CALIB = "scripts/pad_calib.json"
BUDGET_S = 5.0            # hard cap per letter, from pointer-down
REPORT_PX = 5             # report size the host delivers 1:1
GAP_STEM_MS = 22
GAP_BOWL_MS = 26
GAP_DEFAULT_MS = 24


def calib():
    with open(CALIB) as fh:
        c = json.load(fh)
    return c["px_per_cell"], c["px_per_cell_y"], c["left"], c["top"]


def pos():
    w = subprocess.run([KVM, "mouse", "pos"], capture_output=True, text=True,
                       timeout=5).stdout.split()
    if len(w) != 2:
        return None
    try:
        return int(w[0]), int(w[1])
    except ValueError:
        return None


def park(target):
    """Place the pointer on target with the button UP (placement is not ink)."""
    tx, ty = int(round(target[0])), int(round(target[1]))
    subprocess.run([KVM, "mouse", "abs", "move", str(tx), str(ty)],
                   capture_output=True, timeout=5)
    for _ in range(8):
        p = pos()
        if p is None:
            return
        dx, dy = tx - p[0], ty - p[1]
        if abs(dx) <= 1 and abs(dy) <= 1:
            return
        subprocess.run([KVM, "mouse", "rel", "move", str(dx), str(dy)],
                       capture_output=True, timeout=5)


def report_plan(cells, kx, ky, bx, by):
    """The reports of one held span, and how many ms each must stay in flight."""
    scr = [(bx + x * kx, by + y * ky) for x, y in cells]
    out = []
    for a, b in zip(scr, scr[1:]):
        dx, dy = int(round(b[0] - a[0])), int(round(b[1] - a[1]))
        dist = math.hypot(dx, dy)
        n = max(1, int(round(dist / REPORT_PX)))
        for i in range(1, n + 1):
            tx, ty = round(dx * i / n), round(dy * i / n)
            px, py = round(dx * (i - 1) / n), round(dy * (i - 1) / n)
            if tx != px or ty != py:
                out.append((tx - px, ty - py))
    return out


def draw_held(cells, kx, ky, bx, by, gap_ms, budget=BUDGET_S, t0=None):
    """One held span over the whole stroke, paced, released ON the last point.

    The last report is emitted as its own report so the release lands exactly on
    the target: no landing move, no settle, no ink past the end.

    `t0` is the START of the letter's budget, taken by the caller BEFORE the
    park. Parking is up to eight `abs`/`rel move` subprocesses and used to sit
    outside the budget, so a slow park could spend the whole 5 s and the span
    would still get a fresh one.
    """
    reports = report_plan(cells, kx, ky, bx, by)
    if not reports:
        return 0.0, False
    end = (bx + cells[-1][0] * kx, by + cells[-1][1] * ky)
    if t0 is None:
        t0 = time.monotonic()
    left = budget - (time.monotonic() - t0)
    if left <= 0.2:
        print("  BUDGET EXHAUSTED before the span (%.1fs used) — stroke skipped"
              % (time.monotonic() - t0))
        return 0.0, True
    park((bx + cells[0][0] * kx, by + cells[0][1] * ky))
    verbs = ["down"]
    for dx, dy in reports[:-1]:
        verbs += ["drag", str(dx), str(dy), "wait", str(gap_ms)]
    # The last report keeps its pace too: emitted with no `wait` its gap fell back
    # to the 2 ms default and `up` landed before the host had sampled the tail,
    # which is the measured 47 px tail. Then one button-up landOn.
    verbs += ["drag", str(reports[-1][0]), str(reports[-1][1]), "wait", str(gap_ms)]
    verbs.append("up")
    proc = subprocess.Popen([KVM, "mouse", "rel"] + verbs,
                            stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True)
    overrun = False
    try:
        proc.communicate(timeout=max(0.2, budget - (time.monotonic() - t0)))
    except subprocess.TimeoutExpired:
        # The 5 s budget is hard. Kill OUR process by pid (a `pkill -f "kvm mouse
        # rel"` also matched any other script's live span), then release the
        # button with a LEGAL span: a bare `rel up` is rejected by the parser
        # ("up must be inside a down..up span"), so the old recovery exited 1 and
        # left the button DOWN. `rel down up` is legal and needs no prior state.
        proc.kill()
        try:
            proc.communicate(timeout=2)
        except subprocess.TimeoutExpired:
            pass
        rec = subprocess.run([KVM, "mouse", "rel", "down", "up"],
                             capture_output=True, text=True, timeout=10)
        if rec.returncode != 0:
            print("  RELEASE FAILED (rc=%d): %s" % (rec.returncode, rec.stderr.strip()))
        print("  BUDGET KILL at %.1fs — span aborted, button released"
              % (time.monotonic() - t0))
        overrun = True
        return time.monotonic() - t0, overrun
    elapsed = time.monotonic() - t0
    # Release happened on `end`; if the host drifted, correct with the button UP.
    p = pos()
    if p is not None:
        dx, dy = int(round(end[0])) - p[0], int(round(end[1])) - p[1]
        if abs(dx) > 1 or abs(dy) > 1:
            subprocess.run([KVM, "mouse", "rel", "move", str(dx), str(dy)],
                           capture_output=True, timeout=5)
    return elapsed, overrun


def main():
    sys.path.insert(0, "scripts")
    from glyphs import GLYPHS

    if len(sys.argv) < 2 or sys.argv[1] not in GLYPHS:
        print("usage: padhand.py <glyph>   glyphs: " + ", ".join(sorted(GLYPHS)))
        return 1
    name = sys.argv[1]
    kx, ky, bx, by = calib()
    plan = GLYPHS[name]
    print("%s: %d stroke(s), %.3f/%.3f px per cell, budget %.0f s"
          % (name, len(plan), kx, ky, BUDGET_S))
    # The budget starts at the first POINTER-DOWN of the letter, so parking the
    # pen counts against it: that is what "5 s per letter from pointer-down" means.
    total = 0.0
    overrun = False
    t0 = time.monotonic()
    for i, cells in enumerate(plan):
        gap = GAP_STEM_MS if i == 0 else GAP_BOWL_MS
        el, over = draw_held(cells, kx, ky, bx, by, gap, BUDGET_S, t0)
        total = time.monotonic() - t0
        overrun = overrun or over
        print("  stroke %d: %.2f s (%d reports, %d ms gap)"
              % (i, el, len(report_plan(cells, kx, ky, bx, by)), gap), flush=True)
    ok = total <= BUDGET_S and not overrun
    print("letter %s: %.2f s total, budget %.0f s per letter — %s"
          % (name, total, BUDGET_S, "PASS" if ok else "OVER BUDGET"))
    return 0 if ok else 1


if __name__ == "__main__":
    sys.exit(main())
