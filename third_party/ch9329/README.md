# CH9329CTL — drive a CH9329 USB-HID bridge as a keyboard and a mouse

`CH9329CTL` turns a CH9329 serial-to-HID bridge into a scriptable input device.
The bridge enumerates on the target machine as an ordinary USB keyboard and
mouse, so every keystroke and pointer move it sends is delivered by the host's
own input stack — no agent, driver, or debug port on the target.

Everything is written against one hard-won rule:

> **The chip ACKs frames the host never applies. An ACK is not evidence.**

So every movement command reads the pointer back with `GetCursorPos()` **in the
same process that sent the frame**, and reports what the OS actually says. A
different process can read a stale position; a chip reply cannot tell you the
payload was semantically wrong. The readback is the only witness.

```
CH9329CTL selftest
CH9329CTL move -x 900 -y 400 -duration 350ms
CH9329CTL slide -from-x 439 -from-y 534 -to-x 685 -to-y 534 -human
CH9329CTL -k ctrl+w -repeat 3
```

Run `CH9329CTL help` for the full command reference.

## Platforms

| OS | Status |
|---|---|
| **Windows** | **Complete.** Pointer witness via `GetCursorPos`, absolute `0x04` motion verified to ±1 px, window/focus helpers implemented. |
| **Linux** | **Witness complete and control-proven** (X11 via `jezek/xgb`, no cgo): a local `SetCursor` warp reads back exactly. Window/focus helpers stubbed. End-to-end chip injection is **not** verified on the reference host — see [PORTING.md](PORTING.md) (the chip ACKs byte-perfect frames but its HID output produced no pointer motion there; a hardware/firmware fact, not a port defect). |
| **macOS** | **Witness implemented** (CoreGraphics cgo) plus a non-cgo stub fallback so `GOOS=darwin CGO_ENABLED=0` builds. `CGEventGetLocation` readback is same-process; requires Accessibility trust only for future `CGEventPost` injection. **Structurally type-checked only** — real-macOS runtime verification is still open. See [PORTING.md](PORTING.md). |

The serial protocol, frame encoding, report layout and all motion shaping are
platform-independent. Only two primitives are not: reading the pointer
(`CursorPoint`) and knowing the coordinate space (`ScreenSize`). The whole
platform layer is isolated per GOOS — `pointer_windows.go` (Windows),
`platform_linux.go` (X11 via `jezek/xgb`), `platform_darwin.go` (CoreGraphics),
with `platform_darwin_nocgo.go` and `platform_other.go` as honest
`ErrUnsupported` fallbacks for builds that lack a real witness.

Those fallbacks return `ErrUnsupported` rather than a plausible-looking zero, on
purpose: a driver that cannot see the pointer cannot prove that anything it sent
had an effect, and a fake witness would make every motion primitive "succeed"
against a fiction. See **[PORTING.md](PORTING.md)** for the full contract, an X11
mapping, the macOS Accessibility requirement, the honest per-platform
verification status, and why Wayland makes reading the pointer back the central
design question of that port.

Cross-platform builds are checked in CI for `windows`, `linux` and `darwin`.

## Requirements

- Windows (the pointer witness is Win32 `GetCursorPos`).
- Go 1.25+ to build.
- A CH9329 bridge on a COM port. The reference board is a CH343 USB-serial
  adapter (often `COM3`) running at **115200 8N1**.

```
go build -o CH9329CTL.exe ./cmd/ch9329ctl
```

## Commands

### Pointer

| Command | What it does |
|---|---|
| `cursor` | Print the real pointer position. |
| `goto -x N -y N` | One absolute `0x04` report, verified in-process. |
| `move -x N -y N` | Smooth move: many small eased steps, then a verified absolute landing. |
| `nudge -dx N -dy N` | One relative `0x05` report; prints what the OS actually delivered. |
| `drag -x N -y N [-from-x N -from-y N]` | Press, glide with the button held, release at the measured landing point. |
| `slide -from-x N -from-y N -to-x N -to-y N` | The held-gesture primitive (see below). |
| `click`, `scroll -n N` | Clicks and wheel notches. |

**Coordinates are flags.** `CH9329CTL goto 700 500` does **not** move to
(700,500): Go's `flag` package stops parsing at the first non-flag argument, so
both numbers are dropped, `x` and `y` stay at their zero values, and the tool
cheerfully reports a successful move to `(0,0)` with `d=(0,0)`. Always spell out
`-x` and `-y`. This failure is silent by construction and has cost real debugging
time; it is called out here, in `help`, and in `QUESTIONS.md` for that reason.

### Keyboard

`key <spec>`, `-k <spec> [-repeat N]`, `hotkey <spec> <spec>...`, `type <text>`.
Key specs are names (`a`, `enter`, `tab`) or chords (`ctrl+w`, `shift+tab`).

### Windows and diagnostics

`focus <title>` brings a window to the foreground and proves it took effect.
`win [-match S]` lists windows with their client geometry.
`probe` reads the chip identity. `bench -n N` times N raw frames.
`selftest` proves the feedback channel *before* trusting it.

## The two primitives that matter

### Why `slide` exists

A slider CAPTCHA needs one sustained press, a stream of small moves, and a
release. Building that out of `mouse down` + `move` + `mouse up` fails for two
independent reasons:

1. **Process boundaries.** Each invocation pays process start, port open and
   handshake, so a 20-step gesture becomes 20 separate gestures (~123 ms apart).
2. **The button byte.** An absolute `0x04` report carries a button mask. If that
   mask is zero, the report **releases** any held button. A drag assembled from
   plain `goto` frames is therefore seen by the page as `pointerdown` followed by
   button-up moves: the handle never enters drag state, `translateX` stays at
   `0px`, and the chip ACKs every frame of the whole futile gesture.

`slide` sends every frame of the path through `MouseAbsHeld(btn, …)`, so the
press survives the entire travel, and it releases at the *measured* landing
point rather than the requested one.

### Human-shaped motion (`-human`)

Real pointer motion is not a straight line at constant speed. `-human` shapes the
path:

- **Lateral tremor** — a smooth, zero-mean wander plus a faster ripple,
  perpendicular to the travel line, 2.5 px by default.
- **Jittered timing** — each gap between reports drawn around the nominal 16 ms,
  with occasional short hesitation pauses.
- **Easing** — slow start, fast middle, slow finish.
- **A slightly non-straight path.**

The design constraint that makes this safe for graded targets:

> **Jitter perturbs the PATH, never the LANDING POINT.**

The tremor envelope is forced to zero at both ends of the travel, and the final
frame is an exact absolute report at the requested destination. So
`slide … -to-x 685 -human` releases at the same coordinate as the plain path,
however the random draws fall. Measured on a 246 px travel: the plain path takes
25 reports and lands at x=684; the `-human` path takes 75 reports over ~2.9 s and
lands at x=684 as well — same landing, visibly different motion.

| Flag | Default | Meaning |
|---|---|---|
| `-human` | off | Enable the whole bundle. |
| `-jitter` | `2.5` | Max lateral tremor, in px. |
| `-ease` | `0.75` | Velocity easing, 0–1. |
| `-timing-sigma` | `0.28` | Relative spread of the inter-report gap. |
| `-no-jitter` | off | Keep timing noise and easing, drop the tremor. |
| `-seed` | `0` | Seed the draws; `0` means clock. Same seed, same path. |

## Design notes

**Absolute, not relative, for destinations.** The `0x04` report maps
pixel-exactly onto this class of host (measured: 6/6 targets within 1 px once the
mandatory `0x02` marker byte was set). It needs exactly one frame, so there is no
accumulation error and no dependence on Windows pointer ballistics. Relative
`0x05` motion *is* subject to the OS acceleration curve — a requested `dx=20`
delivered `dx=22` — which is why relative motion is used only for motion that
looks human and never for landing on a target.

**Resolution ceiling.** The chip's grid is 4096×4096 over the whole screen, so on
a 1920×1080 display one grid step is 0.47 px horizontally and 0.26 px
vertically. Anything claimed below that is measurement noise.

**Readbacks wait for stillness.** The host applies an `0x04` report
asynchronously; reading the cursor 60 ms after the write can still return the
previous position. The readback loop waits until the position has stopped
changing *and* at least one frame gap has passed, anchored on the last known
position so a stale reading is never mistaken for the result.

**Port opens are retried.** The CH343 adapter intermittently (~50 % of attempts)
fails `serial.Open` with `Serial port busy` and reliably swallows the first frame
right after opening. Both silently drop HID frames mid-gesture, which is
indistinguishable from a widget refusing input. `port.go` wraps `Open` in a
bounded retry loop and retries one zero-length write after a short delay.

**Never locate the pointer from a screenshot.** `Graphics.CopyFromScreen` /
GDI/BitBlt grabs do **not** composite the cursor: two frames taken across a
101 px pointer move can differ by 0 pixels. Use `CH9329CTL cursor`.

## Layout

```
ctl.go              pointer control: goto, glide, drag, slide, repeat
device.go           device open/handshake, frame send with reply handling
port.go             serial port with the reopen/retry loop
protocol.go         CH9329 framing (GET_INFO, 0x04 absolute, 0x05 relative...)
reports.go          report layout and the button/modifier masks
keymap.go           key-spec parsing -> HID usage codes
human.go            human-shaped path: tremor, timing, easing
pointer_windows.go  GetCursorPos / SetCursorPos bindings
window_windows.go   window enumeration, foreground, client geometry
win_support.go      screen metrics
hex.go, raw.go      small helpers for tracing frames
frame_test.go       frame-layout tests
human_test.go       shaper tests (endpoints exact, tremor bounded, mean zero)
cmd/ch9329ctl/      the CLI
cmd/sigcap/         capture real mouse input as ground truth, diff two captures
cmd/pointerprobe/   minimal pointer-position probe
```

`_scratch/` holds quarantined session artifacts and is not part of the build.

## Testing

```
go build ./... && go vet ./... && go test ./...
CH9329CTL.exe selftest          # needs the bridge attached
```

`selftest` exits **3** (`METHOD INVALID`) when the witness itself is broken —
for example when a local `SetCursorPos` does not read back, as on a locked
session or an unusual input stack. In that case nothing the chip did can be
judged, and the tool says so instead of blaming the hardware.
