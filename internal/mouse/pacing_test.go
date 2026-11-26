package mouse

import (
	"testing"
	"time"
)

// fakePort records every frame the emitter writes, with the instant of the
// write, and answers each one with a synthetic chip ACK.
//
// It exists because the defect this test guards is a TIMING property: the
// arithmetic was always right (evenReports sums exactly to the request) while
// the DELIVERY was wrong, because N reports left the port inside one UART burst
// and the host accumulated them as a single fast flick with acceleration
// applied. No pure-arithmetic test can see that. This one can, because it sees
// the clock.
type fakePort struct {
	writes []fakeWrite
	// ack makes each write produce a complete chip reply. When false the write
	// is recorded but ReadAvailable returns nothing, which is the "silent chip"
	// case that must still be paced.
	ack bool
	// nak makes each write produce a well-formed REJECTION (status 0xE4, the
	// command echo carrying the error bit). A driver that discards the reply
	// cannot tell this apart from success, which is the defect this field
	// exists to pin: see TestRejectedReplyIsAnError.
	nak bool
}

type fakeWrite struct {
	at    time.Time
	frame []byte
}

// Complete single-byte-status ACK: 57 AB 00 85 01 00 88 (cmd echoed with 0x80).
var fakeAck = []byte{0x57, 0xAB, 0x00, 0x85, 0x01, 0x00, 0x88}

// Rejected report: same layout, but the command echo carries the ERROR bit
// (0xC0|cmd instead of 0x80|cmd) and the status is 0xE4 = DEF_CMD_ERR_SUM.
// 0x57+0xAB+0x00+0xC5+0x01+0xE4 = 0x3AC -> 0xAC.
var fakeNak = []byte{0x57, 0xAB, 0x00, 0xC5, 0x01, 0xE4, 0xAC}

func (f *fakePort) Write(b []byte) (int, error) {
	cp := make([]byte, len(b))
	copy(cp, b)
	f.writes = append(f.writes, fakeWrite{at: time.Now(), frame: cp})
	return len(b), nil
}

func (f *fakePort) ReadAvailable(timeoutMs int) []byte {
	if f.nak {
		return fakeNak
	}
	if !f.ack {
		return nil
	}
	return fakeAck
}

func (f *fakePort) FlushInput() {}

// reportDelta extracts the signed dx,dy a relative frame carries.
//
// Layout from ch9329.MouseRelFrame → reports.mouseRelReport, verified against
// the wire log (`57 AB 00 05 05 01 00 64 00 00 71` is dx=0x64=100):
//
//	[0]57 [1]AB [2]addr [3]cmd [4]len [5]relMarker(0x01) [6]btn [7]dx [8]dy [9]wheel [10]sum
func reportDelta(t *testing.T, frame []byte) (dx, dy int) {
	t.Helper()
	if len(frame) != 11 {
		t.Fatalf("expected an 11-byte relative frame, got %d: % x", len(frame), frame)
	}
	if frame[5] != 0x01 {
		t.Fatalf("expected the relative marker 0x01 at index 5, got %#x: % x", frame[5], frame)
	}
	return int(int8(frame[7])), int(int8(frame[8]))
}

// A multi-report leg must be paced: consecutive reports must be separated by at
// least the floor, so the host cannot see them as one burst.
//
// Fails on the pre-fix code: with gap 0 (or any gap <= FrameGap) streamAt wrote
// every report back-to-back in the same instant, which is exactly the wire-log
// burst that landed +478 for a +300 request.
func TestReportsArePacedNotBurst(t *testing.T) {
	const want = 300
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0}

	for _, step := range evenReports(want, 0) {
		if err := r.streamAt(0, step[0], step[1], r.Gap); err != nil {
			t.Fatalf("streamAt: %v", err)
		}
	}

	if len(f.writes) < 2 {
		t.Fatalf("expected the %d px request to be split into >=2 reports, got %d", want, len(f.writes))
	}
	for i := 1; i < len(f.writes); i++ {
		gap := f.writes[i].at.Sub(f.writes[i-1].at)
		if gap < reportGapFloor {
			t.Fatalf("reports %d and %d are %v apart, less than the %v floor — they leave as one burst",
				i-1, i, gap, reportGapFloor)
		}
	}
}

// The sum of what went on the wire must equal the request, so pacing cannot have
// changed the DELTA math (streamAt clamps to ±127, evenReports splits there).
func TestPacedReportsSumToRequest(t *testing.T) {
	for _, want := range []int{0, 1, 5, 126, 127, 128, 255, 300, 300, -300, -1} {
		f := &fakePort{ack: true}
		r := &Rel{io: f, Gap: 0}
		for _, step := range evenReports(want, 0) {
			if err := r.streamAt(0, step[0], step[1], r.Gap); err != nil {
				t.Fatalf("streamAt: %v", err)
			}
		}
		sum := 0
		reports := 0
		for _, w := range f.writes {
			dx, dy := reportDelta(t, w.frame)
			if dy != 0 {
				t.Fatalf("want=%d: unexpected dy %d", want, dy)
			}
			if dx == 0 {
				continue
			}
			sum += dx
			reports++
		}
		if want == 0 {
			if reports != 0 {
				t.Fatalf("want=0 emitted %d motion reports", reports)
			}
			continue
		}
		if sum != want {
			t.Fatalf("want=%d: wire sum is %d (over %d reports)", want, sum, reports)
		}
		for _, w := range f.writes {
			dx, _ := reportDelta(t, w.frame)
			if dx > 127 || dx < -127 {
				t.Fatalf("want=%d: report dx=%d exceeds one HID field", want, dx)
			}
		}
	}
}

// A silent chip must not collapse the pacing: some firmware never ACKs a HID
// report, and the floor has to survive that case too.
func TestReportsArePacedWhenTheChipIsSilent(t *testing.T) {
	f := &fakePort{ack: false}
	r := &Rel{io: f, Gap: 0}
	for _, step := range evenReports(200, 0) {
		if err := r.streamAt(0, step[0], step[1], r.Gap); err != nil {
			t.Fatalf("streamAt: %v", err)
		}
	}
	if len(f.writes) < 2 {
		t.Fatalf("expected >=2 reports, got %d", len(f.writes))
	}
	for i := 1; i < len(f.writes); i++ {
		if gap := f.writes[i].at.Sub(f.writes[i-1].at); gap < reportGapFloor {
			t.Fatalf("silent chip: reports %d and %d are only %v apart (< %v)", i-1, i, gap, reportGapFloor)
		}
	}
}
