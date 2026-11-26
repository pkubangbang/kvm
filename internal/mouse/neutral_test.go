package mouse

import (
	"testing"
	"time"
)

// These tests pin the neutral-state contract: when a command finishes —
// normally, on a mid-stroke error, or interrupted at the press — the frames it
// left on the wire END with a button-up release and a zero-delta frame, and
// nothing non-neutral comes after. Exit code 0 is not evidence of this (a
// swallowed release also exits 0): the frame sequence is.

// lastNonNeutral reports the index of the last frame carrying a button or
// motion, and whether any frame after it is not a pure neutral frame
// (btn=0, dx=0, dy=0, wheel=0). The contract is: such a frame exists, and
// everything after it is neutral.
func neutralTail(t *testing.T, writes []fakeWrite) (lastLive int, neutralAfter bool) {
	t.Helper()
	lastLive = -1
	for i, w := range writes {
		if len(w.frame) != 11 {
			t.Fatalf("frame %d is %d bytes, want an 11-byte relative frame: % x", i, len(w.frame), w.frame)
		}
		btn, dx, dy, wheel := w.frame[6], w.frame[7], w.frame[8], w.frame[9]
		live := btn != 0 || dx != 0 || dy != 0 || wheel != 0
		if live {
			lastLive = i
		}
	}
	neutralAfter = true
	for _, w := range writes[lastLive+1:] {
		if w.frame[6] != 0 || w.frame[7] != 0 || w.frame[8] != 0 || w.frame[9] != 0 {
			neutralAfter = false
		}
	}
	return lastLive, neutralAfter
}

// A completed held span followed by Neutralize must end in neutral frames: the
// release frame is btn=0, and the final frame on the wire is a zero-delta —
// "stay still", not merely "not moving at this instant".
func TestNeutralizeEndsSpanWithReleaseAndZeroDelta(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: 0x01, down: true}

	// A held span that dies mid-stroke (the error path's shape): the press and
	// some motion are on the wire, then neutralization.
	if err := r.streamAt(r.btn, 10, 5, time.Millisecond); err != nil {
		t.Fatalf("streamAt: %v", err)
	}
	r.Neutralize()

	if len(f.writes) < 2 {
		t.Fatalf("expected press/motion plus >=2 neutral frames, got %d writes", len(f.writes))
	}
	last := f.writes[len(f.writes)-1].frame
	if last[6] != 0 || last[7] != 0 || last[8] != 0 || last[9] != 0 {
		t.Fatalf("final frame is not neutral (btn=%#x dx=%#x dy=%#x wheel=%#x): % x",
			last[6], last[7], last[8], last[9], last)
	}
	lastLive, neutralAfter := neutralTail(t, f.writes)
	if lastLive < 0 {
		t.Fatal("expected at least one live (button/motion) frame before the neutral tail")
	}
	if !neutralAfter {
		t.Fatal("a non-neutral frame follows the last live frame — the span did not end in a neutral state")
	}
	// The release frame itself: the first neutral frame after the live tail
	// must carry btn=0 (all buttons up), regardless of which button was held.
	if f.writes[lastLive+1].frame[6] != 0 {
		t.Fatalf("frame after the live tail has btn=%#x, want 0 (all buttons up)", f.writes[lastLive+1].frame[6])
	}
	if r.Held() {
		t.Fatal("Held() reports true after Neutralize — the flag and the wire disagree")
	}
}

// Neutralize must be idempotent: the wire sees the release/zero-delta sequence
// exactly once even if the normal path and a signal handler both call it.
func TestNeutralizeIsIdempotent(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: 0x01, down: true}

	r.Neutralize()
	n := len(f.writes)
	r.Neutralize()
	r.Neutralize()
	if len(f.writes) != n {
		t.Fatalf("second/third Neutralize wrote %d more frames; the sequence must be emitted once", len(f.writes)-n)
	}
}

// A neutral release must never be swallowed by streamAt's all-zero early
// return — the historical defect where the host kept the button down while the
// type believed it was up (wire log: last inking frame followed by NO btn=00
// frame). release() and Neutralize bypass streamAt on purpose; this pins that.
func TestReleaseFrameIsNeverSwallowed(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: 0x01, down: true}

	if err := r.release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	if len(f.writes) != 1 {
		t.Fatalf("release wrote %d frames, want exactly 1 (it must not be swallowed)", len(f.writes))
	}
	w := f.writes[0].frame
	if w[6] != 0 || w[7] != 0 || w[8] != 0 {
		t.Fatalf("release frame is not all-zero: % x", w)
	}
}

// Every button mask (left/right/middle spans) neutralizes to btn=0 — the
// release frame is button-agnostic, so a right-button span cannot leave
// BtnRight latched.
func TestNeutralizeClearsAnyButtonMask(t *testing.T) {
	for _, btn := range []byte{0x01, 0x02, 0x04} {
		f := &fakePort{ack: true}
		r := &Rel{io: f, Gap: 0, btn: btn, down: true}
		if err := r.streamAt(btn, 3, 0, time.Millisecond); err != nil {
			t.Fatalf("streamAt(btn=%#x): %v", btn, err)
		}
		r.Neutralize()
		last := f.writes[len(f.writes)-1].frame
		if last[6] != 0 {
			t.Fatalf("button %#x: final frame still carries btn=%#x", btn, last[6])
		}
	}
}
