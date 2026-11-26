# TODO — the two coordinate systems, and why the loop kept failing

A record of a failure, written down so it is not repeated. This file is not an
apology; it is a defect report against the *agent loop itself*, which lacked the
vocabulary to see the error while it was making it.

---

## 1. The two terms, stated once and for all

| | **ink-space** | **screen-space** |
|---|---|---|
| Origin | the canvas's own top-left corner | the screen's top-left pixel |
| Unit | a fraction of the canvas CSS box (`rx`, `ry` in [0,1]) | one physical screen pixel |
| Range | 0..1 per axis | 0..1919 x 0..1079 on this rig |
| Written by | the page's `pos()` — the trace file | `mouse abs move` / `mouse pos` |
| Read back by | the client file in `strokes.d` (newest by mtime) | `kvm mouse pos` |

They are bridged by exactly one quantity, the pad's **anchor**: the *screen*
coordinate of the canvas's top-left corner.

```
screen = anchor + ink * extent       # ink-space -> screen-space
ink    = (screen - anchor) / extent  # screen-space -> ink-space
```

`extent` = the canvas's on-screen size, measured once (553 x 553 on this rig) and
recorded in `scripts/pad_calib.json`. The **anchor** must be MEASURED — by
decoding the page's painted corner markers (`scripts/pad_bbox.py`), or by an
absolute move -> click -> read `rx,ry` -> solve — never inferred from the very
points it is meant to explain, and never asked of the server.

## 2. Failures this session, each of which was a language failure before it was
   an arithmetic failure

1. **Inset error** — reported a stroke's start as "start(px)=(161.5,189)" and
   compared it to a pointer at screen (1229,108). `rx*454` is an inset from the
   canvas corner; it is not a screen coordinate. The comparison was meaningless
   and I did not notice, because the loop had no term for "which space is this
   number in".
2. **Anchoring error** — invented anchors of 978, 1117 and 1120 screen px for the
   pad, each fitted FROM the trace, then used to judge the trace. Circular, and
   each one was wrong; the window was at screen x=650.
3. **Extent error (avoided only by accident)** — the box is 454x454 today, but a
   `max-width`/`max-height` squeeze makes it non-square, and a single
   px-per-cell was already known to be the wrong model from an earlier
   measurement (6.00 px/cell across vs 4.47 down).
4. **Space-mixing in a formula I wrote down and still got wrong** — recorded in
   the pre-compaction notes as "do not add `box.x` on top of `rx*box.w`
   (double-count)", against README instructions that require `box.x` exactly
   once. Two documents contradicted each other and nothing in the loop checked
   which space each term was in.
5. **Repeating it after being told** — the user said, in words, that the trace
   is relative to its own left-top corner and the mouse is relative to the
   screen. The next turns still mixed them, because the correction was stored as
   prose rather than as a named quantity with a formula.

## 3. The root cause: the loop has no type for "a number with a space"

Every number crossing a tool boundary in this session was a bare integer pair.
`(1229, 108)` and `(161.5, 189)` are both "a point", so nothing objected when
they were subtracted. The failure is exactly the class a **unit type** prevents
in a compiler: the loop needed `Point<Screen>` and `Point<Ink>`, and had
`Point`.

## 4. The mechanism a future loop needs (the "new tool / new mechanism" ask)

Recorded as a concrete proposal, not a wish:

* **P1 — Quantity-tagged values.** Every coordinate-bearing tool result should
  carry its space explicitly, e.g. `mouse pos` -> `{"screen": [1229,108]}`,
  the file in `strokes.d` (newest by mtime) -> `{"ink": {"rx":0.2412,"ry":0.3150,"extent":[454,454]}}`,
  `/api/map` -> `{"screen": [x,y], "ink": {...}, "cell": [cx,cy]}`. A result
  that cannot name its space is a bug in the tool, not a choice for the caller.
* **P2 — One conversion tool, not N inline formulas.** `kvm pad anchor` /
  `kvm pad to-screen` / `kvm pad to-ink`, each requiring the anchor as an
  explicit argument, so a conversion cannot happen without the anchor being
  present and measured.
* **P3 — Anchor must be a measured record.** `/api/anchor` returns the anchor
  and the residuals of the clicks it was solved from, or returns
  `{"anchor": null, "why": "no calibrated click yet"}`. A consumer then cannot
  silently use a guessed origin — the same discipline `/api/map` already applies
  by returning HTTP 400 when `ox`/`oy` are missing.
* **P4 — Space check at the boundary the loop actually crosses.** The agent
  harness should reject an intent that names a tool and passes numbers whose
  space differs from the tool's declared space (e.g. feeding `ink` values to
  `mouse abs move`, or `screen` values to `/api/map` without `ox`/`oy`). The
  `mouse abs move` tool would declare `space=screen`; the trace reader would
  declare `space=ink`.
* **P5 — A stop-guard question before any geometry claim.** Before asserting
  "the ink is misaligned", the loop must answer, in one line: *"which space is
  each of the two numbers, and what is the measured anchor?"* If the anchor is
  not a measured number, the claim is not available.

## 5. What is still genuinely open (do not mistake this file for a fix)

- **The pad's anchor on this rig has never been measured.** Until it is, the
  original report — a click at the grid's left edge recording a point to the
  right of the cursor — cannot be settled, only explained.
- The three 10-point click strokes in `strokes.d/342dac2b-*.json` are the closest
  thing to calibration data, but they were taken while the window was at an
  unknown position and before `rx`/`ry` were persisted by the running server
  binary, so they cannot serve as the anchor sample.
- The heart draw is blocked on a *separate* measured fact, not on coordinates:
  ~35 ms of wall time per report (the `ackWaitMS = 60` ack wait in
  `internal/mouse/transport.go`), so a stroke must be priced in reports, not ms.

## 6. Cross-references

- `README.md`, *The inputpad -> Coordinates: ink-space and screen-space are NOT
  the same space* — the same ground truth, in the document a user reads.
- `scripts/heart_check.py` — shape verdicts from ink-space alone; it needs no
  anchor because it never leaves that space.

---

## 9. PROPOSED — not yet done (recorded so it is not lost)

Written down on the user's instruction: "put the proposal in the TODO md, do
not do it yet, finish the test first."

### 9.1 Page-side runtime diagnostics

The page has now failed to write a single event to its append log across five
reload cycles, while the served script demonstrably contains `push('load')`,
`push('down')`, `push('drag')`, `push('up')`, `/api/log` and `sessionStorage`.
The server contract is proven by hand-made POSTs (probe-1, probe-2). So the
fault is inside the page's own execution and is currently invisible.

Proposal (diagnostic only, no data-path change):

1. First line of the page script:
   `window.addEventListener('error', e => live.textContent = 'JS ERROR: ' + e.message + ' @' + e.lineno)`
2. `unhandledrejection` listener doing the same, so a rejected `send()` shows.
3. `Cache-Control: no-store` on the `/` handler, to rule out a cached script.
4. Surface `send()` failure text in the `live` element (it is currently only
   written on success).

Rationale: guessing has cost four failed attempts tonight (backtick-in-raw-string,
TDZ on `queue`, a blanket backtick sweep, a `git checkout` that discarded the
work). One error listener is cheaper than a sixth guess.

### 9.2 QR fiducial markers in the canvas

The user's design, recorded verbatim in intent:

- Three **QR-style** markers inside the canvas, one at three corners.
- They are **grey cells, not black**, and a pen stroke drawn over one **covers it**
  (black wins); an uncovered marker stays grey.
- Purpose: after a screenshot, a **script** (not a vision model) decodes them and
  derives the canvas's screen rectangle exactly — replacing the empirically
  back-solved anchor (1317,162), which was circular and is the same class of
  error as the three invented anchors already recorded in this project.
- Future use: the user can draw a QR code in blank space and scan it with a
  phone, closing the loop physically.

Open parameters, not yet decided:
- marker size in cells (9x9 suggested, unconfirmed)
- which three corners
- whether the observer must always exclude the marker cells from the density
  heat map (it should)

### 9.3 Test protocol to run once the page logs events

Certify strictly from `strokes.d/<client>.jsonl` — never from an API:

| # | check | criterion |
|---|---|---|
| 1 | boundary | events exist after the last `load` or `clear` line |
| 2 | event shape | each line is `{client,ts,kind,rx,ry}`; NO `points` array |
| 3 | stroke completeness | every `down` is followed by an `up` |
| 4 | **sub-region density** | per-cell point counts; expect exactly 5 non-empty letter bands |
| 5 | **aspect ratio** | I <= 0.25, Z >= 0.8, b/r 0.3-0.7, a 0.4-0.8 |
| 6 | letter boundaries | x histogram valleys; expect 4 gaps for 5 letters |
| 7 | stroke count | a letter must not take 60 strokes (measured failure before) |
| 8 | direction | Z diagonal must have NEGATIVE dx (down-left) |
| 9 | **total duration** | 5 letters in under 10 seconds, start of first `down` to end of last `up` |
| 10 | button released | the last event for the client must be `up`, not a stranded `down` |
