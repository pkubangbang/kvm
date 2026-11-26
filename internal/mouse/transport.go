package mouse

import (
	"fmt"
	"time"

	"ch9329"
)

// portIO is the slice of *ch9329.Port that the relative emitter uses.
//
// It exists so the report PACING can be asserted without a chip: pacing is the
// one property of this package that a healthy-looking unit test could not
// previously reach (every test either compared arithmetic or required the real
// board). *ch9329.Port satisfies this interface as written.
type portIO interface {
	Write(b []byte) (int, error)
	ReadAvailable(timeoutMs int) []byte
	FlushInput()
}

// ackWaitMS bounds the wait for one report's acknowledgement.
//
// ReadAvailable drains a whole reply and stops as soon as one is parseable, so a
// healthy chip costs ~1-2 ms (a 7-byte frame at 115200 baud). The value is only a
// safety net for a silent chip, which is why it matches ch9329's own ceiling.
const ackWaitMS = 60

// reportGapFloor is the minimum spacing between two relative reports.
//
// This is the fix for the burst: three reports written back-to-back inside one
// UART burst are accumulated by the host as a single fast flick and land with
// pointer acceleration applied (measured: +100 commanded lands +100 alone, but
// three such reports in 1 ms land +478). ch9329.FrameGap is the chip's own
// spacing constant, reused rather than duplicated.
const reportGapFloor = ch9329.FrameGap

// port returns the byte sink: the test seam when set, otherwise the real port.
func (r *Rel) port() portIO {
	if r.io != nil {
		return r.io
	}
	return r.dv.d.Port
}

// device returns the underlying chip device, or nil when the controller was
// built with only a test seam (r.dv unset). Bookkeeping must never be the
// reason a test needs a real board.
func (r *Rel) device() *ch9329.Device {
	if r.dv == nil {
		return nil
	}
	return r.dv.d
}

// writeReport writes ONE frame and waits for its acknowledgement before
// returning, so the caller cannot write the next report into the same UART
// burst.
//
// The ACK is the pacing: the chip answers a HID report only after it has
// delivered it, so waiting for that reply is what guarantees "one write, one
// report, one host event". A floor of reportGapFloor is still applied because a
// silent chip (some firmware revisions never answer HID reports) would otherwise
// let the writes stack again; in that case the wait degrades to plain pacing.
//
// gap is the caller's requested spacing. It is treated as a MINIMUM: a caller
// asking for 0 or 2 ms still gets reportGapFloor, which is what makes `-gap 0`
// safe.
func (r *Rel) writeReport(frame []byte, gap time.Duration) error {
	p := r.port()
	start := time.Now()
	if _, err := p.Write(frame); err != nil {
		return err
	}
	if gap < reportGapFloor {
		gap = reportGapFloor
	}
	// Wait for THIS report's ACK, and JUDGE it.
	//
	// This used to be `_ = p.ReadAvailable(ackWaitMS)`: the reply was read and
	// thrown away, so a rejected report (0xE4 DEF_CMD_ERR_SUM, 0xE5 PARA,
	// 0xE6 EXEC) was indistinguishable from success. The emitter then reported
	// "sent" for a frame the chip never executed — the bookkeeping-lying class
	// the whole motion design forbids. A reply that cannot be parsed is still
	// ACCEPTED (some firmware answers in a layout ParseReply does not know, and
	// a silent chip is a documented condition here), but a reply that IS a
	// well-formed echo carrying a non-zero status is returned as an error.
	//
	// The FLOOR is measured from the write, not added after the read: a chip
	// that answers in 0.4 ms (or a fake one that answers instantly) would
	// otherwise satisfy the ACK and still leave the next report 0.4 ms behind —
	// the same burst. So: spend whatever is left of the floor after the ACK.
	reply := p.ReadAvailable(ackWaitMS)
	if elapsed := time.Since(start); elapsed < gap {
		time.Sleep(gap - elapsed)
	}
	if r, ok := ch9329.ParseReply(reply); ok && !r.OK() {
		return fmt.Errorf("chip rejected the report: %s (0x%02X)", ch9329.StatusText(r.Status), r.Status)
	}
	return nil
}
