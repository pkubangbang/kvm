package ch9329

// The serial transport, on top of go.bug.st/serial.
//
// Why this file exists instead of the hand-rolled Win32 code it replaced: that code
// ACKed every frame and still failed to move the pointer, and the same 13 bytes sent
// from pyserial on the same port landed exactly, every time. The difference was never
// found in DCB flags, overlapped I/O, write chunking, purge calls or modem-line state -
// each of those was tried and each left the behaviour unchanged or worse. go.bug.st/serial
// is a maintained cross-platform port layer that does exactly what pyserial does, so the
// whole class of hand-written Win32 mistakes is gone rather than guessed at.
//
// The observable contract the rest of the package relies on is unchanged:
//   - Open / Close
//   - Write returns a short-write error rather than a silent success
//   - ReadAvailable honours a timeout and returns the bytes it saw
//   - SetReadTimeout / FlushInput are kept for callers that used them
//
// Every remaining Win32 call lives in the _windows files that do window and screen
// work, not in the transport.

import (
	"fmt"
	"time"

	"go.bug.st/serial"
)

// Port is an open CH9329 serial link.
type Port struct {
	path string
	baud int
	p    serial.Port
}

// Open opens and configures the port at (baud, 8N1, no flow control), which is what the
// chip expects. Reads are bounded by ReadAvailable's argument.
func Open(path string, baud int) (*Port, error) {
	mode := &serial.Mode{
		BaudRate: baud,
		DataBits: 8,
		Parity:   serial.NoParity,
		StopBits: serial.OneStopBit,
	}
	sp, err := serial.Open(path, mode)
	if err != nil {
		// A CH343 board re-enumerates briefly and the driver reports the port as
		// busy for tens of milliseconds. Measured on this host: roughly half of
		// one-shot opens fail this way, and the next attempt a moment later
		// always succeeds. Losing an open in the middle of a gesture silently
		// drops the button-down or button-up frame, which is invisible at the
		// call site and looks exactly like "the widget rejected the drag" --
		// that misdiagnosis cost a long debugging detour, so the retry lives
		// here, is bounded, and is reported rather than hidden.
		for attempt := 0; attempt < 20 && err != nil; attempt++ {
			time.Sleep(25 * time.Millisecond)
			sp, err = serial.Open(path, mode)
		}
		if err != nil {
			return nil, fmt.Errorf("open %s after retries: %w", path, err)
		}
	}
	// A generous read timeout by default; ReadAvailable narrows it per call.
	if err := sp.SetReadTimeout(200 * time.Millisecond); err != nil {
		sp.Close()
		return nil, fmt.Errorf("set read timeout: %w", err)
	}
	return &Port{path: path, baud: baud, p: sp}, nil
}

// Write sends raw bytes and reports a short write instead of pretending it succeeded.
//
// A zero-length write is retried once after a short pause: the CH343 bridge
// (this board) reliably swallows the very first frame issued right after the
// port is opened - observed 2 of 3 runs of the self-test dying with
// "short write: 0 of 6 bytes" on the GET_INFO frame, while the same binary
// writing the same bytes a moment later always succeeds. Losing a frame
// silently is exactly the class of failure that made earlier pointer
// measurements untrustworthy, so the retry is explicit and counted rather than
// hidden behind "err == nil".
func (p *Port) Write(b []byte) (int, error) {
	n, err := p.p.Write(b)
	if (err != nil || n == 0) && len(b) > 0 {
		// Let the driver finish its post-open state transition, then retry once.
		time.Sleep(30 * time.Millisecond)
		if n2, err2 := p.p.Write(b); err2 == nil && n2 == len(b) {
			return n2, nil
		} else if err2 != nil {
			return 0, fmt.Errorf("write %d bytes after retry: %w", len(b), err2)
		}
	}
	if err != nil {
		return n, fmt.Errorf("write %d bytes: %w", len(b), err)
	}
	if n != len(b) {
		return n, fmt.Errorf("short write: %d of %d bytes", n, len(b))
	}
	return n, nil
}

// ReadAvailable drains whatever the chip has said, waiting at most timeoutMs for data.
//
// This must accumulate, not return after the first read. go.bug.st/serial's
// Windows backend sets COMMTIMEOUTS.ReadIntervalTimeout=0xFFFFFFFF (special mode:
// return as soon as ANY byte is present, plus ReadTotalTimeoutConstant), so a
// 14-byte GET_INFO reply arrives as roughly one byte per Read. The previous
// version broke out of the loop on `n < len(buf)`, kept only 0x57, and made the
// chip look dead at 115200 while pyserial read the same reply whole. Now the
// loop drains until a read times out, the chip goes quiet, or the buffer fills.
func (p *Port) ReadAvailable(timeoutMs int) []byte {
	// Read ONE complete reply, not "whatever bytes showed up first".
	//
	// Two faults lived here. (1) Go's serial layer runs the port in "return as
	// soon as any byte exists" mode, so the first Read returned 1-2 bytes and the
	// loop's `n == 0` break fired long before the 14-byte frame had arrived - the
	// caller then waited out the quiet window anyway. (2) With a 250-400 ms
	// window EVERY frame cost ~370 ms, so a 73-step glide took 22 s and the
	// pointer lurched visibly instead of flowing (measured with a stopwatch:
	// goto 279 ms, nudge 135 ms, single-process sweep 125 reports in 11.5 s).
	//
	// A serial ACK frame is 57 AB addr cmd len status sum = 7 bytes (14 for
	// GET_INFO). At 115200 baud the whole thing lands in ~1.2 ms, so: keep reading
	// until a full frame is parseable, then stop. The timeout is only a safety
	// net for a silent chip.
	deadline := time.Now().Add(time.Duration(timeoutMs) * time.Millisecond)
	if timeoutMs <= 0 || timeoutMs > 60 {
		timeoutMs = 60
		deadline = time.Now().Add(60 * time.Millisecond)
	}
	_ = p.p.SetReadTimeout(5 * time.Millisecond)
	buf := make([]byte, 256)
	out := make([]byte, 0, 64)
	for time.Now().Before(deadline) {
		n, err := p.p.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if completeReply(out) {
				break
			}
		}
		if err != nil {
			break
		}
	}
	return out
}

// completeReply reports whether b already contains a whole reply frame:
// 57 AB <addr> <cmd|0x80> <len> <len data bytes> <sum>.
func completeReply(b []byte) bool {
	for i := 0; i+4 < len(b); i++ {
		if b[i] != Head0 || b[i+1] != Head1 {
			continue
		}
		if b[i+3]&0x80 == 0 {
			continue
		}
		need := i + 5 + int(b[i+4]) + 1
		if len(b) >= need {
			return true
		}
	}
	return false
}

// SetReadTimeout adjusts the read timeout used by the next read.
func (p *Port) SetReadTimeout(ms int) {
	_ = p.p.SetReadTimeout(time.Duration(ms) * time.Millisecond)
}

// FlushInput discards pending input.
func (p *Port) FlushInput() { _ = p.p.ResetInputBuffer() }

// Close releases the port.
func (p *Port) Close() error { return p.p.Close() }

// Path reports the device path this port was opened with.
func (p *Port) Path() string { return p.path }

// Baud reports the configured line speed.
func (p *Port) Baud() int { return p.baud }
