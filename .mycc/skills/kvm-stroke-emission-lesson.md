---
name: kvm-stroke-emission-lesson
description: >
  Lessons learned from drawing lettering on the kvm inputpad with a CH9329
  USB-HID bridge (kvm mouse abs/rel). Covers problems: strokes scattering
  ("drunk" drift) across a multi-stroke word, a wrong D glyph (overshooting
  bowl arc, curly tail below the baseline), duplicate divergent stroke
  emitters in a generator, right-edge overflow of lines despite the
  generator's self-reported extents claiming they fit, and two strokes lost
  per run. Solutions: emit every stroke as an ABSOLUTE reposition
  (`kvm mouse abs move X Y`) followed by relative ink only
  (`kvm mouse rel down ...drags... up`) — never relative pen-up navigation
  between strokes; use ONE emitter with one running integer cursor per stroke;
  verify extents by re-integrating the emitted commands, not the generator's
  width math; use `;;` to join abs and rel segments in one invocation; diff
  the pad's own trace (strokes.d/<client>.jsonl) to recover ground-truth
  strokes. Applies to tasks involving programmable mouse lettering, HID
  stroke replay, inputpad tracing, or absolute-vs-relative input sequencing.
  Related to kvm CLI grammar, CH9329 transport pacing, inputpad trace format.
keywords: [kvm, ch9329, mouse, lettering, stroke, drift, absolute, relative,
  inputpad, trace, lesson, hid, glyph, generator, verification]
---

# Lessons from drawing "Keep Calm And Enjoy Life" on the kvm inputpad

## Context

The goal: write three lines of text ("KEEP CALM" / "AND" / "ENJOY LIFE") on
the kvm inputpad canvas by driving a CH9329 USB-HID bridge — `kvm mouse abs
move` to place the pointer, `kvm mouse rel down …drags… up` to draw ink —
with each glyph reconstructed from a stroke table in a Python generator
(`gen_lettering.py`) that emits a shell script of `kvm` invocations.

The first runs drew letters that "scattered on the canvas like drunk", lines
overflowed the pad's right edge, and the letter D rendered as a mirrored bowl
with a curly tail hanging below the baseline.

## Problems Encountered

### Problem 1: Drunk scatter — relative pen-up navigation between strokes

The generator moved the pen *up* to the next stroke's start with a **relative**
`rel move DX DY`, tracked by a running `cur_x/cur_y`. The inter-stroke deltas
were computed from the wrong origin (the new stroke's start instead of the
previous stroke's end), so every pen-up jump was misplaced and error
accumulated across the word. Measured: final cursor reached x=1920 (the
screen's right edge) while the pad's right edge is 1758.

### Problem 2: Two near-duplicate stroke emitters diverging

`gen_lettering.py` carried two emitters — `glyph_cmd` and `line_cmd` — with
*different* cursor bookkeeping for the same job. Only one was actually used,
and the other's logic silently disagreed. Any "fix" applied to one did not
reach the other, so behaviour was unpredictable.

### Problem 3: Wrong D glyph — an arc that is not a bowl

The D bowl was `_arc(cx=0.2, rx=0.6, …)` plus a straight leg back to (0,1).
With rx=0.6 the bowl bulged to x=0.8 of the glyph box, then the closing leg cut
across — visually "a mirrored/odd bowl with a curly tail descending below the
baseline". The shape was wrong at the geometry level, not at the transport
level.

### Problem 4: The generator's self-reported extents were wrong (but confident)

The generator printed "extents x 1148..1734 fit inside the pad" while the
actual render clipped the M of "CALM" and truncated "LIFE" to "LI" at the
right edge. The width/position arithmetic was not the truth source — the
render and the trace were.

### Problem 5: Two strokes silently lost per run, and per-stroke "fixes" doubled ink

A full 32-stroke replay traced only 30 strokes: the two A-legs (apex joins)
never landed. Redrawing just those two on top of existing ink created
duplicates and one misplaced stray, which looked like smudging. The "missing"
count turned out to be a trace-segmentation artifact in some runs and a real
loss in others — the count alone did not say which.

## Solutions Found

### Solution 1: Absolute reposition per stroke, relative ink only (the core rule)

Every stroke is emitted as exactly this shape — **no relative pen-up
navigation anywhere**:

```bash
./kvm mouse abs move X Y ';;' rel down drag dx dy drag dx dy ... up
```

The `;;` joins the two namespaces in ONE invocation (bash needs it quoted);
`cli.Parse` rejects mixing `abs` and `rel` in a single namespace run without
it. Between strokes the cursor is re-anchored absolutely, so a stroke can
never inherit drift from its predecessor. This single change eliminated the
scatter entirely.

### Solution 2: ONE emitter, one running integer cursor per stroke

Collapse to a single `stroke_cmd(pts, ox, oy)`. Subdivide each leg to ≤5 px
steps with one running integer cursor so the per-step deltas sum EXACTLY to
the leg's integer endpoint (independent per-step rounding inflated a 47 px
stem to 52 px).

### Solution 3: Verify extents from the emitted commands, not the generator's math

An independent verifier (`verify_cmds.py`) re-integrates `letter_cmds.sh`
(absolute anchors + drag deltas) with its own cursor model and checks every
point against the pad rect:

```
strokes: 32
ink bbox: x 1161..1721   y 587..805
pad rect: x 1123..1758   y 378..1013
OK: every emitted point is inside the pad
```

Trust the re-integration and the pad's own trace — never the generator's
self-report.

### Solution 4: Recover ground truth from the pad's trace by diffing

The pad records every stroke to `strokes.d/<client>.jsonl` (kinds
`load|down|drag|up|clear`, `rx`/`ry` = canvas ratios in [0,1]). When strokes
go missing or shapes are uncertain, have the user draw the reference stroke,
then **diff the trace tail** to extract the exact anchor and deltas in screen
space (`screen = anchor + ink × extent`). That is how the correct D bowl was
recovered:

```
./kvm mouse abs move 1488 671 ';;' rel down drag 0 5 ... drag 0 5 up   # stem
./kvm mouse abs move 1491 677 ';;' rel down drag 5 0 ... drag -5 0 up  # bowl
```

### Solution 5: Fix glyphs in the generator and re-run the WHOLE script

Never patch individual strokes on top of drawn ink — that doubles ink and
invalidates the run. Clear the pad (the top-bar Clear button, e.g. at
(1828,359)), fix `gen_lettering.py`, regenerate, and replay the full script
once.

## Key Learnings

- **Absolute for placement, relative for ink.** Absolute reports drop the
  button byte, so held ink must be relative; but every stroke START must be an
  absolute reposition. Relative pen-up navigation is the root of drift.
- **One emitter, one cursor model.** Duplicate near-identical emitters will
  diverge; delete, don't reconcile.
- **Emit evidence, not claims.** A generator's own width/extent report proved
  wrong three times. Re-integrate the emitted commands independently and read
  the pad's trace.
- **The trace is the ground truth.** `strokes.d/<client>.jsonl` records what
  actually landed (ratios of the canvas box). Diff it to recover exact strokes.
- **A stroke count gap is ambiguous.** 30 traced vs 32 planned can be trace
  segmentation (strokes merged into one record) or a real loss — match stroke
  starts/ends to the plan before concluding, and never "fix" by patching
  single strokes onto existing ink.
- **Glyph shape bugs look like transport bugs.** A wrong arc produces a
  "scattered/dragged" look even when every report landed exactly — check the
  geometry table before blaming pacing or drift.

## What to Do Differently

1. Emit every stroke as `kvm mouse abs move X Y ';;' rel down … up` — absolute
   anchor, relative ink, never relative navigation between strokes.
2. Keep exactly one stroke emitter with one running integer cursor whose steps
   sum exactly to each leg's endpoint.
3. Before replaying, prove extents with an independent re-integration of the
   emitted commands AND dry-run bbox (`kvm -dry-run`), then replay the whole
   script once onto a cleared pad.
4. Verify after drawing from the pad's trace file (newest by mtime) and a
   screenshot; if a stroke is missing, diff the trace to recover ground truth
   rather than guessing a shape.
5. Collapse collinear `drag` runs (`drag 0 5 drag 0 4 …` → one `drag 0 47`)
   only when consecutive deltas are exactly equal — merging `5 2 / 5 3` into
   `10 5` would change the path.

## Related Skills

- environment-detection: for repo/tool layout when resuming this work
- create-skill: for capturing further lessons in this format
