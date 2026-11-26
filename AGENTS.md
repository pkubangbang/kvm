# AGENTS.md — standing rules for agents working in this repository

These rules are **binding**. They were each stated by the user repeatedly because they
were repeatedly violated. Read them before touching the mouse, the pad, or a browser.

---

## 1. The input pad is the one inside VS Code. Do not open another.

The canonical input pad is served by `cmd/kvm-inputpad` on `127.0.0.1:8899` (the flag default;
AGENTS.md used to say 8898 — the real listener is whatever `netstat`/`Test-NetConnection`
shows) **as embedded in the VS Code webview pane** (the "Inputpad" tab). That is the pad every
measurement refers to.

**Do not:**
- open the pad in Edge, Chrome, or any other browser window;
- launch `--new-window`, `--app=`, or a second tab for the pad;
- start extra `kvm-inputpad` instances on spare ports to "get a window I control".

**Reloading and restarting ARE allowed** (the user's standing grant: "you have the right to
do anything on this repository"). What the old wording forbade — "without being asked" —
was misread as a permanent ban and cost a whole session. Read it as: if the page looks
wrong or stale, fix it, do not ask permission you already have.

**THE STALE-BUILD TRAP — read this before blaming code or the pointer.** `page.html`,
`pad-core.js` and `pad-events.js` are `//go:embed`-ed into the binary (`main.go:413+`).
The server hands out the page it was **compiled with**, not the one on disk. Editing
`page.html` changes nothing until `kvm-inputpad` is **rebuilt and restarted**. Measured on
this rig: the running process had started 13:59:05 while `page.html` was modified 14:17:44,
so the webview served an eighteen-minute-stale page all session — four corner markers when
the source painted three. Reloading the webview (`epoch` ticks, page re-initialises) is
**not** proof of new code: it re-fetches the same embedded bytes.

To prove the running build really is current, in this order:
1. `Get-Process kvm-inputpad` — its `StartTime` must be LATER than the mtime of every
   embedded asset;
2. fetch `http://127.0.0.1:8899/` and grep the served text for the change you expect;
3. only then reload the webview.

The `epoch` number in `strokes.d/<client>.jsonl` proves the page RE-INITIALISED. It does
NOT prove which build it initialised into.

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
  grey QR finder patterns the page paints at TL, TR and BL with `pad-bbox -grab -bbox`
  (in-process GDI capture on Windows; `-shot FILE` reads a PNG elsewhere). An anchor
  back-solved from the trace is circular and has been wrong three times;
- a stored calibration is **not a safe fallback**. The old `scripts/pad_calib.json` was
  exactly that and is now deleted: measured on this rig, a stroke aimed with it landed
  ~150 px from where the pointer actually went. Re-measure the rect before every draw and
  never keep an anchor for reuse — you cannot know the window was not resized;
- **`internal/capture` has no Windows implementation.** The library has only the
  interface (`Unsupported`, which reports "unsupported on this platform") plus
  `capture_x11_linux.go`. The fiducial route on this host is `cmd/pad-bbox`, whose
  `-grab` mode BitBlts the screen itself on Windows (DPI-aware, ~20 ms) instead of going
  through `internal/capture`; do not add a second capture implementation;
- the pad has **no read-back API** for the canvas rect — do not add one. The
  read-back endpoints (`/api/box`, `/api/map`, `/api/whoami`) have been REMOVED
  from `cmd/kvm-inputpad`; they described the page in its own viewport
  coordinates, which are neither screen pixels nor trustworthy, and a wrong
  answer from them was indistinguishable from a right one. Do not reintroduce
  them, and do not grow a replacement.
  (`POST /api/log`, page → disk, and `GET /`, the page itself, are the only
  endpoints left, and they are how every verification in this repo is done);
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
  narrow builds are the honest gate (`decision-tree.md` A3).
- The store preserves stroke grouping (`append(have, s)`); a hand-POSTed multi-point stroke
  round-trips intact. If strokes arrive split, the split is upstream, not in `store.go` —
  check before blaming it.

---

## 5. You are a vision + input agent. Drive the desktop yourself.

This agent is **not** a text-only assistant. It is an RPA-style operator with two
capabilities that make the desktop its own to drive:

- **Vision** — the `screen` tool captures the desktop and describes what is on it. That
  is how screenshots are taken here. **Do not write screenshot/capture code** into this
  repo to get one (the user's words: "你不需要去写任何有关屏幕截图的脚本. MyCC 已经为你提供了
  足够的工具"). `internal/capture` stays X11-only and stays alone.
- **Input** — the repo's own binary moves the pointer and clicks: `kvm mouse abs move X Y`,
  then `kvm mouse rel down up`. (Use the built
  `kvm.exe`; there is no Python driver.) `kvm mouse pos` reads the pointer back.

Because both halves are present, the agent is expected to **close the loop on its own**:
find the window with `screen`, activate it (click the tab, or the VS Code icon on the
taskbar), perform the action, then `screen` again and read the result back. This was
demonstrated on this rig: `kvm mouse abs move 270 55` + a click switched VS Code to the
`kvm inputpad 128x128` tab, and `screen` confirmed the pad had rendered.

**Asking the user to click, switch tabs, or take a screenshot is a failure mode**, not a
courtesy. Do it yourself unless it is genuinely impossible (e.g. a UAC/elevation prompt,
a password field, or a locked workstation). "I can't see the screen" is not such a case —
that is what `screen` is for.

Two boundaries that still hold: driving the desktop does **not** license §2 (no pointer
sweeps to scan the pad, no calibration ink left behind), and it does **not** license
opening a second pad (§1).

---

## 6. Working notes that save re-derivation

- The pointer witness on Windows is `GetCursorPos` (`internal/mouse/shellpos_windows.go`);
  there is no ring.
- Delivered travel depends on **report size**, not on a constant gain: 60 px reports ≈ 1:1,
  12 px ≈ 1:1, 6 px ≈ 0.66, 3 px ≈ 0.01. Never "fix" a shortfall by doubling the request —
  that lands back in the lossy band. See `internal/mouse/leg.go` and `pitfalls.md` §6.
- `heldStepPx = 20` subdivides held drags, so a `drag 45` becomes ~3×15 px reports.
- The stroke record lives **only on disk**: `<out>/<client>.jsonl` holds one record per
  line with kinds `load`, `down`, `drag`, `up`, `clear`; `events.jsonl` holds the per-action
  ledger. The file shape is the authority — read the file, not a summary, when grouping
  matters. There is no read-back endpoint; do not reintroduce one.
- `.mycc/` is runtime state and must never be committed.
