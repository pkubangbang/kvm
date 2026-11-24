# Zibra-Z.md — draw the Z on the pad, fast, with certifiable evidence

Target: the letter Z with vertices **(1100,200) → (1400,200) → (1100,500) → (1400,500)**
inside the pad grid **x 1057–1824, y 182–955**.

Principle: **no feedback loop inside the stroke.** Reports are ≤6 px (libinput's
1:1 plateau), the chip is polled at 1 ms, and there is exactly one verification
at the end. Homing is out (it does not clamp); `abs` establishes the start.

---

## Step 0 — Preconditions (fail loudly, do not proceed)

1. `ss -ltnp | grep 8898` — the pad server is listening. If not:
   `./kvm-inputpad -addr 127.0.0.1:8898 -box 1057,182,767,773 -out /tmp/pad-strokes.json`
2. `go build ./...` is green and `./kvm` is the freshly built binary.
3. Ring liveness: read the ring, send `rel move 20 0`, read again — the
   position **must change**. If it does not, stop.

## Step 1 — Clean up and certify the slate

1. `curl -s :8898/api/trace > /tmp/trace.before` — the baseline to compare against.
2. Click Clear: `kvm mouse abs move 1420 150` then `kvm mouse rel down up`;
   screen probe: the canvas is blank. (The "N strokes exported" label does not
   reset — do not read it as canvas state.)
3. `mv /tmp/kvm-pointer.pos /tmp/kvm-pointer.pos.bak` — cold start for the
   tracker (optional; the ring is the truth either way).

Clear wipes the canvas only. It does **not** wipe the server's last trace,
which `GET /api/trace` keeps serving from memory. That is why the snapshot in
step 1 exists.

## Step 2 — Park the pen

```
kvm mouse abs move 1100 200
kvm mouse pos          # must read 1100,200 (±5) and lie inside 1057–1824 × 182–955
```

If it is outside the grid or off by more than ~10 px: re-issue once, then stop
and report. Never press on an unconfirmed position (pitfall `e022103e`: that
drags the browser window).

## Step 3 — Draw, one `down … up` span, ≤6 px steps at `-gap 2`

```
kvm mouse rel -gap 2 down \
  drag 6 0   (×50) \
  drag -6 6  (×50) \
  drag 6 0   (×50) \
  up
```

150 reports, about 0.3–0.5 s. Only `drag` is legal inside the span (`move` is a
parse error while the button is down); the parser appends `up` if it is omitted.

## Step 4 — Verify (all three must pass)

1. **Wire** — the ring endpoint is ≈ (1400,500) within a few px, and different
   from its pre-test value.
2. **Server** — `curl -s :8898/api/trace` differs from `/tmp/trace.before`, and
   its first point maps to the pen-down cell of (1100,200).
3. **Screen** — the Z is visible and the red zones are blank:
   - R1 (1405,150)–(1824,250) — overshoot past the top-right corner
   - R2 (1405,250)–(1824,495) — drift right of the top bar
   - R3 (1057,505)–(1824,955) — landing below the bottom bar
   - R4 (1057,182)–(1095,955) — rebound left of the start column
   - R5 (1057,182)–(1824,195) — rebound above the top bar
   - R6 (1095,205)–(1395,495) — wrong side of the diagonal (below-left of it)
   - R7–R10, everything outside the pad — ink there means a window drag, not a
     stroke, so the run measures nothing about the glyph.

## Step 5 — Report

The measured endpoint, the trace diff, and the red-zone verdict. If the endpoint
is more than ~5 px off, say so; do not call the glyph drawn.

---

## Escape hatches

- Ring frozen at any point → abort, report "observer dead". No drawing on a
  stale feed.
- Park lands outside the grid → do not press.
- Trace unchanged after drawing → the stroke never reached the page; that is a
  failure even if ink looks present.

## Out of scope (deliberately)

No per-report feedback, no gain estimation, no dead reckoning, no homing, no
`StrokeHeldFast` redraw retries. Those are what made the previous attempts slow
and wrong.
