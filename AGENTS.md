# AGENTS.md — standing rules for agents working in this repository

These rules are **binding**. They were each stated by the user repeatedly because they
were repeatedly violated. Read them before touching the mouse, the pad, or a browser.

---

## 1. The input pad is the one inside VS Code. Do not open another.

The canonical input pad is the page serving `127.0.0.1:8898` **as embedded in the VS Code
webview pane** (`cmd/kvm-inputpad`, the "Inputpad" tab). That is the pad every measurement
refers to.

**Do not:**
- open the pad in Edge, Chrome, or any other browser window;
- launch `--new-window`, `--app=`, or a second tab for the pad;
- start extra `kvm-inputpad` instances on spare ports to "get a window I control";
- close or restart the pad, or reload its page, without being asked.

Opening a second pad does not give a better measurement. It gives a **different pad**, with
a different client id, a different canvas rect, and a different anchor — which is how earlier
runs drew into a page nobody was watching.

---

## 2. Do not scan the pad by moving the mouse.

Never locate or probe the canvas by moving the pointer across it and reading back `rx`/`ry`.
Calibration clicks are **ink**. They leave stray marks on the pad (the user has already caught
a stray horizontal line produced exactly this way).

**Do not:**
- sweep the pointer over the pad in a loop to find edges;
- take repeated full-screen screenshots and analyse white-pixel runs to find the canvas;
- perform multi-click calibration sweeps.

**Instead:**
- get the canvas rect by **measuring it, never by asking the server**: decode the three
  grey QR finder patterns the page paints at TL, TR and BL with `scripts/pad_bbox.py`
  (a screenshot), or read the left/top already recorded in `scripts/pad_calib.json`.
  An anchor back-solved from the trace is circular and has been wrong three times;
- the pad has **no read-back API** — do not add one and do not use `GET /api/box` or
  `GET /api/map` to learn where the canvas is. Those endpoints describe the page in its
  own viewport coordinates, which are neither screen pixels nor trustworthy, and a
  wrong answer from them is indistinguishable from a right one;
- if a single confirmation click is truly needed, make **one** click, not a scan, and clear
  the trace immediately afterwards.

---

## 3. Measure once, and never inside the measured run.

- Snapshot the trace, then clear it, then draw — no calibration ink may survive into a run
  that will be judged.
- One anchor, validated once on a held-out point, is enough. Re-solving the anchor from the
  same clicks it is meant to explain is circular and has produced several wrong origins.
- Verify after drawing from the **client's own trace file** (newest by mtime) **and** the
  `events.jsonl` tail — in one batched read, not one call per turn. There is no read-back
  endpoint: the pad POSTs records to `/api/log` and `store.go` appends them to disk.

---

## 4. Report honestly; do not declare a pass you cannot evidence.

- If an endpoint is more than ~5 px off, say so; do not call the glyph drawn.
- If a run measured the wrong build, the wrong window, or a stale client file, mark it
  **void** and say which of those it was.
- `go build ./...` exiting 0 while a package is excluded by build tags is a false green;
  narrow builds are the honest gate (`DECISION-TREE.md` A3).
- The store preserves stroke grouping (`append(have, s)`); a hand-POSTed multi-point stroke
  round-trips intact. If strokes arrive split, the split is upstream, not in `store.go` —
  check before blaming it.

---

## 5. Working notes that save re-derivation

- The pointer witness on Windows is `GetCursorPos` (`internal/mouse/shellpos_windows.go`);
  there is no ring.
- Delivered travel depends on **report size**, not on a constant gain: 60 px reports ≈ 1:1,
  12 px ≈ 1:1, 6 px ≈ 0.66, 3 px ≈ 0.01. Never "fix" a shortfall by doubling the request —
  that lands back in the lossy band. See `internal/mouse/leg.go` and `PITFALLS.md` §6.
- `heldStepPx = 20` subdivides held drags, so a `drag 45` becomes ~3×15 px reports.
- The stroke record lives **only on disk**: `<out>/<client>.jsonl` holds one record per
  line with kinds `load`, `down`, `drag`, `up`, `clear`; `events.jsonl` holds the per-action
  ledger. The file shape is the authority — read the file, not a summary, when grouping
  matters. There is no read-back endpoint; do not reintroduce one.
- `.mycc/` is runtime state and must never be committed.
