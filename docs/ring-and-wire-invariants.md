# KVM pointer-feed invariants: ring data format + HUD wire protocol

These are the invariants reviewers must check line by line. Writer:
`extensions/kvm-cursor-report@mycc/extension.js` (GNOME Shell). Readers:
`internal/cursor/ring_linux.go` (`kvm mouse pos`) and `cmd/kvm-crosshair`
(the HUD daemon). The two formats below are independent contracts; a change
to either must update both sides.

## 1. Ring data format (file contract)

**Path:** `$XDG_RUNTIME_DIR/kvm-cursor.ring` (fallback `/tmp/kvm-cursor.ring`).
**Total size:** 64 + 24*64 = 1600 bytes, little-endian throughout.
**Writer:** one tick per frame (`TICK_MS = 16`, ~60 Hz). Every tick fills the
record at `write_index`, increments `write_index`, and rewrites the whole file
via `replace(null, false, REPLACE_DESTINATION)` — even when the pointer did not
move (that is how `heartbeat` stays fresh at 1 Hz).

### Header (64 bytes)

| Offset | Size | Field | Meaning |
|---|---|---|---|
| 0 | u32 | magic | `0x524D564B` = "KVMR" |
| 4 | u32 | capacity | 64 slots |
| 8 | u32 | write_index | monotonic count of records published (0,1,2,… — a COUNT, not a slot number). The writer fills slot `write_index % 64` then publishes `write_index+1`, so the newest record is in slot `(write_index - 1) % 64`. Readers currently scan for the max `timestamp_ns` instead (see Reader-side rules), which is equivalent on this writer but does not trust the header. |
| 12 | u32 | heartbeat | +1 at least once per second; **0 means "no writer"** (reader rejects) |
| 16..63 | — | reserved | must be zero |

### Record (24 bytes each, 64 slots starting at offset 64)

| Offset | Size | Field |
|---|---|---|
| 0 | u32 | seq (non-zero, increments per record) |
| 4 | i32 | x (visible cursor tip = origin + 1) |
| 8 | i32 | y |
| 12 | u64 | timestamp_ns, **wall clock** (`Date.now()*1e6`), NOT CLOCK_MONOTONIC — the ORDERING key and a diagnostic; readers must NOT derive staleness from it (see the clock note below) |
| 20..23 | — | padding, zero |

### Reader-side rules (ring_linux.go)

Liveness has **two independent gates**, checked in this order, and neither of
them is the record's own timestamp:

- Magic mismatch → no feed.
- `heartbeat == 0` → no feed. The writer bumps it at least once a second and
  never zeroes it on `disable()`, so this gate catches a file that is present
  and being rewritten by something that is not the extension.
- **The ring FILE's mtime is the age clock.** `Latest()` stats the path on
  every call and rejects the feed when `time.Since(fi.ModTime()) >
  ringMaxAge` (3 s). mtime is stamped by the local kernel on the writer's
  `replace_contents()`/`replace()`, i.e. in **host** time — unlike
  `timestamp_ns`, which the extension stamps with its own view of the wall
  clock. Because the writer rewrites the whole 1600-byte file every tick, a
  live feed has an mtime age of ~0 and a dead one crosses 3 s within 3 s.
  *This is why suspend/resume, NTP steps and a container/VM whose clock
  differs from the shell's no longer matter*: a reader can disagree with the
  writer about the wall clock and still agree about the file's age.
  `Recent()` gates on the same mtime, so a dead feed returns `nil` rather than
  a trail of old points.
- **Newest slot: chosen by max `timestamp_ns`, not by `write_index`.**
  `write_index` is a monotonic publish count (`0,1,2,…`); it is *not* used as a
  slot selector here. `Latest()`/`Recent()` scan all 64 slots and take the
  greatest timestamp, because `timestamp_ns` comes from a single writer whose
  clock is monotonic across its own records — relative order is trustworthy
  even when absolute time is not. `seq` is likewise a monotonic count, so for
  the newest record `seq == write_index` while older slots hold
  `seq = write_index-63 …`. Both are counts, never slot numbers.
  (The layout still permits the `write_index - 1` shortcut — the writer fills
  slot `idx % 64`, then publishes `write_index = idx+1` — and that is what
  `docs` previously mandated. It is a valid *optimisation*, but the scan is the
  contract because it does not depend on the header being consistent.)
- Torn-frame retry: re-read the slot's seq around the payload, max 4 attempts;
  a zero seq means an unwritten slot and is skipped.
- Read a fresh copy of the file on every call (`os.ReadFile` + `os.Stat`); never
  cache an mmap or a `FileInfo` — every publish replaces the inode, so a cached
  mapping stays stale and a cached mtime stays young.
- Writer rewrites the whole 1600-byte block per publish via `Gio.File.replace(null, false, REPLACE_DESTINATION, null)` + `write_all` + `close` (no persistent stream — GJS GC closes it, causing "stream already closed"), and each tick publishes even when the pointer has not moved (heartbeat freshness).

> **Clock note — two clocks, two jobs.**
> `timestamp_ns` is written by the extension as `Date.now()*1e6`, the shell's
> wall clock. It is therefore **unusable for staleness** (the shell and the
> reader can be seconds apart after a suspend or an NTP step) but it is the
> right **ordering** key, because one writer's own successive stamps are
> monotonic. Staleness uses the file's mtime, which is kernel-stamped in host
> time. Do not "simplify" this back to comparing `timestamp_ns` against
> `time.Now()`: that was the old rule, and it silently declared a live feed dead
> whenever the two clocks disagreed by more than `ringMaxAge`.
> `Recent(within)`'s window is measured against the **newest record's own
> stamp** (`newest.Add(-within)`), never `time.Now()`, for the same reason.

## 2. HUD wire protocol (socket contract)

**Path:** `$XDG_RUNTIME_DIR/kvm-crosshair.sock`, **unixgram** (SOCK_DGRAM).

- Singleton ownership is an `flock(LOCK_EX|LOCK_NB)` on `$XDG_RUNTIME_DIR/kvm-crosshair.lock` (kernel releases on process death; stale sockets are removed and re-bound by the new owner).
- Control messages are a single datagram of exactly `"on"` or `"off"` (see `onMsg`/`offMsg` in `cmd/kvm-crosshair/main.go`):
  - `kvm mouse hud on` → spawns the daemon (no-op if flock is held).
  - `kvm mouse hud off` → dials unixgram and writes `"off"`; if the socket is absent the CLI errors "crosshair is not running (dial unixgram …: no such file or directory)" — this is expected when the daemon was never started.
- The daemon reads pointer positions exclusively from the ring above; it never queries X11 (XWayland reports a phantom pointer).

## Invariants that must hold after any change

1. Ring is exactly 1600 bytes; magic, capacity=64, record size 24 are fixed.
2. Timestamps are wall-clock ns and are the ORDERING key; heartbeat is never 0
   while enabled, and staleness comes from the file's mtime, never from
   `timestamp_ns`.
3. Reader and writer agree on **which** record is newest. Readers take the max
   `timestamp_ns` (single writer ⇒ that clock is monotonic across its own
   records); the writer's `write_index` is a publish COUNT, and slot
   `(write_index-1) % 64` is the same record — use the scan, do not treat either
   counter as a slot number.
4. HUD control is one unixgram datagram, "on"/"off" only; one daemon per runtime dir via flock.
5. The daemon re-reads the ring every 16 ms tick and draws only when the position changes, so the crosshair tracks the pointer within one frame (~16 ms) of the writer's publish. A reader that looks "live" (advancing seq/heartbeat) but lags ~1 s is reading a slot one lap behind; a reader that reports nothing while the file's mtime is fresh is broken, not the writer.
