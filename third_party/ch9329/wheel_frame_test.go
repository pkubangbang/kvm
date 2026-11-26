package ch9329

import (
	"bytes"
	"testing"
)

// The wheel byte is the difference between a report the host acts on and one it
// ignores. reports.go records the measurement that pinned it: a four-byte
// payload is ACKed by the chip and produces ZERO host wheel events, while the
// five-byte form with the 0x01 lead byte produces one event per notch.
//
// This test pins the exact frame for one notch in each direction, computed by
// hand from the layout in reports.go:
//
//	mouseRelReport = [0x01 lead, buttons, dx, dy, wheel]
//	Frame          = 57 AB 00 05 05 <5 data bytes> <sum>
//
// Up (wheel = +1), sum over every byte including the 57 AB header:
//
//	57 AB 00 05 05 01 00 00 00 01 -> 0x0E
//
// Down (wheel = -1 = 0xFF): the wheel byte gains 0xFE over the +1 case, so the
// sum is 0x0E + 0xFE = 0x10C -> 0x0C.
func TestScrollFrameCarriesTheWheelByte(t *testing.T) {
	up := MouseRelFrame(0, 0, 0, +1)
	wantUp := []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x00, 0x00, 0x00, 0x01, 0x0E}
	if !bytes.Equal(up, wantUp) {
		t.Fatalf("up frame = % X; want % X", up, wantUp)
	}

	down := MouseRelFrame(0, 0, 0, -1)
	wantDown := []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x00, 0x00, 0x00, 0xFF, 0x0C}
	if !bytes.Equal(down, wantDown) {
		t.Fatalf("down frame = % X; want % X", down, wantDown)
	}

	// The lead byte and the wheel byte are the two bytes a wrong builder loses.
	if up[5] != 0x01 {
		t.Fatalf("lead byte = 0x%02X; the 0x01 marker is what makes the chip emit a relative report", up[5])
	}
	if up[9] != 0x01 || down[9] != 0xFF {
		t.Fatalf("wheel byte = 0x%02X up / 0x%02X down; want 0x01 / 0xFF", up[9], down[9])
	}
	if up[6]|up[7]|up[8] != 0 {
		t.Fatalf("buttons/dx/dy must be zero on a pure wheel frame; got % X", up[6:9])
	}
}
