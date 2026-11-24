// Package mouse exposes TWO API sets — absolute and relative — as separate types
// with separate namespaces. There is deliberately no shared "mode" field: the
// choice is made once, in the parser, and then the types keep you honest.
//
// On this host the two are not interchangeable. Probed evidence:
//   - RELATIVE frames deliver button state (BTN_LEFT press + release observed).
//   - ABSOLUTE frames carry motion but their button state is silently dropped
//     (0 BTN_LEFT events across a whole absolute drag).
//
// So `rel` is the drawing path; `abs` is travel-only.
package mouse

import (
	"fmt"
	"os"
	"time"

	"ch9329"
)

// Device is the shared serial link. It owns the release-on-exit guarantee.
type Device struct {
	d    *ch9329.Device
	W, H int
	// px, py remember the pointer's last known SCREEN PIXEL position. It is
	// kept in pixels, not chip grid, because every caller reasons in pixels and
	// the grid is derived on the way out (mixing the two units silently sends a
	// stale coordinate — see grid()).
	px, py int
	log    *os.File
	// ReadPos reports the pointer position from the host, when the host offers
	// one. nil means "not observable", never "at the origin".
	ReadPos func() (int, int, bool)
}

// WireLogPath returns where every frame is recorded. It sits beside the
// executable (or under KVM_WIRE_LOG) so a run can be diffed afterwards: the
// record is the ground truth for "what did we actually put on the wire".
//
// Recording is OFF unless KVM_WIRE_LOG names a path. It used to be forced on,
// which made every invocation append to a file next to the binary: a `kvm mouse
// move` that was supposed to be a single report also cost a file open, a
// write and a flush line per frame, and the file grew without bound (the one
// in this repo is 12 MB). Opt in when you actually need the record.
func WireLogPath() string { return os.Getenv("KVM_WIRE_LOG") }

// Open opens the chip and arranges for an unconditional button release.
func Open(port string, baud, w, h int) (*Device, error) {
	d, err := ch9329.OpenDevice(port, baud)
	if err != nil {
		return nil, err
	}
	dv := &Device{d: d, W: w, H: h}
	// The wire record is OPT-IN (KVM_WIRE_LOG=<path>). Losing the log must
	// never stop a drawing, so an open failure degrades to tracing off rather
	// than an error — and with no path set, tracing is simply off.
	if path := WireLogPath(); path != "" {
		if f, ferr := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644); ferr == nil {
			dv.log = f
			base := time.Now()
			d.Verbose = true
			d.Trace = func(line string) {
				fmt.Fprintf(f, "%+7.3f %s\n", time.Since(base).Seconds(), line)
			}
		}
	}
	return dv, nil
}

// Close releases the button and closes the port. Always safe to call twice.
// ReadBack asks the OS where the pointer actually is. It is the only way to
// close the loop on an output-only device: the host is the witness, the chip is
// not. A host that cannot answer leaves the position unknown, which the caller
// must treat as "do not walk from here".
func (dv *Device) ReadBack() (int, int, bool) {
	if dv.ReadPos == nil {
		return 0, 0, false
	}
	return dv.ReadPos()
}

// Close is unchanged; see below.
func (dv *Device) Close() {
	// zero-mask RELATIVE release: it cannot teleport the pointer the way an
	// absolute frame at 0,0 would.
	_ = dv.d.MouseRelRaw(0, 0, 0)
	dv.d.Close()
	if dv.log != nil {
		dv.log.Close()
		dv.log = nil
	}
}

// Release is the same guarantee, callable from a defer in main.
func (dv *Device) Release() { _ = dv.d.MouseRelRaw(0, 0, 0) }

// Chip exposes the underlying protocol device (for probing commands).
func (dv *Device) Chip() *ch9329.Device { return dv.d }

// NOTE: the pixel→grid mapping (gridForPixel / grid / MapPixelsToGrid /
// lastPixel) was deleted here. It converted screen pixels to the chip's 12-bit
// absolute grid, and nothing in the tree sends an absolute (0x04) frame any
// more: `abs` placement is a relative walk (Abs.GoTo → goPoint → walkTo), so the
// mapping had no producer and no consumer except its own unit tests, which were
// deleted with it. The measurements that killed 0x04 on this host are kept in
// the package comment: an absolute frame carries motion but its button byte is
// silently dropped.
