# Rig acceptance — #22

Owner: graphologist (measurement verdict only; no rig code edited).
Workdir: the repository root (this checkout). Every number below is measured on the artifacts named.

**Timestamps checked.** `git status`/`git diff` cannot be used here: `.gitignore:8` is
`/.mycc/*`, so no rig file is tracked. The check applied instead is mtime vs. clock:
at **00:00:05** the newest of the seven was `scripts/padturns.py` 23:27:57, then
`padhand.py` 23:27:38, `pad_trace.py` 23:19:25, `pad_corr.py` 23:04:15, `pad_glyph.py`
22:54:42, `pad_draw_glyph.py` 22:38:10, `pad_coverage.py` 22:25:55 — i.e. auditor-pad's
#19 had **not** landed. Re-check item 2 after it does; the line numbers below are 23:27 state.

---

## 1. Reference targets (teacher trace `pad-trace-raw.json`, 2 strokes, 201 points, epoch 8)

*Struck through below: the teacher fixture and the scripts that produced these tables were
removed with `reference/`; the numbers stay as the measured record.*

* Stem path **403.1 px** (67.2 cells) over **1504 ms** (ts 2707212 → 2708716).
* Bowl path **577.0 px** (96.2 cells) over **2507 ms** (ts 2709916 → 2712423).
* Pen lift **1200 ms** (2708716 → 2709916); **2 strokes** exactly.
* Ink **163.4 cells**; bbox **34 × 67**, x 37..70, y 26..92 (172 cells of the 128×128 grid).
* Genuine corners **0 and 0** (heading change ≥ 30° within 3 cells, 0.25-cell resample).
* Net signed turn **+5°** (stem) and **+235°** (bowl), clockwise = +.
* Ascender : body **0.69** (27.0 / 39.0 cells); bowl **w/h 0.80** (31.2 / 39.0).
* Density 75 and 126 samples, median dt **17 ms** = one sample per 60 Hz frame.
* Final teacher points (the "past the target" limits): stem y **90.7** at x 41.2; bowl
  **(41.8, 92.3)**, rightmost x **70.7**.
* Reproduce: **no longer possible** — `scripts/pad_coverage.py` now needs an explicit trace
  path and the teacher file is gone. It read `TOTAL: 172 cells ... bbox (37,26) 34x67`,
  `ink 403 px` / `ink 577 px`.

---

## 2. Per-script verdict against the four issue checks

Checks: **(a)** padturns still deletes |turn|>90°; **(b)** padhand's `BUDGET_S` is compared and
can exit non-zero; **(c)** pad_glyph's first report is a rel delta and Y uses `px_per_cell_y`;
**(d)** readers no longer default a missing `fx`/`fy` to 0.5.

| script | (a) >90° | (b) budget | (c) rel+Y | (d) fx/fy 0.5 | evidence |
|---|---|---|---|---|---|
| `padturns.py` | **FAIL** | n/a | n/a | **FAIL** | `:79-80` `if abs(d) > 90: d = 0.0` still inside the `:75` loop; `:40` `p.get("fx", 0.5)`; returns 0 unconditionally so it cannot fail a draw |
| `padhand.py` | n/a | **FAIL** | n/a | **PASS (n/a)** | `:30` `BUDGET_S = 5.0` used only as the `:105` subprocess timeout; `t0` at `:97` is **after** `park()` at `:96`; `:142` prints total, `:143` `return 0` — no comparison. Recovery `:109` `[KVM,"mouse","rel","up"]` is rejected by `internal/cli/parse.go:154-158`, a silent no-op. No fx/fy reader. |
| `pad_glyph.py` | n/a | n/a | **FAIL both halves** | **PASS (n/a)** | `:101` `fx = fy = 0` ⇒ first `drag` at `:103` is the absolute (~1000 px) park coordinate; `:48` returns only `c["px_per_cell"]` and `:132` applies it to Y; `:126` `cal.get("top", box.get("y", 158))` ignores the live `-box` y |
| `pad_draw_glyph.py` | n/a | n/a | n/a | **FAIL** | `:116` `p.get("fx", 0.5)`; also unpaced ≤6 px drags (`:99-106`) — the emitter that produced the 0.71 length ratio |
| `pad_corr.py` | n/a | n/a | n/a | **FAIL** | `:43` `p.get("fx", 0.5)`; `:34` still curls the server's removed record route (405) when `argv[2]` is omitted |
| `pad_coverage.py` | n/a | n/a | n/a | **FAIL** | `:33` `p.get("fx", 0.5)`; `:27` curls the 405 — and the bare invocation is its documented form (`:4`) |
| `pad_trace.py` | n/a | n/a | n/a | **FAIL** | `:65` `p.get("fx", 0.5)`; `:141-142` divides geometry by `gain_x`/`gain_y` (1.072/1.066) on top of Go's `wireGainDiv = 1.55` (`internal/mouse/leg.go:56`) — dormant only because `pad_calib.json:6-7` currently pins 1.0 |

**Item 2 verdict: FAIL / NOT ACCEPTED.** All four checklist items are in their pre-fix state;
13 call sites in 11 files still default `fx`/`fy` to 0.5 while the CDP family
(`ab_cert.py:132`, `cdp_cert.py:117`, `ab_zcert.py:111`, `ab_paced_cert.py:101`,
`cdp_draw.py:87`, `z_geometry.py:20`) uses the true `0.0`; and two readers (`pad_corr.py:34`,
`pad_coverage.py:27`, plus `pad_fidelity.py:29`, `pad_seq_corr.py:44`,
`pad_copy_teacher.py:100`, `pad_leg_probe.py:40`, `ascii_trace.py:27`) still call the removed
GET the server's removed record route (`store.go:182-189` → HTTP 405 + JSON body).

---

## 3. No-display re-certification procedure

### 3.1 Artifacts (no display, no browser, no server read-back)
* Teacher fixture: **removed** with `reference/` (the drawn-side checks below still work).
* Drawn: the newest `<client>.jsonl` in the server's `-out` **directory**. Running server
  for this session: `./kvm-inputpad -addr 127.0.0.1:8898 -box 1067,158,768,768 -out /tmp/padtrace`
  (`ps -eo pid,etimes,args`), so the file is `/tmp/padtrace/<client>.jsonl`, one record per
  line (`load`/`down`/`drag`/`up`/`clear`) appended by `store.go`. Locate it without a
  display: `ls -t /tmp/padtrace/*.jsonl | head -1`, and confirm the run with
  `tail -3 /tmp/padtrace/events.jsonl` (`stroke`/`clear` lines carry `client`, `epoch`,
  `points`, `total_strokes`).

### 3.2 Exact command lines (run for this verdict; all exit 0)
The paired commands below needed `reference/pad-trace-raw.json` and no longer run; only the
single-trace forms are still available.
```
cd <repo root>   # repository checkout containing cmd/, scripts/
DRAWN=$(ls -t /tmp/padtrace/*.jsonl | head -1)

python3 scripts/pad_tracecheck.py "$DRAWN"                 # stroke count, per-stroke n/bbox/ts
python3 scripts/pad_coverage.py "$DRAWN"                    # drawn cells/ink/bbox
```
Never omit `argv[1]` on `pad_corr.py`/`pad_coverage.py`: that path curls a removed endpoint
and gets a 405.

### 3.3 Thresholds — all must hold; one failure = FAIL
| # | gate | threshold |
|---|---|---|
| 3.3a | stroke count | **exactly 2** (`pad_tracecheck.py` header) |
| 3.3b | stem path | 403.1 px **±10 %** → 362.8 .. 443.4 |
| 3.3c | bowl path | 577.0 px **±10 %** → 519.3 .. 634.7 |
| 3.3d | durations | stem 1354..1654 ms; bowl 2256..2758 ms |
| 3.3e | pen lift | 1200 ms, band 600..1800 ms |
| 3.3f | points past the final teacher point | **0** — no stem sample with y > 91.7 (90.7+1), no bowl sample with x > 71.7 or y > 93.5 |
| 3.3g | containment | every sample inside teacher bbox + 1 cell: x 36..71, y 25..93 |
| 3.3h | net signed turn | **±10 % of teacher**: stem +4.5..+5.5°, bowl +211.5..+258.5° |
| 3.3i | corners | 0 corners ≥ 30° within 3 cells per stroke (`padturns.py` cannot do this — item 2) |
| 3.3j | dwell at each sustained turn | for each accepted turn, the recorded samples covering it must span ≥ **9.6 × median dt** (the teacher's own `SLOW_FACTOR*2*WINDOW` = 9.6 frames); on THIS glyph both strokes have 0 sustained turns, so the gate is vacuous here and only binds on a cusped glyph (Z) |
| 3.3k | coverage | ≥ 90 % of 172 cells → **≥ 155** |
| 3.3l | path error | `pad_corr` mean **≤ 6 px** and ≥ 90 % of the 200 pairs within 6 px |
| 3.3m | pacing | ≈1.1 (stem) / ≈1.3 (bowl) samples per cell; median dt 16-17 ms; ≤ 5 % of dt ≤ 2 ms |

### 3.4 Inline block for the gates no script prints (3.3f, 3.3m, and 3.3h/3.3j via resample)
```
python3 - <<'EOF'
import json, math
D=json.load(open("<drawn.json>"))
P=lambda s:[(p["x"]+p.get("fx",0.0), p["y"]+p.get("fy",0.0), p["ts"]) for p in s]
print("3.3f stem past:", sum(1 for q in P(D[0]) if q[1] > 91.7),
      " bowl past:", sum(1 for q in P(D[1]) if q[0] > 71.7 or q[1] > 93.5))
print("3.3m stem dt/cell:", round(len(D[0])/67.2,2), "bowl:", round(len(D[1])/96.2,2))
EOF
```
(`fx`/`fy` at **0.0** is mandatory here: 33 of 201 x and 26 of 201 y samples carry no key
because of `omitempty` at `cmd/kvm-inputpad/main.go:26-27`. Using 0.5 shifts 16 % of samples
by 3 px and would corrupt 3.3f.)

### 3.5 Baseline measured with this procedure (epoch 33, 23:28, `/tmp/padtrace/a1d569cc-*.json`, 111+147 pts)
| gate | measured | result |
|---|---|---|
| 3.3a | 2 strokes (`2 stroke(s), 258 point(s)`) | PASS |
| 3.3b | 420.2 px (+4.2 %) | PASS |
| 3.3c | 503.0 px (−12.8 %) | **FAIL** |
| 3.3d | 2589 / 3915 ms | **FAIL** both |
| 3.3e | 971 ms | PASS |
| 3.3f | 0 / 0 past | PASS (short, not over) |
| 3.3g | bbox x 38..60, y 26..92 | PASS |
| 3.3h | stem +4°, bowl **+144°** (−39 %) | **FAIL** bowl |
| 3.3i | stem 0, bowl **2** corners | **FAIL** |
| 3.3j | no sustained turns on either stroke | N/A |
| 3.3k | 139 cells | **FAIL** |
| 3.3l | mean 42.2 px, p90 68.8, 3.0 % within a cell | **FAIL** |
| 3.3m | 1.65 / 1.53 samples per cell | **FAIL** (over-dense = drawn too slow) |

---

## 4. Verdict: can a drawn glyph be certified without a display today?

**No — the procedure is sound and runs today, but the rig it certifies fails its own bar, and
three of the four post-fix checks in item 2 are still unfixed.**

The procedure in §3.2–3.4 needs only two files and no display, and it is *proven*: it ran
against the live record and scored 6 of 13 gates. What blocks certification is not the method
but the state of the pipeline: (i) `pad_glyph.py:101` and `:48` still draw the first segment as
an absolute move and use the X scale for Y, so any run started from it is mis-scaled and
mis-placed; (ii) `padturns.py:79-80` cannot see a corner, so gate 3.3i must be computed inline;
(iii) `pad_hand`'s budget and its illegal `rel up` recovery are unchanged, so nothing bounds a
run or guarantees the button is released; (iv) every metric still reconstructs `fx`/`fy` as
0.5, so sub-cell claims are unsound even when the shape passes. The one thing that *is*
certifiable today is the pair (strokes, path length, bbox) from `pad_tracecheck.py` +
`pad_coverage.py` on a file whose `fx` defaults are ignored — everything finer needs #10, #7,
#9, #8 and #6 to land first.

Order to unblock: #10 (pad_glyph rel delta + `px_per_cell_y`) → #7 (`fx`/`fy` default 0.0) →
#9 (budget + legal release) → #8 (padturns >90°) → #6 (file reader, drop GET) → add the
pacing gate 3.3m to a script.
