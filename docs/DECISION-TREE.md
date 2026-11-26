# Decision Tree — what was tried, what it proved, what is still open

A record of every avenue attempted on the CH9329 pad-drawing problem, the evidence
that killed or confirmed it, and where it led. Written because the same wrong
theories were re-derived more than once, and each cost hours.

Legend: **[X]** falsified · **[OK]** confirmed · **[?]** still open

---

## A. Is the device reachable at all?

| # | Hypothesis | Test | Result |
|---|---|---|---|
| A1 | The chip is not present | `kvm mouse pos` | **[OK]** returns a live coordinate (e.g. `430 939`). Device is there. |
| A2 | The serial port is wrong on Windows | `config.Defaults()` GOOS branch | **[OK]** `COM3`. No override needed. |
| A3 | `go build ./...` is a sufficient gate | build with `cmd/kvm-crosshair` broken | **[X]** false green — it exits 0 while silently omitting a package with no matching files. **Narrow per-package `GOOS=… go build <pkg>` is the only trustworthy gate.** |

**Led to:** A3 recorded as a pitfall; all later verification used narrow builds.

---

## B. Does absolute placement work?

| # | Hypothesis | Test | Result |
|---|---|---|---|
| B1 | Absolute reports are unreliable | `abs move 640 400` then `pos` | **[X]** lands `639 400` — sub-pixel, deterministic. |
| B2 | Absolute placement can carry a held button | `abs move` while `down` | **[X]** refused with `ErrAbsWhileHeld`. Absolute frames **cannot** draw. |
| B3 | Absolute frames drop the button byte on the host | docs + wire evidence | **[OK]** an absolute drag shows 0 button events. Confirms B2 is right, and is why ink must be relative. |
| B4 | A separate process can send `abs move` mid-stroke | reason about held state | **[?]** held state dies with the process, so `ErrAbsWhileHeld` is **in-process only** — a second process can teleport the pointer under a held button (sim-b JUMP-1). Not yet fixed. |

**Led to:** ink is relative-only; placement is absolute-only. Both are correct.

---

## C. Why does one held stroke become many tiny strokes? (the long detour)

| # | Hypothesis | Test | Result |
|---|---|---|---|
| C1 | The host accelerates bursts, so reports are skipped | vary `-gap` | **[X]** split count identical at 16/6/2 ms. Pacing is not the cause of *fragmentation* (it is the cause of *acceleration*). |
| C2 | The pad server is stale and must be rebuilt | inspected `rel.go` vs `cmd/kvm-inputpad` | **[X]** `rel.go` is CLI-side; the page source was unmodified. Restarting would have proven nothing. |
| C3 | Frame coalescing in the browser | `getCoalescedEvents` | **[X]** partial — coalescing exists but does not *end* strokes. |
| C4 | A suppressed release frame leaves the button latched | wire log | **[OK]** *contributed*: `strokeSeg` released via `streamAt(0,0,0,0)`, which returns early without writing. The chip kept the button DOWN. |
| C5 | The **page** infers a lift from a button bit | read `main.go:271` | **[OK]** `if(e.buttons === 0){ endOnce('stale-press', e); return; }`. A full-state HID packet with `buttons:0` is read as a pen lift; `if(!down) return` then drops **every** later sample. **This is the root cause of fragmentation.** |
| C6 | `setPointerCapture` makes the button heuristic necessary | read `main.go:389` | **[X]** capture already guarantees `pointerup`/`pointercancel`. The heuristic was both wrong and redundant. |

**Led to:** C5 + C6 fixed by deleting both `e.buttons === 0` terminators (canvas and
window). C4 fixed by routing `strokeSeg` through `release()`.

---

## D. Why did the pointer not land where it was commanded?

| # | Hypothesis | Test | Result |
|---|---|---|---|
| D1 | The screen→cell transform is wrong | probe grid + live press | **[X]** the fitted transform predicted cell (58,57) vs an actual (58,57). The transform was right; the input was wrong. |
| D2 | `holdseg` is a goto | `holdseg 1250 200 → 1450 200` from an unverified start | **[X]** it is **relative**; the name misleads. The "under-delivery" was a wrong assumption about the start point. |
| D3 | Held delivery is ~50 % | measure from a **verified** start | **[X]** 1:1 within ±2 px up to ~110 px commanded, degrading past that. The 50 % readings were all invalid. |
| D4 | The degradation is a pacing effect | vary gap at fixed length | **[X]** it is a **report-size** effect: 120 → one 120 px report → 101 delivered. |
| D5 | The step bound is per axis | `(300,300)` held | **[OK]** `n = ⌈max(|dx|,|dy|)/127⌉` → reports of `(100,100)` = **141 px of travel**, and `(127,127)` = 179.6 px. Law violation (sim-d D1). |

**Led to:** `evenReports` now splits by **vector length** at `heldStepPx = 20`.
The law lives in `internal/mouse/leg.go` (`evenReports`, the `heldStepPx`
constant block) and is pinned by `internal/mouse/law_test.go`; there is no
`docs/movement-law.md` in this tree.

---

## E. Is the feedback loop real?

| # | Hypothesis | Test | Result |
|---|---|---|---|
| E1 | `DragTo` is a closed loop | read `absops.go` | **[X]** the comment says closed-loop; the body samples once and streams. **Open loop with a closed-loop comment.** |
| E2 | `walkTo` converges | read `leg.go` | **[X]** deliberately de-looped ("ONE emission per request"). The doc 10 lines above still claims convergence. |
| E3 | `settleHeld` corrects the endpoint while held | read `leg.go` | **[X]** the function no longer exists: what remains is a dangling comment (`leg.go:238`, "settleHeldWithin is settleHeldSlow bounded…") immediately above `wireSettled`. `settleHeldBounded`/`settleHeldSlow` are gone too. Dead comment, live file. |
| E4 | `landOn` corrects after the segment | read `leg.go` | **[X]** *was* true, is not anymore. `landOn` (`leg.go:199`) now opens with "NO POST-STROKE CORRECTION", discards its argument (`_ = target`) and returns `nil`; the `landPasses` loop was deleted because with bounded vector steps and ACK pacing an open-loop stroke already ends where its last report ended, so the correction could only ADD motion. `DrawStep` still *calls* it, so the name is now the only trace of the behaviour. |

**Led to:** the "uncontrollable" feel is explained — almost nothing corrected anything,
and what did correct drew with the button down.

---

## F. Fixes applied, each traceable

| Fix | Finding | Where |
|---|---|---|
| split by vector length, `heldStepPx = 20` | sim-d D1 / sim-a CRITICAL-2 | `leg.go` `evenReports` |
| release through `release()` | sim-a CRITICAL-1 | `rel.go` `strokeSeg` |
| clear `down` only after a successful write | sim-b CRITICAL-2 | `rel.go` `strokeSeg` |
| `Home` steps at 20 px, not 179.6 | sim-b CRITICAL-4 | `rel.go` `Home` |
| delete both `e.buttons === 0` terminators | sim-c P0-1 | `cmd/kvm-inputpad/main.go` |
| right/middle button support | user request | `rel.go` `SetButton`, `-button` |
| `rdown` / `rup` as first-class verbs | a right-click is an action, not a mode | `internal/cli/parse.go` (`RDown`/`RUp`), `cmd/kvm/exec.go` |
| formal law + property tests | user request | `internal/mouse/leg.go` (`evenReports`, `heldStepPx`), `law_test.go` (no `docs/movement-law.md` in tree) |

## G. Open / not yet fixed

- **G1** `os.Exit(3)` still skips the deferred `dv.Close()`/`dv.Release()`; the
  *button* is no longer at risk on the error paths — `runFire` calls
  `rel.ForceRelease()` before each exit that could leave a press on the wire
  (`cmd/kvm/exec.go`), and the end-of-sequence guard releases and says so. What
  remains is the port not being closed cleanly.
- **G2** `maxVerbs` clamp re-widens steps to 47 px while held (sim-b CRITICAL-3).
- **G3** `send()`/`stream()` bypass `writeReport` → no ACK gate, no 8 ms floor for `Down`/`Up`/`Home`.
- **G4** ACK read result discarded → "ACK-gated pacing" is really just the 8 ms floor (sim-d D5).
- **G5** page epoch is decorative; post-clear `record = []` deletes live ink; retry can resurrect a cleared stroke (sim-c).
- **G6** no continuity guard in the page (≤20 px / ≥8 ms unenforced at the only place both delta and timestamp exist).
- **G7** `ErrAbsWhileHeld` is in-process only.
- **G8** ~15 stale comment blocks, chiefly `abs.go`/`device.go` claiming "nothing sends 0x04" while `GoTo` does.

## H. Standing rules learned the hard way

1. **Measure from a verified start point**, or the measurement is fiction (D2/D3).
2. **A comment is not evidence.** Four separate behaviours were asserted in comments
   that the code did not implement (E1/E2/E3, C4).
3. **`go build ./...` is not a gate** (A3).
4. **A vision model is not a witness** on a dense grid canvas — it called a drawn Z
   "blank". Pair it with a pixel diff.
5. **Do not restart a server to explain a client bug** (C2).
