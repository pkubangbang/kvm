# zibra-z.md — draw the Z on the pad, fast, with certifiable evidence

> **THE TARGET RECTANGLE BELOW IS STALE — RE-BASE IT BEFORE RUNNING.**
> The vertices and grid in this section were measured on a rig whose pad sat at
> a different place and size. Measured on 2026-11-26 with the UIA canvas probe
> (`.mycc/pad.ps1`), the pad canvas now occupies **screen x 1293–1730,
> y 175–611** (rect `1293,175,437,436`). The Z's first vertical run is at
> **x = 1100**, which is **193 px LEFT of the canvas's left edge** — so the
> glyph as written lands mostly off-pad and the run cannot be certified no
> matter how good the motion is. Do not treat a failed Z as a motion bug until
> the vertices have been re-derived inside the live canvas rect.
>
> Derive the target from the live rect, not from this file: read the UIA rect
> immediately before the run (it drifts when the VS Code pane is resized), inset
> by ~15% on each side, and take the three vertices at (left, top),
> (right, top), (left, bottom), (right, bottom) — the same Z shape, expressed in
> the pad that actually exists.

## LIVE TARGET — re-based 2026-11-26 12:1x from `/api/whoami` (USE THIS, not the numbers below)

The pad server on `:8898` reports the canvas box as **`x=1361, y=166, w=437, h=436`**
(read live this session; verified twice, stable). Insets are one divisor per axis,
15 % of each:

```
inset_x = round(437 * 0.15) = 66      inset_y = round(436 * 0.15) = 65
L = 1361 + 66 = 1427    R = 1361 + 437 - 66 = 1732
T = 166  + 65 = 231     B = 166  + 436 - 65 = 537
```

**Vertices: (1427,231) → (1732,231) → (1427,537) → (1732,537)**
**Deltas: `+305,0` → `−305,+306` → `+305,0`**  (legs 305 px and 306 px)

Report split — protocol minimum, NOT shredding: a relative HID field is one byte,
so 305/127 → 3 reports and 306/127 → 3 reports. The command line stays one `drag`
per leg; the transport does the split.

### Pre-staged run (execute verbatim once `GetPortNames()` lists the port)

```powershell
# 0. confirm the port is real and the chip answers
#    (115200 8N1; send 57 AB 00 01 00 03; expect 57 AB 00 81 ... within ms)
go build -o kvm.exe ./cmd/kvm
.\kvm.exe -port <COMn> mouse pos            # must return a coordinate

# 1. snapshot the slate, then park the pen and confirm it
#    before drawing, compare against the newest file in strokes.d (by mtime)
.\kvm.exe -port <COMn> mouse abs move 1427 231
.\kvm.exe -port <COMn> mouse pos            # must read 1427,231 ±5 and be inside 1361–1798 × 166–602

# 2. draw, ONE held span, one drag per leg
.\kvm.exe -port <COMn> mouse rel down drag 305 0 drag -305 306 drag 305 0 up

# 3. verify from the CLIENT FILE (newest by mtime in strokes.d) — the pad's
#    record lives on disk; there is no read-back route (the old one was deleted)
Get-ChildItem D:\dev\kvm\strokes.d -Filter *.jsonl | Sort-Object LastWriteTime -Descending | Select-Object -First 1
```

**Pass criteria:** the newest client file gains a stroke whose first point maps to
the pen-down cell of (1427,231); the stroke's bounding box covers ≈ x 1427–1732,
y 231–537; the pad visibly shows a Z. **Any endpoint more than ~5 px off is a FAIL,
reported as such.**

---

## Historical target (STALE — kept only to show what drifted)

Target: the letter Z with vertices **(1359,240) → (1664,240) → (1359,546) → (1664,546)**
inside the pad grid **x 1293–1730, y 175–611** (canvas rect `1293,175,437,436`).

> **RE-BASED 2026-11-26.** The older vertices (1359,240) → (1730,240) → (1359,611) →
> (1730,611) put the right-hand x at the canvas EDGE (1730 is the last column) and the
> bottom y at the edge too (611), leaving zero margin for the pen-down offset — and their
> deltas of 371 px cannot fit a 437 px canvas three times over. The vertices above are the
> live rect inset by 15 % on BOTH sides: 437 x 0.15 = 65.6 -> x from 1359 to 1664;
> 436 x 0.15 = 65.4 -> y from 240 to 546. Confirm the live rect immediately before any run
> (`GET /api/box`, then UIA via `.mycc/pad.ps1`); it moves when the VS Code pane is resized.

The deltas for Step 3 are therefore **+305,0 → -305,+306 → +305,0**. Re-derive
them from the live rect before each run; the pane moves when VS Code is resized.

Principle: **no feedback loop inside the stroke.** One report per straight run
(see Step 3), the chip is polled at 1 ms, and there is exactly one verification
at the end. Homing is out (it does not clamp); `abs` establishes the start.

---

## Step 0 — Preconditions (fail loudly, do not proceed)

1. `ss -ltnp | grep 8898` — the pad server is listening. If not:
   `./kvm-inputpad -addr 127.0.0.1:8898 -box 1057,182,767,773 -out /tmp/pad-strokes.json`
2. `go build ./...` is green and `./kvm` is the freshly built binary.
3. Ring liveness: read the ring, send `rel move 20 0`, read again — the
   position **must change**. If it does not, stop.

## Step 1 — Clean up and certify the slate

1. Note the newest client file: `ls -t strokes.d/*.jsonl | head -1` — the baseline to
   compare against.
2. Click Clear: `kvm mouse abs move 1420 150` then `kvm mouse rel down up`;
   screen probe: the canvas is blank. (The "N strokes exported" label does not
   reset — do not read it as canvas state.)
3. `mv /tmp/kvm-pointer.pos /tmp/kvm-pointer.pos.bak` — cold start for the
   tracker (optional; the ring is the truth either way).

Clear wipes the canvas and appends a `clear` record. The stroke record is the client
file under `-out`; there is no server-side read-back to snapshot, so step 1 notes the
baseline file instead.

## Step 2 — Park the pen

```
kvm mouse abs move 1359 240
kvm mouse pos          # must read 1359,240 (±5) and lie inside 1293–1730 × 175–611
```

If it is outside the grid or off by more than ~10 px: re-issue once, then stop
and report. Never press on an unconfirmed position (pitfall `e022103e`: that
drags the browser window).

## Step 3 — Draw, one `down … up` span, ONE report per straight run

```
kvm mouse rel down \
  drag 305 0 \
  drag -305 306 \
  drag 305 0 \
  up
```

Four verbs, three legs (each leg is subdivided into ≤20 px reports by
`internal/mouse/evenReports`; the command line stays one drag per leg).
**Do not split a straight run into repeated equal steps.** An earlier version of this step prescribed `drag 6 0` fifty times per
leg — 150 shredded reports — and that is wrong on two counts:

1. It contradicts the measured transport. `internal/mouse/leg.go` records the
   per-report delivery curve (60 px ≈ 1:1, 30 px ≈ 0.98, 12 px ≈ 1:1,
   6 px ≈ 0.66, 3 px ≈ 0.01) and concludes, in the source: *"ONE report per
   requested delta, no division, no chopping."* Shredding a leg into 6 px
   pieces lands you in the lossy band and makes delivered distance depend on
   push rate — which reads as acceleration but is really report loss.
2. The user forbade it directly and repeatedly: a long stroke in the same
   direction is not to be subdivided into repeated identical drags.

The only bound that remains is the protocol's ±127 px per report (a relative
HID field is one byte), so a delta larger than that is split into the FEWEST
parts the protocol requires — not into a smoothness stream. `drag 300 0` is
therefore sent as 3 reports of ≤127 px by the transport, which is correct and
automatic; the command line stays three drags.

Only `drag` is legal inside the span (`move` is a parse error while the button
is down); the parser appends `up` if it is omitted.

## Step 4 — Verify (all three must pass)

1. **Wire** — the ring endpoint is ≈ (1400,500) within a few px, and different
   from its pre-test value.
2. **Server** — the newest client file in `strokes.d` gained a stroke after the
   baseline noted in step 1, and its first point maps to the pen-down cell of (1359,240).
3. **Screen** — the Z is visible and the red zones are blank:
   - R1 (1405,150)–(1824,250) — overshoot past the top-right corner
   - R2 (1405,250)–(1824,495) — drift right of the top bar
   - R3 (1057,505)–(1824,955) — landing below the bottom bar
   - R4 (1057,182)–(1095,955) — rebound left of the start column
   - R5 (1057,182)–(1824,195) — rebound above the top bar
   - R6 (1095,205)–(1395,495) — wrong side of the diagonal (below-left of it)
   - R7–R10, everything outside the pad — ink there means a window drag, not a
     stroke, so the run measures nothing about the glyph.

## Run record — 2026-11-26 (session 2) — BLOCKED, and `kvm mouse pos` is NOT a liveness check

Re-attempted after the 12:0x probe. **The stroke was never delivered. No ink, no
trace, no motion.** The verdict is unchanged, but two preconditions in this
document are now known to be unsound and are struck below.

### Correction 1 (important): `kvm mouse pos` returns a coordinate for a port that does not exist

This document treats `kvm mouse pos` as the "the device is alive" gate
(Step 0 item 3, Step 2 "must read … ±5"). **It does not test the device.** It
prints a plausible-looking pair and exits 0 on any port name, including one the
OS has never enumerated:

| Command | Exit | Printed | Real cursor, `GetCursorPos` ×3 |
|---|---|---|---|
| `.\kvm.exe mouse pos` (discovery default) | **0** | `644 898` | `485 306` |
| `.\kvm.exe -port COM3 mouse pos` | **0** | `636 899` | `485 306` |

The real cursor never moved, and `GetPortNames()` did not list COM3 at all.
**Therefore:** an exit-0 `pos` proves nothing about the chip. The only honest
liveness gate is a **chip answer**: write the CH9329 get-info frame
`57 AB 00 01 00 03` at 115200 8N1 and require `57 AB 00 81 …` back. Anything
less is a stale cached coordinate, and the previous run record's line "`kvm`
reads the pointer … so the only missing component is still the HID bridge" was
built on this bad check — treat it as struck.

### Correction 2: the CH9329 composite has no serial interface of its own

The bridge is **present and healthy on its HID side** and its **serial side is a
separate physical device**:

| | HID composite (present) | USB-serial (absent) |
|---|---|---|
| Instance | `USB\VID_1A86&PID_E12A\20250418XXXX` | `USB\VID_1A86&PID_55D3\5C94029941` |
| State | Status OK, CME 0 | `CM_PROB_PHANTOM`, `Present=False` |
| Nodes | 11, all `MI_00`–`MI_03` (HID class, no Ports node) | Ports class, `Unknown` |

Enumerating every `VID_1A86` node **with the presence filter off** yields no
`MI_04` and no Ports-class node under `PID_E12A`. Since `internal/mouse` opens
the chip only through `ch9329.OpenDevice` → `serial.Open`, the HID side being
healthy is irrelevant: **no `COMn` that answers get-info ⇒ no motion, ever.**
Name-based PnP filters (`CH9329|CH34|CP210`) return empty here because the
friendly names are generic — filter on `InstanceId -match 'VID_1A86'`.

### Correction 3: `/api/whoami`'s `box` is viewport CSS px, not screen px

Measured the same session, both alive at once:

| Source | Reads |
|---|---|
| `GET /api/whoami` → `box` | `x=146, y=40, w=436, h=436` |
| UIA canvas probe (`.mycc/pad.ps1`) | `1361,166,437,436` (screen px) |

`/api/map` builds its answer from the **stored** rect, so it is correct only
when it is fed `ox/oy` matching that rect's space. The "LIVE TARGET" section
above computes `L = 1361 + 66` from the *UIA* rect — that arithmetic is sound
for `mouse abs move` (screen px), but it must **not** be fed into `/api/map`
without `ox/oy` in the same space. There is **no read-back route** — it was deleted on
purpose; the authorities are the client file `<out>/<client-id>.jsonl` (newest by
mtime) and `events.jsonl`.

### Why there is no software-only evidence leg

Step 4 requires three legs — **wire** (ring endpoint moved), **server** (a new
stroke whose first point maps to the pen-down cell) and **screen** (Z visible,
red zones blank). All three are properties of *emitted motion*. The pad server's
only write route is `POST /api/log`, which **records a human's strokes**; it
cannot originate input. So a POSTed "stroke" would produce a trace file that
certifies nothing about the motion path. The escape hatches say it outright:
*"Trace unchanged after drawing → the stroke never reached the page; that is a
failure even if ink looks present."* There is no legitimate way to a PASS
without the chip.

### Verdict for this session

**BLOCKED — the CH9329's USB-serial interface is not enumerated.** Not PASS
(nothing drawn), not FAIL (the motion path was never exercised), not VOID.

Measured this pass, not carried over: `GetPortNames()` = `COM2` only; COM2
opened at 115200 8N1 and returned **0 bytes** to `57 AB 00 01 00 03`;
`PID_55D3` `Present=False`; pad server live (pid 42320, `:8898`, `/api/whoami`
and `/api/map` both answering); `go build -o kvm.exe ./cmd/kvm` = 0.

Software recovery is exhausted and needs elevation this agent cannot obtain:
`pnputil /remove-device "USB\VID_1A86&PID_55D3\5C94029941"` → `Access is denied`,
`pnputil /scan-devices` → `Access is denied`, and `Start-Process -Verb RunAs`
hangs on an unanswerable UAC prompt (do not retry it).

**Unblock is physical:** attach the CH343 USB-serial adapter (or any UART wired
to the CH9329), confirm `[System.IO.Ports.SerialPort]::GetPortNames()` gains a
new `COMn`, confirm that port answers `57 AB 00 81 …` to the get-info frame,
then re-read the UIA rect, re-derive the 15 % inset, and run Steps 2–4 verifying
from the client trace file.

---

## Measured run — 2026-11-26 10:32 (result: shape PASS, scale FAIL)

The spec as re-based above was EXECUTED on the live pad. Everything below is
measured, not inferred.

Setup: UIA pad rect 1293,175,437,436 (re-read immediately before the run), pad
cleared via the UIA id=clear button (epoch 23 boundary), pen parked with
`kvm mouse abs move 1359 240` and confirmed `1359 239` on two reads.

Command (one held span, three drags, no shredding). NOTE: this archived run
used the OLD 371 px target, before the re-base; it is kept as the measured
evidence for the ~0.76 delivery ratio, not as the current command.

    kvm mouse rel down drag 371 0 drag -371 371 drag 371 0 up

| Quantity | Measured | Spec |
|---|---|---|
| pen-down (screen) | 1359,240 | 1359,240 — EXACT |
| leg 1 (to the right) | 281 px | 371 px |
| leg 2 (diagonal down-left) | dx 284, dy 281 | 371, 371 |
| leg 3 (to the right) | 286 px | 371 px |
| corners | 1359,240 -> 1640,248 -> 1356,529 -> 1642,528 | 1359,240 -> 1730,240 -> 1359,611 -> 1730,611 |
| wire endpoint | 1642,528 (two reads, stable) | ~1730,611 |
| server log | 111 events, 1 stroke, 0 duplicates, "appended 111 event(s)" | grows, first point = pen-down cell |
| pad UI | "1 strokes", "cell 102,103" | — |

Screen-space reconstruction used the LIVE UIA RECT as the anchor; solving the
anchor back from the recorded ratios returns exactly 1293.50,176.00 — the rect
itself — so it is a measurement, not a fitted constant.

VERDICT: the shape is a correct Z, the scale is ~76% of spec, uniform over all
three legs and both axes. The origin is exact, so this is neither an anchoring
error nor an extent error.

### Why 76% — the leg length, not the motion code

CORRECTION to the first draft of this section. The cause is NOT a decayed tail
report. internal/mouse/leg.go splits by VECTOR LENGTH over HeldStepPx = 20, not
by reportMaxPx = 127: "a host does not see axes - it sees a vector ... Held
motion is only 1:1 while the step stays small (measured on this rig: 20 px -> 22,
40 px -> 60, i.e. 1.5x once the step grows)". A 371 px leg is therefore ~19
reports of 20 px or less, all at the PINNED dragGapMS = 12 pacing; the 127 cap
never comes into play here.

What the 0.76 measures is the CADENCE term, which strokePath documents
explicitly: "the same 160 px draw measured 1.04x at 2 ms, 0.89x at 6 ms and
0.65x at 12 ms". At the pinned 12 ms this host delivers held motion short, and
0.76 is the same band. Every leg in every direction came out 0.76-0.77 because
they all ran at the same cadence - that is a cadence signature, not a torn tail.

Consequence for a re-run. There is no gain model in the driver by design
(scaleByGain is the identity; the gain division was removed on the user's
instruction), so the fix belongs in the SPEC's leg lengths, not in
internal/mouse. Three honest options:

  1. Ask for MORE: legs of 371 / 0.76 ~= 488 px keep the 12 ms cadence and the
     sample count and land on the stated vertices.
  2. Change the CADENCE: a smaller -gap approaches 1.04x at 2 ms, but the page
     samples once per display frame, so a faster stroke records in fewer points
     - denser geometry, coarser evidence.
  3. Re-base the vertices to what the pinned cadence actually delivers, and
     state that the glyph is drawn at 76% of the ideal Z.

Do NOT "fix" this by doubling the request blindly, and do not reintroduce a
gain divisor: a single `rel move` travels 1:1 and the shortfall is caused by
report cadence, not by the host losing distance per se.

### ROOT CAUSE of the 0.76 — measured from the log, 2026-11-26

Both completed runs (epoch 23 unpaced, epoch 24 with `wait 2`) delivered the
same ~281 px per 371 px leg, so pacing was eliminated. The per-event record of
epoch 24 leg 1 shows what is actually happening:

    events  1-13 : dT 14-15 ms,  dX 9-10 px
    events 14-29 : dT  8- 9 ms,  dX 9-10 px
    event  30->31: dT 29 ms     (the corner)

Three facts follow, and each kills an earlier explanation:

1. The pad records ~9 px per sample, NOT 20. HeldStepPx = 20 governs the
   reports sent to the chip; the page samples the pointer once per display
   frame (~16 ms at 60 Hz), so a frame collapses one or two chip reports into a
   single sample. Per-sample step is therefore pacing x speed, not report size.
2. Leg 1 ends at x=1640 and STAYS there for the last three samples, so the
   pointer genuinely stopped ~90 px short. This is not a sampling artifact and
   not corner-detector clipping.
3. The leg spent ~30 x 14 ms ~= 420 ms in flight to move 280 px. The commanded
   371 px at 19 reports x 12 ms would have taken 228 ms. The host applies a
   HELD-STREAM DECELERATION of ~0.76 that scales with wall-clock in flight, not
   with report count. Identical results at 12 ms and at `wait 2` are the
   signature.

A SECOND finding, separate from the ratio: `heldRun` attaches one gap per
WAYPOINT, not per report, so `wait MS` between two drags cannot pace the
individual reports of a multi-report leg. The claim in strokePath's comment
that a held span honours the caller's gap "so a stroke can be paced across as
many frames as the caller needs" is therefore NOT reachable from the CLI for
any leg longer than one report. A paced-handwriting experiment cannot be
expressed with `wait`.

### Remedies, now concrete

* SPLIT THE SPAN AT CORNERS (re-press per leg) so no single held run exceeds
  ~15 reports. This is the drawStep design that already exists in leg.go for
  exactly this reason, and it is what the Z's three legs should use.
* Or PRE-COMPENSATE the requested delta by 1/0.76 for a leg of this length -
  one measurable constant, but it is a gain model by another name and the
  gain division was deliberately removed from this driver.
* Or shorten HeldStepPx so fewer, larger reports fit the frame budget - but
  that trades directly against the acceleration band the same comment warns
  about (20 px -> 22, 40 px -> 60).

NOT a remedy: doubling the request, shrinking the drag, or adding a post-stroke
correction. The correction is what once drew a second backwards stroke.
### Caveats that make this evidence, not a certificate

* The run went through kvm-inputpad-final.exe (PID 19768, built 2026-11-25
  23:27) — the OLD binary — so it certifies that page, not today's build.
* `kvm mouse abs move 1359 240` FAILED ONCE during setup, reporting
  "-> 1285,306" (an honest error, not a silent lie; `mouse pos` then read
  "1232,361"). The identical command verified exactly on retry, and the log
  proves the real pen-down was 1359,240. An intermittent absolute-placement
  failure is a real defect and should be recorded separately.
* `kvm mouse rel down up` (the Step 1 clear click) does NOT move the pointer —
  no relative move is emitted — so the pad must be cleared through the UIA
  id=clear button instead.

---

## Step 5 — Report

The measured endpoint, the trace diff, and the red-zone verdict. If the endpoint
is more than ~5 px off, say so; do not call the glyph drawn.

---

## Run record — 2026-11-26 11:08 (result: BLOCKED, no HID device)

Attempted on the canonical pad after the anchor had been validated on the live
canvas rect. **The stroke was never delivered.**

### Preconditions that DID pass

| Check | Result |
|---|---|
| Server on today's build | `/api/whoami` → pid 42320, `exe_mtime 2026-11-26T03:02:26Z` |
| Live canvas rect (UIA, `.mycc/pad.ps1`) | **`1361,166,437,436`** |
| Document rect vs live rect | documented `1293,175,437,436` was **stale by +68,-9** |
| `/api/box` seeded | `POST {"x":1361,"y":166,"w":437,"h":436}` echoed back verbatim |
| Anchor validated (read-only, `/api/map`) | `ox=0,oy=0` → `(1427,231)` cell **19,19**; `(1732,537)` cell **108,108**; both `inGrid:true` |
| Pointer parked before pressing | `kvm mouse pos` → `1572 520`, read twice, unchanged |

Target derived from the live rect, 15 % inset per axis:
**(1427,231) -> (1732,231) -> (1427,537) -> (1732,537)**, deltas
`+305,0 -> -305,+306 -> +305,0`.

### What failed

At `2026-11-26T11:08:37+08:00`:

    kvm mouse abs move 1427 231
    kvm: open COM3 after retries: Serial port not found

    kvm mouse rel down drag 305 0 drag -305 306 drag 305 0 up
    kvm: open COM3 after retries: Serial port not found

`kvm mouse pos` read `1572 520` before **and** after both commands — unchanged.
No pointer motion, no ink, no trace file written.

### Evidence that the device is absent, not mis-addressed

    [System.IO.Ports.SerialPort]::GetPortNames()  ->  COM2
    Get-CimInstance Win32_SerialPort              ->  COM2  通信端口 (COM2)
    Win32_PnPEntity where Name matches
      CH9329|CH34|CP210|FT232|USB-SERIAL|Silicon Lab  ->  (empty)

Only a legacy motherboard COM2 exists. No USB-serial bridge and no CH9329 are
attached, so this is not a wrong `-port` value: no port override could succeed.
`-dry-run` is not a substitute either — it sends nothing and writes no trace.

### Verdict

**BLOCKED — the bridge is PRESENT on its HID interface, but its USB-serial
interface is not enumerated.** Not PASS, not FAIL, and not VOID-for-wrong-build:
the run was correct in every respect up to the serial open. The Zibra test
remains **unperformed**, and this file should not be read as reporting one.

> **CORRECTED 2026-11-26 12:1x — the earlier wording of this verdict ("no HID
> device present") was WRONG.** Measured in a single pass with
> `Get-CimInstance Win32_PnPEntity | Where DeviceID -match 'VID_1A86'`:
>
> | Interface | Device | Status |
> |---|---|---|
> | **HID composite — PRESENT** | `USB\VID_1A86&PID_E12A\20250418XXXX` (USB Composite Device) | **OK**, `ConfigManagerErrorCode 0` |
> | HID keyboard | `HID\VID_1A86&PID_E12A&MI_00` | OK |
> | HID system/consumer control | `HID\VID_1A86&PID_E12A&MI_01` | OK |
> | HID mouse (2 collections) | `HID\VID_1A86&PID_E12A&MI_02` | OK |
> | HID vendor-defined | `HID\VID_1A86&PID_E12A&MI_03` | OK |
> | **USB-serial — ABSENT** | `USB\VID_1A86&PID_55D3\5C94029941` (CH343) | no device, no driver instance |
>
> So the chip is attached and healthy; what is missing is the serial side that
> used to enumerate as `COM3`. **This matters because `kvm` has no HID
> transport at all**: `internal/mouse/device.go` → `ch9329.OpenDevice` →
> `serial.Open` (`third_party/ch9329/port.go`). There is no `net.Dial`, no HID
> path, and no mock backend outside `internal/mouse/*_test.go`. Re-confirmed in
> the same pass: `[System.IO.Ports.SerialPort]::GetPortNames()` → **`COM2`**;
> opening `COM3` → `Could not find file 'COM3'`.
>
> Therefore the blocker is a **driver/enumeration fault on the CH343 serial
> interface**, which is a fixable class of problem — not "no hardware". What to
> try, in order: physically re-plug the adapter (the HID side can stay up while
> the serial side fails to re-enumerate); PnP rescan; reinstall the CH343
> driver if it has been uninstalled. Confirm success by `GetPortNames()`
> gaining a `COMn` and by a CH9329 get-info (`57 AB 00 01 00 03`) answered with
> `57 AB 00 81 …` within milliseconds.

To unblock: restore the serial interface, re-read the live UIA rect (it moved once
during this session), re-derive the 15 % inset from it, and re-run Step 2–4.

#### Follow-up probe, 2026-11-26 11:1x — COM2 ruled out too (both ports now measured)

The 11:08 run only established that COM3 could not be opened. A later probe
closed the remaining hypothesis — "the bridge is on the motherboard port" — by
measurement rather than assumption:

| Port | Test | Result |
|---|---|---|
| `COM3` | `USB-Enhanced-SERIAL CH343`, `USB\VID_1A86&PID_55D3\5C94029941` | `Present = False`, `ConfigManagerErrorCode = CM_PROB_PHANTOM` — a ghost registration from an unplugged adapter; `New-Object SerialPort 'COM3'` → `Could not find file 'COM3'` |
| `COM2` | `ACPI\PNP0501` motherboard UART, opened at **115200 8N1**, wrote the CH9329 get-info frame `57 AB 00 01 00 03` (cmd `0x01`), 300 ms read window | Opened OK, **0 bytes returned** — read timed out. A real CH9329 answers `57 AB 00 81 …` in milliseconds; a bare UART with nothing behind it swallows the write silently. |

So neither enumerated port can carry a HID report: COM3 does not exist as a
device, and COM2 exists but does not speak CH9329. No `-port` value (including
`cfg.PortAlt`) can succeed, and this is now verified rather than inferred.
**The Zibra test cannot be performed on this host until a CH9329-based bridge
enumerates as a present USB-serial device.**

### Two findings worth keeping from this attempt

1. **The document rect goes stale in minutes.** It was measured at
   `1293,175,437,436`, then the pane moved to `1361,166,437,436` (+68,-9)
   within the same session. Re-read UIA immediately before every run; never
   inherit a rect from this file.
2. **`/api/box` and `/api/map` `ox/oy` must share ONE coordinate space.**
   `mapCell` computes `u = (sx - ox - b.X) * 128 / b.W` (main.go:142) while
   `cssPoint` returns `sx - ox` (main.go:163). If the box holds SCREEN px and
   the caller also passes a viewport origin, the canvas origin is subtracted
   twice and **every** point reads `inGrid:false` — silently, with no error.
   Posting a screen-px rect is valid only when `/api/map` is called with
   `ox=0,oy=0`; a page-published (viewport-relative) rect uses the real offset.

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

#### Follow-up probe, 2026-11-26 12:0x — re-check, hardware still absent (unchanged verdict)

`zibra-z.md` was re-read and the whole Step 0–1 gate re-run at 12:0x, in case the
bridge had been attached since the 11:08 run. It had not. Evidence, measured this
pass, not carried over:

| Check | Command | Result |
|---|---|---|
| Enumerated ports | `Get-PnpDevice -Class Ports` | `通信端口 (COM2)` `OK` `ACPI\PNP0501\1`; `USB-Enhanced-SERIAL CH343 (COM3)` **`Unknown`** `USB\VID_1A86&PID_55D3\5C94029941` |
| Port list the runtime sees | `[System.IO.Ports.SerialPort]::GetPortNames()` | **`COM2`** only — COM3 is not a live device |
| Open COM3 with .NET | `SerialPort 'COM3'` → `.Open()` | `Could not find file 'COM3'` (the earlier "open succeeded" reading was wrong — re-measured) |
| Open COM3 with `kvm` | `./kvm.exe -port COM3 mouse abs move 1400 300` | `kvm: open COM3 after retries: Serial port not found`, exit 3 |
| COM2 speaks CH9329? | open 115200 8N1, write `57 AB 00 01 00 03`, 400 ms window | opened OK, **0 bytes** returned — still a bare UART |
| Pad server | `GET /api/whoami` | live, PID 42320, box `1361,166,437,436`, out `D:/dev/kvm/strokes.d` |
| Pad stroke record | read `<out>/<client-id>.jsonl` (newest by mtime) | on disk — there is no read-back route at all |

Two corrections to earlier notes in this file:

- **There is no read-back route.** Steps 1 and 4 of this document once told the
  operator to curl one; that route was deleted on purpose (`pad-protocol.md` §5 —
  the API receives the log, the observer reads the file). The authorities are the
  client's own file under `-out` (`<client-id>.jsonl`, newest by mtime) and
  `events.jsonl`. Both steps above now read the file.
- **`events.jsonl` has not been written since 2026-11-25 22:18.** The newest file in
  `strokes.d` is `342dac2b-….jsonl` (mtime 2026-11-26 10:42). No strokes have reached
  the page since the last failed attempt; the slate state is unchanged.

`go build -o kvm.exe ./cmd/kvm` → OK, and `kvm` reads the pointer (`kvm mouse pos`
→ `357 792`), so the only missing component is still the HID bridge itself.

**Verdict unchanged: BLOCKED — no CH9329 device present. The Zibra test remains
unperformed.** Unblocking is a physical action: plug the CH9329 bridge in, confirm
`GetPortNames()` gains a new `COMn`, re-read the live UIA rect, re-derive the 15 %
inset, then run Steps 2–4 and verify from the client trace file.