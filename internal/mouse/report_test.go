package mouse

// Layer 1 proof: one CH9329 relative report, byte for byte, from the builder
// through the paced emitter.
//
// docs/report-layer.md is the contract; this file is the enforcement. The
// property tests in law_test.go prove the ARITHMETIC (bounds, exact sum). This
// one proves the WIRE: the exact bytes, including the checksum that covers the
// header.

import (
	"bytes"
	"testing"
)

// sum is the contract's checksum: HEAD0 + HEAD1 + ADDR + CMD + LEN + ΣDATA.
//
// Written out here independently of the library's Checksum so the golden bytes
// are checked against the DOCUMENT, not against the code that produced them —
// a shared bug would otherwise agree with itself.
func sum(frame []byte) byte {
	var s byte
	for _, b := range frame[:len(frame)-1] {
		s += b
	}
	return s
}

func TestGoldenRelativeReport(t *testing.T) {
	cases := []struct {
		name      string
		btn       byte
		dx, dy    int
		wantBytes []byte
	}{
		// Held motion, left button: cmd 0x05, len 5,
		// payload 01 <btn> <dx> <dy> <wheel=0>.
		// sum = 57+AB+00+05+05+01+01+01+01 = 0x110 → 0x10.
		{"held +1 +1", 0x01, 1, 1, []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x01, 0x01, 0x01, 0x00, 0x10}},
		// A negative delta is two's complement: -1 → 0xFF.
		// sum = 57+AB+00+05+05+01+01+FF = 0x10D → 0x0D.
		{"held -1 0", 0x01, -1, 0, []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x01, 0xFF, 0x00, 0x00, 0x0D}},
		// Button up motion (tracking, no drag).
		// sum = 57+AB+00+05+05+01+00+02+00 = 0x10F → 0x0F.
		{"move +2 0", 0x00, 2, 0, []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x00, 0x02, 0x00, 0x00, 0x0F}},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if len(c.wantBytes) != 11 {
				t.Fatalf("golden frame must be 11 bytes, got %d", len(c.wantBytes))
			}
			// The table must satisfy the documented rule on its own.
			if got := sum(c.wantBytes); got != c.wantBytes[10] {
				t.Fatalf("golden checksum byte is wrong: document sum is %#x, table says %#x", got, c.wantBytes[10])
			}
			// And the emitter must write exactly these bytes.
			f := &fakePort{ack: true}
			r := &Rel{io: f, Gap: 0}
			if err := r.streamAt(c.btn, c.dx, c.dy, 0); err != nil {
				t.Fatalf("streamAt: %v", err)
			}
			if len(f.writes) != 1 {
				t.Fatalf("one short report must leave as ONE frame, got %d", len(f.writes))
			}
			if got := f.writes[0].frame; !bytes.Equal(got, c.wantBytes) {
				t.Fatalf("wire frame\n got: % x\nwant: % x", got, c.wantBytes)
			}
		})
	}
}

// A release must be WRITTEN even though it carries no motion.
//
// The pre-fix emitter released by streaming (0,0,0,0), which the all-zero guard
// swallows — so "release" returned nil having sent nothing and the button stayed
// down on the host. This test is the guard against that regressing.
func TestGoldenReleaseIsNeverSwallowed(t *testing.T) {
	for _, btn := range []byte{0x01, 0x02, 0x04} {
		f := &fakePort{ack: true}
		r := &Rel{io: f, Gap: 0, btn: btn}
		// release() drains first; drain() goes through the port seam, so the
		// fake port is enough — no chip, no *Device.
		if err := r.release(); err != nil {
			t.Fatalf("release: %v", err)
		}
		if len(f.writes) != 1 {
			t.Fatalf("btn %#x: release wrote %d frames, want exactly 1", btn, len(f.writes))
		}
		frame := f.writes[0].frame
		if frame[6] != 0x00 {
			t.Fatalf("btn %#x: release frame carries button mask %#x, want 0", btn, frame[6])
		}
		if frame[10] != sum(frame) {
			t.Fatalf("btn %#x: release checksum %#x, document sum %#x", btn, frame[10], sum(frame))
		}
	}
}

// On a held span the press rides the first MOVING report instead of going out as
// a zero-motion frame the page would read as a lift. The first report of a held
// span must therefore already carry the button bit AND the motion.
func TestGoldenPressRidesFirstMove(t *testing.T) {
	if !pressRidesFirstMove {
		t.Skip("this host keeps the standalone press frame")
	}
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: 0x01, down: true}
	if err := r.streamAt(0x01, 0, 0, 0); err != nil {
		t.Fatalf("streamAt: %v", err)
	}
	if len(f.writes) != 0 {
		t.Fatalf("a zero-motion press must be deferred on this host, got %d frame(s)", len(f.writes))
	}
	if err := r.streamAt(0x01, 1, 0, 0); err != nil {
		t.Fatalf("streamAt: %v", err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("the first moving report must be written, got %d", len(f.writes))
	}
	if got := f.writes[0].frame[6]; got != 0x01 {
		t.Fatalf("the first moving report must carry the button bit, got %#x", got)
	}
}
