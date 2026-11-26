# usage.md — the common commands, and the conventions behind them

Read this before driving the mouse. It is written for an agent: every rule here
exists because breaking it produced a wrong measurement or a stray mark on the
pad.

Two programs: **`kvm`** (the CLI) and **`kvm-inputpad`** (the page that records
ink). Build both with `go build ./...`.

---

## 1. The one convention that matters

**Place with `abs`. Ink with `rel`.** They are separate namespaces and mixing
them is the single most expensive mistake in this project.

```bash
kvm mouse abs move X Y          # screen pixels, exact, no button
kvm mouse rel down drag DX DY up  # relative, holds the button
```

`abs` cannot hold a button (the chip's 0x04 report has no button byte), and
`rel` cannot address the screen (it only knows deltas). So a stroke is always:
**`abs` to the start point, then one `rel` span.**

### Every move reports where it landed

`move` prints the measured pointer position, so a command proves itself instead
of being taken on trust:

```
$ kvm mouse abs move 1342 173
abs move 1342,173 -> 1342,173          # residual 0: placement is exact

$ kvm mouse rel move 20 0
rel move +20,+0 -> 1222,600            # asked +20, moved +22
```

Absolute placement lands where it is told, and the read-back shows it. A
relative move prints the position it reached, so a shortfall is visible at the
moment it happens rather than being discovered three steps later. Read this line
before concluding anything about delivery: the pointer's real travel is printed,
and the residual is printed too when an absolute move misses.

The witness is `GetCursorPos` on Windows. It is honest for a placement (nothing
is in flight) and unreliable mid-stroke (it updates once per compositor frame,
~16 ms), which is why the read-back is attached to moves and not to held spans.

---

## 2. `kvm` — the CLI

```
kvm [-port PORT] [-baud N] mouse <verb>...
```

| Command | What it does |
|---|---|
| `mouse abs move X Y` | place the pointer at screen pixel (X,Y) |
| `mouse abs holdseg X Y U V` | one held segment from (X,Y) to (U,V), then a button-up landing |
| `mouse rel move DX DY` | move the pointer (button up) |
| `mouse rel down` / `up` | press / release the left button |
| `mouse rel rdown` / `rup` | press / release the right button |
| `mouse rel drag DX DY` | move with the button held — repeatable inside a span |
| `mouse rel wait MS` | sleep inside a span |
| `mouse pos` | print where the pointer is |
| `mouse hud on` / `off` | the crosshair overlay |

The verb set is **closed**: `move`, `down`, `up`, `rdown`, `rup`, `drag`,
`wait`, plus `pos` and `hud`. There is no `probe`, no `plan`, no `fire`.

### Flags

| Flag | Position | Meaning |
|---|---|---|
| `-port PORT` | **before `mouse`** | serial port, overrides discovery |
| `-baud N` | **before `mouse`** | baud rate |
| `-button L\|right\|middle` | anywhere | which button `down`/`drag` carries (default left) |
| `-human[=bool]` | anywhere | human-motion profile (default on for ink) |
| `-origin` | anywhere | home to the top-left corner first |
| `-dry-run` | anywhere | predict and print, send nothing |

`-port` is **top-level**: `kvm -port COM3 mouse abs move 900 400` works;
`kvm mouse abs -port COM3 move 900 400` fails with `unknown verb "-port"`.

`-ease` and `-step` were removed. They were accepted but inert — `-step` was
parsed into a field nothing read, and `-ease` was only echoed into the
`-dry-run` line. Report size is now the single constant `mouse.HeldStepPx`
(20 px), and the dry-run estimate uses that same constant, so the prediction
and the emitter cannot disagree.

### A stroke, in full

```bash
kvm mouse abs move 1200 400
kvm mouse rel down drag 45 0 drag -45 45 drag 45 0 up
```

Press and release go in the **same process**. A line left open is completed
with an appended release rather than refused.

### Predict before you send

```bash
kvm mouse rel -dry-run down drag 45 0 drag -45 45 up
```

Nothing is sent. Use it to check the arithmetic and the report count.

---

## 3. Report size is a delivery law, not a preference

Measured on this rig — the fraction of requested travel that actually arrives
depends on the size of each report:

| Report size | Delivered |
|---|---|
| 60 px | ~1.01x |
| 12 px | ~1.07x |
| 6 px | ~0.66x |
| 3 px | ~0.01x |

Held drags are split into reports of at most `HeldStepPx` (20 px) by vector
length, so a `drag 45 0` becomes about three 15 px reports — inside the band
that arrives. A `drag 6 0` becomes ~4 px reports and loses about half.

**Never fix a shortfall by doubling the request.** That lands back in the lossy
band and overshoots. Move the report size, not the distance.

---

## 4. `kvm-inputpad` — the pad that records ink

```bash
kvm-inputpad -addr 127.0.0.1:8899 -out strokes.d
```

| Flag | Default | Meaning |
|---|---|---|
| `-addr` | `127.0.0.1:8899` | listen address |
| `-out` | `strokes.d` | directory of one trace file per client + `events.jsonl` |

### HTTP surface

| Endpoint | Meaning |
|---|---|
| `GET /` | the page (draw on the canvas) |
| `POST /api/log` | append records (`load`, `down`, `drag`, `up`, `clear`) for a client; `store.go` writes them to `<out>/<client>.jsonl` |

That is the whole surface the toolchain uses. The page has **no read-back
endpoint** and must never grow one: the record lives on disk, and a reader that
asks the server instead cannot tell a stale answer from a live one.

(The server still exposes `GET /api/box` and `GET /api/map` for the page's own
bookkeeping, but nothing in `scripts/` or the CLI may call them. They describe
the canvas in the page's viewport coordinates, which are neither screen pixels
nor independently verifiable; `scripts/pad_bbox.py` measures the canvas from the
painted corner markers instead.)

### Why the canvas rect is measured, not fetched

The pad is a **VS Code webview pane** on this rig, so the page's own rect
(viewport CSS px) and screen px differ by more than a thousand pixels. Feed a
viewport rect to `mouse abs move` and you will draw outside the canvas. Decode
the three grey QR finder patterns at TL, TR and BL from a screenshot
(`pad-bbox -grab -bbox`, or `scripts/pad_bbox.py --json` for a PNG you already
have) and you get the canvas origin in screen px directly, with no click and no
back-solved origin. `-grab` takes the screenshot itself on Windows, which is
~80x faster than piping `ffmpeg -f gdigrab` through a PNG on disk: measured
~20 ms of capture in a 50-66 ms run, against ~2066 ms for capture plus decode.

### A trace file

An array of strokes; each stroke is an array of points:

```json
[[{"ts":123,"x":64,"y":32,"fx":0.5,"fy":0.25,"rx":0.5,"ry":0.25}]]
```

`rx`/`ry` are **pure ratios of the canvas CSS box** in [0,1] and are
authoritative. `x`/`y` are a 128-cell lattice derived from them. Convert with
`rx * box.w` for x and `ry * box.h` for y — **one divisor per axis**, because
the canvas may be non-square.

### `events.jsonl`

One line per action: `client-new`, `strokes`, `clear`. A clear event now records
what it destroyed:

```json
{"event":"clear","client":"…","epoch":3,"cleared_strokes":11,"cleared_points":137}
```

### Client ids are per page load

The page mints a fresh id each load and **stores it nowhere** — no
`localStorage`, no `sessionStorage`. So a reload starts a new trace file, and
the file a draw landed in is identifiable by mtime alone. Do not assume a
client id survives a reload.

---

## 5. Rules for measuring

1. **The pad is the one inside VS Code.** Do not open another window, tab, or
   spare port for it. A second pad is a different pad with a different client
   id and a different anchor.
2. **Never locate the canvas by moving the pointer across it.** Calibration
   clicks are ink. Measure the canvas instead: decode the three grey QR finder
   patterns at TL, TR and BL (`pad-bbox -grab -bbox`, or `scripts/pad_bbox.py`
   for a PNG). There is no read-back endpoint to consult, and asking the server
   would not be evidence. `scripts/pad_calib.json` records an anchor measured at
   some past moment — validate it, never draw with it unconfirmed.
3. **No calibration ink in a measured run.** Snapshot, clear, draw, verify.
4. **Verify from the file, not the ledger.** Read the client's trace file and the
   `events.jsonl` tail together. There is no read-back endpoint: the page POSTs to
   `/api/log` and the server appends to disk.
5. **If a run measured the wrong build, window, or client, mark it void** and
   say which.
