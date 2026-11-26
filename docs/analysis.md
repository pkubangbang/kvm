# Hand-writing analysis of the teacher glyph vs the machine attempt

Sources: `reference/pad-trace-raw.json` (teacher, epoch 8, 75 + 126 points),
`/tmp/drawn.json` (machine, epoch 21, 141 + 144 points).
All cells are 6 px; times are ms from each stroke's first point unless stated.
Metrics reproduced: `pad_corr.py` mean 52.1 px, p90 85.6 px, 4.5 % within one cell;
`pad_coverage.py` 172 cells, bbox 34x67; `pad_fit_teacher.py` stem
(38.7,26.5)->(41.1,90.7), bowl centre (56.8,74.6) radii (17.2,21.0) rot 9.6 deg.

---

## 1. Stroke-level description of the teacher's glyph

### Global
* One glyph, two strokes, **one pen-lift**: stroke 0 ends ts 2708716 (41.2, 90.7),
  stroke 1 starts ts 2709916 (39.5, 63.5) -> **lift of 1200 ms**.
* Total ink 163.4 cells / 980.1 px; bounding box x 37..70, y 26..92 (170 cells).
* Sampling: median dt **17 ms** on both strokes (p10 16, p90 18) -> the teacher is
  recorded **once per 60 Hz frame**; zero intervals <= 2 ms, no duplicate ts. So the
  teacher's point count is a *clock* property, not a report-count property.

### Stroke 0 — stem (75 pts, 1504 ms, path 403.1 px = 67.2 cells)
* **Direction: top -> bottom**, nearly pure. Chord 64.2 cells, total-variation
  |dx| 6.7 / |dy| 65.2 cells; max lateral deviation from the chord **0.74 cells**.
  Net signed heading change **0 deg**, **0 genuine corners** (no >= 30 deg turn inside
  3 cells). It is a straight vertical with a *slight bow*, not a polyline.
* **Speed profile** (cells/s, local +-5 points):
  | % arc | t (ms) | cells/s | x,y |
  |---|---|---|---|
  | 0 | 0 | 10 | (38.5,26.5) |
  | 10 | 454 | 41 | (38.8,32.3) |
  | 30 | 687 | 67 | (39.8,44.5) |
  | 50 | 854 | 94 | (39.7,58.3) |
  | 70 | 1006 | 76 | (40.5,71.7) |
  | 90 | 1239 | 52 | (41.2,85.3) |
  | 100 | 1504 | 18 | (41.2,90.7) |
  i.e. **start dwell 293 ms** (first dt), accelerate to a peak of ~94 cells/s
  (=566 px/s) at mid-stem, then decelerate over the last 20 % (334 -> 312 -> 108 px/s).
  Motion-only average (intervals <= 60 ms): 73 intervals / 1211 ms = **60.3 pts/s,
  332.9 px/s = 55.5 cells/s**. Whole-stroke average 44.7 cells/s.
  Median step per sample **5.0 px** = 0.83 cell (p90 9.0 px).
* **Pen-lift at the foot is a dwell, not a stop**: last 6 points stay inside
  y 89.8..90.7 over 83 ms while ts keeps ticking every 16-17 ms.

### Stroke 1 — bowl (126 pts, 2507 ms, path 577.0 px = 96.2 cells)
* **Direction: start at (39.5,63.5) on the stem, sweep UP-RIGHT over the bowl top,
  round the right extreme, along the bottom, and close back leftward at (41.8,92.3).**
  Path/chord ratio 3.32 (a closed-ish loop). Heading per 10 % of arc:
  -69, -34, -11, +18, +63, +86, +113, +133, +153, +171, -169 deg.
  Net signed turn **+243 deg** (clockwise on screen), monotone, **0 genuine corners**:
  one continuous arc, no vertex. Total |heading change| 1017 deg is *quantisation
  noise* around that single arc, not 32 real turns.
* **Speed profile** (px/s): 72 at the start -> 370 (10 %) -> 327 -> 334 -> 396 (40 %,
  the fast run into the right extreme) -> **281 / 275 / 273 / 282 (50-80 %, the whole
  right-and-bottom quadrant is a uniform 46 cells/s)** -> 257 (90 %) -> **24 at the
  close**. Motion-only: 122 intervals / 2099 ms = 58.1 pts/s, 274.9 px/s =
  **45.8 cells/s**; whole stroke 38.4 cells/s. Median step 4.47 px (p90 7.8).
* **Dwell**: first dt 136 ms at the stem, and a **205 ms dwell at the close**
  (41.8, 92.3) — the hand lands, holds, then lifts. Inter-point dts at 52 and 67 ms
  at (42.3-42.5, 92.5) are a small hesitation at the end of the closing run.

### Proportions
* Ascender (stem above the bowl top y 53.5): 27.0 cells.
* Body (bowl height): 39.0 cells. **ascender : body = 0.69**.
* Bowl width 31.2 cells, height 39.0 -> **w/h = 0.80** (taller than wide).
* Bowl attaches to the stem at y 63.5 = **58 % down the stem**, and closes at
  y 92.3 = **1.6 cells BELOW the stem foot (90.7)** — the bowl overshoots the stem.
* Stem foot 90.7 sits *inside* the bowl's vertical span: the closing run passes
  under the stem.

### Bowl shape — it is NOT an ellipse, it is a tightening spiral
Normalised radius r/rmax along the arc: 0.72 at the start, 0.75-0.77 over the top
(25-30 %), then **pinches to 0.44-0.49 between 60 % and 80 %** (the right/lower-right
quadrant), and opens to 0.82 at the close. Deviation from the fitted ellipse peaks at
**6.8 cells at 65 %** (and 5.8 at 55 %, 6.5 at 60 %), while the top (20-35 %) is within
0.3 cells of it. **The teacher's loop tightens on the bottom-right and is flat and wide
across the top.** Any pure-ellipse draw will be wrong by up to 7 cells there; copying
the teacher polyline (as `pad_draw_glyph.py` now does) is the correct choice.

---

## 2. Why the machine trace is short and truncated (ranked)

Machine: stem 141 pts / 1719 ms / 366.9 px (teacher 403.1 = **91 %**);
bowl 144 pts / 1848 ms / 408.5 px (teacher 577.0 = **71 %**).

### Cause 1 (dominant for the bowl, ~20 of the 30 %) — small fast reports are eaten by the host, so delivered gain < 1 and it is *direction dependent*
`pad_draw_glyph.draw()` chops each step into reports of <= 6 px and sends them all in
one `rel` batch at the pinned 2 ms drag gap. Simulating exactly that emitter on the
teacher bowl gives **140 non-zero drag verbs summing 577.7 px**, with a magnitude
histogram of:

| |v| px | 1 | 2 | 3 | 4 | 5 | 6 | 7 | 8 |
|---|---|---|---|---|---|---|---|---|---|
| reports | 12 | 13 | 20 | 49 | 17 | 18 | 7 | 4 |

**53 of 140 (38 %) are < 4 px.** `PITFALLS.md` #3 measured the host's gain on exactly
this: 20 px @ 12 ms -> **0.90**, 4 px -> **0.67**, and a 68 px decay train arrived as
40 px. 577.7 commanded x 0.71 = 410 px — the recorded 408.5 px. The shortfall is
therefore not a shape error but a **delivery** error, and it is worst where the
reports are smallest: **66 of the 140 reports are issued while x > 60 cells**, i.e. over
the right extreme, and that is exactly where the ink stops: drawn xmax **60.8** vs
teacher **70.7**.
Direction dependence confirms it: start-normalised x extent is **62 %** of the
teacher's (19.4 vs 31.2 cells) while the y extent is **92 %** (26.7 vs 29.0). The
horizontal progress (the flat top run, where the reports are the diagonal 4-5 px ones)
is lost; the vertical progress survives.

### Cause 2 (the ~8-cell end truncation on BOTH strokes) — `up` is issued before the tail is delivered; the pointer then settles backwards
Both strokes stop **7.7-7.8 cells** short — stem end y 82.9 (teacher 90.7), bowl end
(43.4, 84.9) vs (41.8, 92.3). A *constant* 47 px tail, independent of the stroke's
length and of its gain (which differ: 0.94 vs 0.71), is the signature of a fixed
trailing queue, not of proportional loss. The tail is visible in the ts:

```
stem  idx 136 ts 461801 (40.43,79.33)   <- 4 samples share ts 461801
      idx 139 ts 461816 (40.43,83.95)
      idx 140 ts 461862 (40.43,82.87)   <- 46 ms later the pointer is BACK 1.1 cell
```
The last sample steps *backwards*: the release happened while the host still held
queued reports, and the observer's next frame shows the pointer where it actually
settled. Same at the bowl: `(42.74,77.79) ts 464229 -> (42.90,80.56) 464235 ->
(43.21,83.33) 464252 -> (43.36,84.87) 464253`, then nothing — the stroke dies 7.8
cells before y 92.3.

### Cause 3 (start placement, 2.6 cells) — the park/correct loop did not converge
Commanded stem start is (38.7, 26.5); recorded start is **(40.4, 23.9)** — 2.6 cells
high and 1.7 right. The bowl is worse: recorded press at (41.4, 58.2) vs the
teacher-copied start (39.5, 63.5), **5.6 cells off**. `abs move` is seeded from its own
target and the 4-pass `rel` correction tolerates only +/-2 px (`PITFALLS.md` #4, #5),
so a >1 cell error can survive. This alone costs the first 10 % of `pad_corr` its error
budget and is why the correlation is bad at every fraction, not just at the end.

### Cause 4 (character, not length) — no wall-clock pacing
Teacher ~60 samples/s. Machine dt distribution has **40/140 (stem) and 35/143 (bowl)
intervals <= 2 ms** (13 and 2 exactly 0 ms) mixed with 16-17 ms ones: the page
coalesces a burst of reports into one frame event, so extra reports buy **no**
samples. `leg.go` already documents this ("a 0.35 s Z recorded 20 samples whether it
went out as 75 reports or 300"). Consequence: report *count* is not density; only
time-in-flight is.

### Cause 5 — the extra corner the user sees
The teacher bowl has **0 genuine corners**; the machine bowl has **2** (>= 30 deg turn
within 3 cells). One is real and visible: after the loop the drawn ink runs **flat
left along y = 77.2 for 8 cells** (idx 120-139, x 46.8 -> 42.7) where the teacher runs
along **y 92.3**. That flat tail is a fake vertex at ~(47.7, 77.0), created by the
stroke dying mid-quadrant (cause 2) and then being closed by a straight leftward
segment. Combined with the `rel drag` direction drift the lead measured (5 % of leg
length perpendicular: 300 px leg -> ~15 cells), chaining held spans manufactures
corners the teacher does not have.

### Summary of the ranked answer
1. Delivery gain < 1 from <= 3 px reports at the 2 ms gap, worst over the right
   extreme **x 60.8 vs 70.7** (accounts for most of the 30 % path shortfall).
2. Release-before-delivery tail loss, a constant **~7.8 cells** on both strokes.
3. Press placement error 2.6 cells (stem) / 5.6 cells (bowl).
4. Report bursts coalescing per frame — density is time, not count.
5. Per-leg direction drift chaining into a spurious corner.

---

## 3. Prescription

### Targets (from the teacher)
| | cells | px | wall-clock | cells/s | px/ms | pts recorded | pts/cell | median step |
|---|---|---|---|---|---|---|---|---|
| stem | 67.2 | 403.1 | **1504 ms** (1211 moving) | 44.7 (55.5 moving) | 0.268 | 75 | 1.11 | 5.0 px |
| bowl | 96.2 | 577.0 | **2507 ms** (2099 moving) | 38.4 (45.8 moving) | 0.230 | 126 | 1.31 | 4.5 px |
| lift | - | - | **1200 ms** | - | - | - | - | - |

The page samples once per **16.7 ms** frame, therefore:
**one recorded sample per 5-6 px of ink** (stem) and per 4.5 px (bowl) =>
the pointer must advance **~0.25 px/ms (0.04 cell/ms)** on both strokes.

### A. If you keep one held span per stroke (recommended — one landing, no chained drift)
* Report size **4-6 px, never below 4 px** (4 px -> 0.67 gain, 20 px -> 0.90).
* Report gap: **22 ms** for 6 px on the stem (0.27 px/ms), **26 ms** for 6 px on the bowl.
  Uniform **24 ms @ 5-6 px** is within 10 % of both.
* Budget: stem **68 reports x 22 ms = 1500 ms**; bowl **97 reports x 26 ms = 2522 ms**.
* Expect: stem 403 px / 67 cells / **75-90 recorded samples**; bowl 577 px / 96 cells /
  **126-150 samples**; gain ~1.0 because at 22-26 ms the pointer is *settled* between
  reports, so the closed loop's stale-origin error is bounded by one report (<= 1 cell)
  instead of accumulating; and no tail loss because the last report has a full frame
  to arrive before `up`.
* Then **one `landOn` with the button UP** on the stroke's true end
  (stem (41.1,90.7), bowl (41.8,92.3)) — with > +/-2 px slack, iterated until
  <= 1 px, because the current 4-pass +/-2 px loop leaves 2-3 cells.

### B. If you keep `drawStep` (one held span per waypoint) — the numbers you asked for
* **Waypoint pitch 1.5 cells (9 px)** — do *not* go to 2-3 cells. Drift is ~5 % of leg
  length (300 px -> 15 cells), so 9 px -> **0.45 cell** (safe), 2 cells -> 0.6 cell,
  3 cells -> 0.9 cell (at the 1-cell budget). Shorter legs are strictly better for
  straightness; buy sample density with **dwell**, not with length.
* **Minimum in-flight per waypoint: 33 ms (2 frames)** — a span shorter than one frame
  records 1-2 points no matter how many reports it contains (this is exactly the
  "31 strokes / 2 points each" symptom). 45 waypoints x 33 ms = 1490 ms for the stem.
* Concrete per-stroke schedule:
  - **stem**: 45 waypoints x 9 px; each span = 2 reports of 4-5 px at gap 11 ms
    (22 ms in flight) + **dwell 12 ms** before `up` => 33 ms/waypoint, 1490 ms total.
  - **bowl**: 64 waypoints x 9 px; each span = 2 reports of 4-5 px at gap 14 ms +
    **dwell 10 ms** => 39 ms/waypoint, 2500 ms total.
  - Add the `landOn` (button up) time on top of these; it must not be *inside* the
    33 ms or the ink pauses and the corner count rises.
* Expect per waypoint **2 recorded samples** (2 frames) -> stem 90, bowl 128 samples;
  **1.5-2 samples per 1.5 cells = 1.0-1.3 per cell**, matching the teacher's 1.11/1.31.
* Keep the teacher's own dwells: **293 ms** at the stem top before the first move,
  **136 ms** at the bowl start, **205 ms** at the close, and a **1200 ms** lift between.

### C. Acceptance you should be able to reach
* `pad_corr.py`: **mean error < 6 px, > 90 % within one cell** (it is a pure
  path-shape test once the start is normalised; the current 52 px is dominated by
  causes 1-3, all of which are placement/pacing, not shape).
  Per-stroke check: stem mean < 5 px, bowl mean < 6 px with worst divergence < 10 px.
* `pad_coverage.py`: **170 cells** (stem 67, bowl 96-106), bbox x 37..70, y 26..92.
  Current: 139-172 cells with bbox x 40..60, y 23..84 — the missing 10 columns of x
  are the whole diagnostic.
* Corners: **0 genuine corners per stroke**; the bowl's flat left tail at y 77.2 must
  be gone (teacher closes at y 92.3).
* Recorded point counts 75+-10 / 126+-15 with median dt 16-17 ms and **< 5 % of
  intervals <= 2 ms**.

### D. Do not
* Do not use < 4 px reports (host eats them).
* Do not put many reports in one frame and expect samples (they coalesce).
* Do not use the ellipse fit for the bowl: it is off by **6.8 cells at 65 %** of the
  arc where the loop tightens (r/rmax 0.44-0.49 vs 0.75 elsewhere).
* Do not trust a 4-pass +/-2 px placement loop: verify the press cell within 1 cell of
  (38.7,26.5) / (39.5,63.5) before `down`.

---

## 4. Appendix — why the recorded absolute cell x drifts between strokes (calibration)

Symptom: the same `drag 0 60` px records 9.2 cells vertically (= 6.52 px/cell, not 6.0)
and an absolute x of 9.6 where the pinned box 1057 predicts (1100-1067)/6 = 5.5; another
identical command moments earlier recorded x 40.4.

**Verdict: the page's live CSS rect, not kvm.** `pos()` has no cached calibration:

```
cmd/kvm-inputpad/main.go:339  function pos(e){ const r=cv.getBoundingClientRect();
cmd/kvm-inputpad/main.go:340    const kx=cv.width/r.width, ky=cv.height/r.height;
cmd/kvm-inputpad/main.go:341    const u=Math.max(0,Math.min(N-0.000001,(e.clientX-r.left)*kx/C));
```

Every sample is mapped through the rect as it is *at event time*. Meanwhile the CSS makes
that rect a function of the viewport:

```
cmd/kvm-inputpad/main.go:288  #pad{display:block;margin:0 auto;...}
cmd/kvm-inputpad/main.go:297    flex:none;height:calc(100vh - 80px);width:auto;max-width:100%;
cmd/kvm-inputpad/main.go:298    aspect-ratio:1/1;object-fit:contain}
```

`height:calc(100vh - 80px)` + `aspect-ratio` set the CSS width from the window height;
`margin:0 auto` re-centres it, so its `left` moves by half the width delta. The 768x768
drawing buffer never changes.

`/api/box` cannot help: `main.go:63` accepts a POST only while `g.X < 0`, so a launch with
`-box 1057,182,767,773` freezes the published rect forever, and the page never posts its
rect (the only `fetch()` calls in the page JS are the three `/api/log` posts; there is no
`resize`/`visualViewport`/`scroll` listener).

Numbers from the reported stroke:
* 60 px / 9.2 cells = **6.52 px/cell** -> live CSS width 128 x 6.52 = **834.8 px**,
  kx = 768/834.8 = 0.920. The canvas is displayed 8.8 % larger than the buffer; the pinned
  box describes 767 px, i.e. 1:1.
* `u = (1100 - r.left) x 128/834.8 = 9.6..10.6` -> **r.left = 1031..1037 px**, 20-26 px left
  of the pinned 1057. `margin:0 auto` shifts left by -(834.8-767)/2 = **-33.9 px** when the
  canvas grows from 767 to 835 CSS px: same sign, comparable magnitude. One viewport change
  explains both the 6.52 px cell and the ~25 px left shift.
* Within one page session x cannot drift (the rect is re-read per event). A 30.8-cell
  (200 px) jump like 40.4 -> 9.6 is ~200 px of rect change: a different page load / tab /
  server instance (default `-addr` is :8899, the docs use :8898) or a ~200 px viewport
  change — check the `epoch` on both strokes before calling it drift.

Fix: page publishes `getBoundingClientRect()` to `/api/box` on load and on
`resize`/`visualViewport.resize`/`scroll`; server drops the `g.X < 0` guard so a POST
overrides `-box`; drawer converts with `screen = box.x + cell * box.w / 128` read
immediately before each stroke (never a hard-coded 6 px, never 1067).
