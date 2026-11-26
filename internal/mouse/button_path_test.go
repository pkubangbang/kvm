package mouse

// button_path_test.go — what a CLICK actually puts on the wire.
//
// The live symptom this pins: `kvm mouse abs move X Y` places the pointer
// exactly (verified by the crosshair diff-capture: peaks land on the requested
// row/col), the chip ACKs every report, and yet a click injected with
// `kvm mouse rel down up` does not reach the desktop — Edge's close button,
// clicked twice at its measured position, stayed open.
//
// The question a unit test can settle is narrower than "does Windows deliver
// it": does the CLICK SHAPE the CLI documents emit a button-down frame that
// actually leaves the port? These tests answer that from the bytes, using the
// same fakePort recorder the pacing tests use, so no chip is required.

import (
	"testing"

	"ch9329"
)

// btnFrame is the button byte a relative frame carries (index 6) and a check
// that it is a well-formed 0x05 report.
func btnFrame(t *testing.T, frame []byte) byte {
	t.Helper()
	if len(frame) != 11 || frame[3] != ch9329.CmdHIDMouseRel || frame[5] != 0x01 {
		t.Fatalf("not a relative report: % x", frame)
	}
	return frame[6]
}

// A zero-motion press must reach the port exactly once, and a release must
// follow it. This is `kvm mouse rel down up`, the documented click.
func TestClickWritesPressThenRelease(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: ch9329.BtnLeft}

	if err := r.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if !r.Held() {
		t.Fatalf("Down() returned but the controller does not believe the button is held")
	}
	if len(f.writes) != 1 {
		t.Fatalf("Down() wrote %d reports, want exactly 1 (the press): %v", len(f.writes), frameList(f))
	}
	if got := btnFrame(t, f.writes[0].frame); got != ch9329.BtnLeft {
		t.Fatalf("press frame carries button %#x, want left %#x: % x", got, ch9329.BtnLeft, f.writes[0].frame)
	}

	if err := r.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if r.Held() {
		t.Fatalf("Up() returned but the controller still believes the button is held")
	}
	if len(f.writes) != 2 {
		t.Fatalf("click wrote %d reports, want 2 (press, release): %v", len(f.writes), frameList(f))
	}
	if got := btnFrame(t, f.writes[1].frame); got != 0 {
		t.Fatalf("release frame carries button %#x, want 0: % x", got, f.writes[1].frame)
	}
}

// The same click with the right button: SetButton must reach the press frame,
// because a right-click is only a right-click on the wire.
func TestRightClickCarriesTheRightButton(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: ch9329.BtnLeft}
	if err := r.SetButton(ch9329.BtnRight); err != nil {
		t.Fatalf("SetButton: %v", err)
	}
	if err := r.Down(); err != nil {
		t.Fatalf("Down: %v", err)
	}
	if got := btnFrame(t, f.writes[0].frame); got != ch9329.BtnRight {
		t.Fatalf("right press carries button %#x, want %#x: % x", got, ch9329.BtnRight, f.writes[0].frame)
	}
	if err := r.Up(); err != nil {
		t.Fatalf("Up: %v", err)
	}
	if got := btnFrame(t, f.writes[len(f.writes)-1].frame); got != 0 {
		t.Fatalf("release after a right press carries button %#x, want 0", got)
	}
}

// A held span that MOVES must press on the wire before it moves: a drag whose
// first frame is already a displacement is a hover, not a drag.
//
// On hosts where pressRidesFirstMove is true, strokeSeg defers the press to the
// first moving frame — so the invariant to test is "the first frame that moves
// carries the button", not "a zero-motion frame precedes it".
func TestDragPressesBeforeItMoves(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: ch9329.BtnLeft}

	if err := r.strokeSeg([2]int{0, 0}, [2]int{30, 0}); err != nil {
		t.Fatalf("strokeSeg: %v", err)
	}
	if len(f.writes) < 2 {
		t.Fatalf("strokeSeg wrote %d reports, want a press and at least one step: %v", len(f.writes), frameList(f))
	}
	moved := false
	for i, w := range f.writes {
		dx, dy := reportDelta(t, w.frame)
		if dx == 0 && dy == 0 {
			continue
		}
		moved = true
		if btn := btnFrame(t, w.frame); btn != ch9329.BtnLeft {
			t.Fatalf("report %d moves by (%d,%d) with button %#x: a drag must move with the button held: %v",
				i, dx, dy, btn, frameList(f))
		}
	}
	if !moved {
		t.Fatalf("strokeSeg wrote no moving report): %v", frameList(f))
	}
	if got := btnFrame(t, f.writes[len(f.writes)-1].frame); got != 0 {
		t.Fatalf("strokeSeg ended with button %#x: the span must release", got)
	}
	if r.Held() {
		t.Fatalf("strokeSeg returned with the controller still holding the button")
	}
}

func frameList(f *fakePort) [][]byte {
	out := make([][]byte, len(f.writes))
	for i, w := range f.writes {
		out[i] = w.frame
	}
	return out
}
