/* kvm-cursor-report@mycc — publishes the live pointer position, draws NOTHING.
 *
 * The shell is the only process that knows where the pointer really is on a
 * Wayland session (XWayland reports a phantom pointer; evdev needs a privileged
 * fd). So this extension owns that one fact and hands it out through a fixed
 * 1 KiB ring buffer, not through a message broker:
 *
 *     $XDG_RUNTIME_DIR/kvm-cursor.ring
 *       header (64 B): magic u32 "KVMR", capacity u32, write_index u32,
 *                      heartbeat u32, reserved
 *       record (16 B): seq u32, x i32, y i32, timestamp_ns u64   (CLOCK_MONOTONIC)
 *
 * WHY A RING AND NOT A SIGNAL
 * ---------------------------
 * The question the agent asks is "where is the pointer NOW, and where was it in
 * the last second" at 60 Hz. A broker makes every frame a delivery guarantee
 * nobody needs — the cheapest frame to lose is precisely the one nobody read —
 * and D-Bus additionally costs a match + wakeup per frame. A ring is a value,
 * not an event: the writer never waits, a full ring simply overwrites the
 * oldest slot (1 s of history is all anyone asked for), and a slow reader gets
 * a shorter trail instead of a backlog.
 *
 * The writer rewrites the whole 1088-byte block once per frame. That is one
 * write() of a single cache-resident buffer — cheaper than the per-field pwrite
 * dance, and impossible to leave half-updated by construction on the reader
 * side (the reader still checks seq, because a 64-byte header and a record land
 * in one write() only by luck of the filesystem's write atomicity).
 *
 * This extension never moves the pointer and draws no chrome: it is a sensor.
 */

import GLib from 'gi://GLib';
import Gio from 'gi://Gio';

import {Extension} from 'resource:///org/gnome/shell/extensions/extension.js';

const TICK_MS = 16; // ~60 Hz: one frame
const HEARTBEAT_MS = 1000; // the reader treats 3 s of silence as "publisher gone"

const MAGIC = 0x524d564b; // "KVMR"
const CAPACITY = 64; // 1 s at 60 Hz
const HEADER = 64;
const RECORD = 24; // seq u32 + x i32 + y i32 + timestamp u64 = 20, padded to 24
const FILE_SIZE = HEADER + RECORD * CAPACITY;

const RING_PATH = GLib.getenv('KVM_CURSOR_RING') ??
    GLib.build_filenamev([GLib.getenv('XDG_RUNTIME_DIR') ?? '/tmp', 'kvm-cursor.ring']);

/* --- little-endian helpers over one preallocated buffer ------------------ */

const buf = new Uint8Array(FILE_SIZE);
const dv = new DataView(buf.buffer);

function putU32(off, v) { dv.setUint32(off, v >>> 0, true); }
function putI32(off, v) { dv.setInt32(off, v | 0, true); }
function putU64(off, v) { dv.setUint32(off, v >>> 0, true); dv.setUint32(off + 4, Math.floor(v / 4294967296) >>> 0, true); }

export default class KvmCursorReportExtension extends Extension {
    enable() {
        this._destroyed = false;
        this._warned = false;
        this._index = 0;
        this._seq = 0;
        this._out = null;
        this._openRing();

        this._tickId = GLib.timeout_add(GLib.PRIORITY_DEFAULT, TICK_MS, () => {
            if (this._destroyed)
                return GLib.SOURCE_REMOVE;
            this._tick();
            return GLib.SOURCE_CONTINUE;
        });
    }

    /* Open (create/truncate) the ring. */
    _openRing() {
        try {
            const f = Gio.File.new_for_path(RING_PATH);
            const rw = f.replace_contents(
                new Uint8Array(FILE_SIZE), null, false,
                Gio.FileCreateFlags.REPLACE_DESTINATION, null);
            if (rw && typeof rw.close === 'function')
                rw.close(null);
            // Static header fields.
            putU32(0, MAGIC);
            putU32(4, CAPACITY);
            putU32(8, 0); // write_index
            putU32(12, 0); // heartbeat
            this._publish(0);
            // Static header fields.
            putU32(0, MAGIC);
            putU32(4, CAPACITY);
            putU32(8, 0); // write_index
            putU32(12, 0); // heartbeat
            this._publish(0);
        } catch (e) {
            this._logOnce(`open ${RING_PATH} failed: ${e.message}`);
        }
    }

    /* Rewrite the whole block from offset 0. Opens the file fresh each
     * publish: no long-lived stream that GJS can GC and close underneath us. */
    _publish(heartbeat) {
        try {
            putU32(12, heartbeat);
            const f = Gio.File.new_for_path(RING_PATH);
            const out = f.replace(null, false,
                Gio.FileCreateFlags.REPLACE_DESTINATION, null);
            out.write_all(buf, null);
            out.close(null);
        } catch (e) {
            this._logOnce(`write ${RING_PATH} failed: ${e.message}`);
        }
    }

    _tick() {
        let px, py;
        try {
            [px, py] = global.get_pointer();
        } catch (e) {
            this._logOnce(`get_pointer failed: ${e.message}`);
            return;
        }
        // Wall-clock ns: the Go reader converts the timestamp with
        // time.Unix(0, ns) and checks time.Since(p.At), so a CLOCK_MONOTONIC
        // value would read as 1970 and always look stale.
        const now = Date.now() * 1e6;
        const idx = this._index;
        const off = HEADER + (idx % CAPACITY) * RECORD;

        // get_pointer reports the cursor origin; the visible tip is one pixel
        // down-right, which is what "where the pointer is" means to a human.
        const x = Math.round(px) + 1;
        const y = Math.round(py) + 1;

        const moved = x !== this._x || y !== this._y;
        const beatDue = now - (this._beatAt ?? 0) >= HEARTBEAT_MS * 1e6;
        // Publish every tick, moved or not: the ring is a freshness signal, and
        // skipping unchanged frames makes readers see 1 Hz heartbeat jumps.
        this._x = x;
        this._y = y;
        this._seq += 1;
        putU32(off, this._seq);
        putI32(off + 4, x);
        putI32(off + 8, y);
        putU64(off + 12, now);

        this._index += 1;
        putU32(8, this._index);
        if (beatDue)
            this._beatAt = now;
        this._publish(beatDue ? ((this._beatCount = (this._beatCount ?? 0) + 1)) : (this._beatCount ?? 0));
    }

    _logOnce(msg) {
        if (this._warned)
            return;
        this._warned = true;
        try {
            console.log(`[kvm-cursor-report] ${msg}`);
        } catch {
            // logging must never break the feed
        }
    }

    disable() {
        this._destroyed = true;
        if (this._tickId) {
            GLib.source_remove(this._tickId);
            this._tickId = 0;
        }
    }
}
