# Report layer — the contract every other layer stands on

Layer 1 of the rebuild. Nothing above this layer (crosshair, movement, CLI) is
written until every claim here is demonstrated on the wire.

> **State of the tree this describes:** layers 2–4 were in fact written first, so
> treat the ordering above as the standard to hold the code to, not as a
> description of the commit order. §5 is therefore a retro-check of layer 1, and
> §3 records one place where the implemented pacing does not yet meet this
> layer's own contract.

Sources cited are in `third_party/ch9329`; the measurement notes are the
author's, recorded in the code before my changes and carried forward unchanged.

---

## 1. Frame envelope

```
0x57 0xAB ADDR CMD LEN D0..Dn-1 SUM
```

| Field | Value / rule | Source |
|---|---|---|
| HEAD0, HEAD1 | `0x57`, `0xAB` | `protocol.go` `Head0/Head1` |
| ADDR | `0x00` for the host | `reports.go` `Frame(0x00, …)` |
| CMD (relative mouse) | `CmdHIDMouseRel` | `protocol.go` |
| LEN | number of DATA bytes | frame layout |
| SUM | `HEAD0 + HEAD1 + ADDR + CMD + LEN + ΣDATA` (mod 256) — **the header bytes are part of the sum** | `protocol.go` `Checksum` |

The checksum claim is not cosmetic: omitting the header bytes makes every frame
`0x02` low and the chip replies `DEF_CMD_ERR_SUM` (`0xE4`). That failure was
once misread as a dead cable.

## 2. Relative-mouse payload (the only thing that draws)

```
D0 = 0x01            fixed marker (says "relative")
D1 = buttons         bit0 left 0x01, bit1 right 0x02, bit2 middle 0x04
D2 = dx   as int8
D3 = dy   as int8
D4 = wheel as int8 (0 for motion)
```

| Claim | Status |
|---|---|
| LEN is **5**, and the `0x01` lead byte is what makes the chip parse it as relative | measured: LEN=4 → chip "scrolled" instead of moving; LEN=5 with `0x01` → correct |
| Negative deltas are two's complement: `-1` → `0xFF` | `byte(int8(dx))`; **exact** |
| Per-axis range is **-128..127**; a larger value wraps silently (`int8(200)` = `-56`) | **contract gap** — callers must clamp; the builder does not |
| A zero-delta, zero-button report is legal and means "no change" | it is also the **release** frame |

**Contract gaps I am asserting explicitly, because they bit me:**

1. **The builder does not clamp.** `mouseRelReport` will emit a wrapped delta for
   any `|d| > 127`. The motion paths in `internal/mouse` clamp on every write
   (`streamAt` and `stream` both clamp to ±127), so the range is enforced by
   convention rather than by the layer. *Layer 1's job is to make it enforced.*
   The convention is **not** universal: `third_party/ch9329/ctl.go` `MouseRelRaw`
   and `RelNudgeBtn` hand `dx, dy` straight to `MouseRelFrame` with no clamp, so a
   caller that reaches the driver directly can still wrap a delta, and
   `internal/mouse/rel.go` `send()` — used by `Rel.Down()` — goes through
   `MouseRelRaw`.
2. **Zero-motion press frames are host-dependent.** A frame with `buttons != 0`
   and `dx=dy=0` is a legal report but the host may deliver it to a page as a
   `pointermove` with no buttons, which a consumer can misread as a lift. It is
   not a driver bug — but it is **layer 1's own rule to own**, and layer 1 does
   own it: `internal/mouse/press_windows.go` and `press_other.go` select the
   behaviour per host (see "Per-host emitted bytes" below), and `Rel.streamAt`
   applies it. A layer above must not re-implement the rule.
3. **One report ≠ one movement.** The host applies acceleration as a function of
   measured speed, so the delivery of one report is only guaranteed 1:1 inside a
   small delta band. The band must be measured, not assumed.

### Per-host emitted bytes

The payload shape is fixed; the *choice of which frames are written* is not. The
build-tag pair below selects it, and that is deliberate, not drift:

| File | Constant | Value |
|---|---|---|
| `internal/mouse/press_windows.go` | `pressRidesFirstMove` | `true` |
| `internal/mouse/press_other.go` | `pressRidesFirstMove` | `false` |

- **When `true`** (Windows) a zero-motion button frame (`btn != 0`,
  `dx == dy == 0`) is **not written** by `Rel.streamAt`; the press rides the first
  moving report instead. `Rel.down` is already true, so the deferred press costs
  no state.
- **When `false`** (every other host) the frame **is written**, and the byte
  stream on those hosts is unchanged.

So "the contract" is the frame layout and the checksum; the press-emission rule is
a documented per-host difference at this layer, selected by build tag.

## 3. Acknowledgement

The chip answers each report with a reply frame; the reply's status byte is the
authority on whether the report was accepted. Two facts from the code:

- A missing or stale reply is indistinguishable from a good one **if the reader
  discards the bytes** — so the contract is: *parse the reply, act on the
  status, and never treat "the read returned" as "the report was accepted".*
  The relative emitter does **not** meet this contract today: `writeReport`
  calls `_ = p.ReadAvailable(ackWaitMS)` and drops the bytes
  (`internal/mouse/transport.go`), so what it enforces is the spacing, not the
  acknowledgement. Closing that gap is layer-1 work, not a claim about the code.
- The pacing floor between relative reports is `ch9329.FrameGap` = **8 ms**,
  measured from the write, not added after the read. `writeReport` applies it as
  `reportGapFloor` and treats the caller's `gap` as a minimum.

## 4. Absolute reports — placement only

`CMD 0x04`, LEN 7, payload marked by a fixed `0x02` first byte (not a button
byte, in contrast to the relative payload where D1 **is** the button byte).
Grid 4096 maps to the screen with no offset: `cursor = 0.46875·grid_x`,
`0.263672·grid_y` (measured, 12 points, max residual 0.5 px).

**This layer is built.** `Abs.GoTo` (`internal/mouse/absops.go`) sends one
`ch9329.Device.AbsGoto`, which builds `MouseAbsFrame` / `mouseAbsReport`
(`third_party/ch9329/reports.go`, `absGrid = 4096.0`) via `PixelsToChip`, and
then waits up to `absSettle = 120 ms` for the pointer to settle. `Abs.GoTo`
refuses to run while the button is held (`ErrAbsWhileHeld`), because an absolute
frame carries its own button mask and would replace a press it does not own.

---

## 5. What "demonstrated" means — the acceptance test for layer 1

Prove the layer, in order, with the smallest possible experiments:

1. **Checksum**: construct a relative frame by hand and verify `Checksum` agrees
   with an independently computed sum. *(A unit test, no hardware.)*
2. **Encoding**: `MouseRelFrame(0, -1, -137, 0)` must show what the contract says
   — `0xFF` for -1, and the wrapped byte for -137 (demonstrating gap 1).
3. **One report, button up, known delta**: send exactly one report of a small
   delta and read the pointer back. Repeat at several sizes to find the band
   where delivery is 1:1, *measured from a verified start point*.
4. **One report, button down**: same, with the button asserted, and confirm the
   host sees the button.

Only when 1–4 hold do layers 2–4 get written — that was the plan; in this tree
they were written first, so this list doubles as the retro-check of layer 1.
Items 1 and 2 are covered by unit tests (`third_party/ch9329/frame_test.go`,
`internal/mouse/report_test.go`); items 3 and 4 need the board.

---

## 6. Standing rules for the rebuild

1. **Every number in this document is either measured or cited.** No claim
   without provenance.
2. **A comment is not evidence.** Four behaviours were previously asserted in
   comments that the code did not implement; each cost hours.
3. **Measure from a verified start.** Every "under-delivery" reading in the last
   round came from not verifying where the pointer actually was first.
4. **Verify each layer before building on it.** The crosshair was never going to
   work (its loop never pumped messages) and the movement layer could not have
   worked (steps bounded per axis at 127). Both were discovered only after
   features had been built on top of them.
