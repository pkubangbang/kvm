package ch9329

// HID reports. Each builder returns the *data* payload only; Frame() adds the
// 57 AB addr cmd len ... sum wrapper.

import "math"

// keyFrameReport builds the data payload of a keyboard report. It always has
// 8 bytes: [modifiers, reserved, k0..k5]. Up to six keys may be held at once;
// extra keys are ignored (the chip and the HID boot protocol both cap at 6).
func keyFrameReport(mods byte, usages []byte) []byte {
	d := make([]byte, 8)
	d[0] = mods
	d[1] = 0x00
	for i, u := range usages {
		if i >= 6 {
			break
		}
		d[2+i] = u
	}
	return d
}

// KeyFrame is the complete frame for a raw keyboard report.
func KeyFrame(mods byte, usages []byte) []byte {
	return Frame(0x00, CmdHIDKeyboard, keyFrameReport(mods, usages))
}

// KeyReleaseFrame clears every key and every modifier. This is the "all zeros"
// frame; its checksum is always 0x0C.
func KeyReleaseFrame() []byte {
	return KeyFrame(0x00, nil)
}

// mouseAbsReport builds the 7-byte absolute-mouse payload per
// CH9329 serial protocol V1.0 Sec 2.2.4 (CMD_SEND_MS_ABS_DATA, LEN=7).
//
//	[0] 0x02        FIXED marker - NOT the button byte
//	[1] buttons     bit0 left, bit1 right, bit2 middle
//	[2] x low byte  (bits 0-7 of the 12-bit coordinate)
//	[3] x high byte (bits 8-11)
//	[4] y low byte
//	[5] y high byte
//	[6] wheel       signed: 0x01..0x7F up, 0x81..0xFF down
//
// The marker is mandatory and the manual's worked example pins it:
//
//	57 AB 00 04 07 02 00 40 01 15 02 00 67   (1280x768 screen -> pixel 100,100)
//
// This is where the earlier "absolute mouse is ACKed but ignored" verdict came
// from. The old builder put the button byte at data[0] and shifted the
// coordinates one byte left. The chip still answered 57 AB 00 84 01 00 with
// status 0x00 - an ACK proves the frame parsed, never that the chip executed
// it - while the host got a report whose first byte was not the marker. Same
// class of bug as the 0x05 lead byte below: shift one byte and the only
// witness that can still tell you the truth is the cursor itself.
//
// Coordinates are in a fixed 0..4095 space that the chip scales to the
// target's absolute-pointer range. This is NOT pixel space and NOT
// normalised 0..100: passing pixels is the single most common way to end up
// with a pointer that "moves but clicks the wrong place".
func mouseAbsReport(buttons byte, x, y int, wheel int8) []byte {
	if x < 0 {
		x = 0
	}
	if x > 4095 {
		x = 4095
	}
	if y < 0 {
		y = 0
	}
	if y > 4095 {
		y = 4095
	}
	return []byte{
		absMarker,
		buttons,
		byte(x & 0xFF), byte((x >> 8) & 0x0F),
		byte(y & 0xFF), byte((y >> 8) & 0x0F),
		byte(wheel),
	}
}

// MouseAbsFrame is a complete absolute-mouse frame in chip coordinate space.
func MouseAbsFrame(buttons byte, x, y int, wheel int8) []byte {
	return Frame(0x00, CmdHIDMouseAbs, mouseAbsReport(buttons, x, y, wheel))
}

// CMD_HID_MOUSE_REL (0x05) takes FIVE data bytes: a constant 0x01 lead byte, then
// buttons, dx, dy, wheel (protocol V1.3 §2.2.5, worked example
// 57 AB 00 05 05 01 01 00 00 00 0E = left button down).
//
// This was wrong for a while in the other direction: an earlier revision emitted
// only four bytes ([buttons, dx, dy, wheel], LEN=4) on the theory that the fifth
// was padding. The chip ACCEPTS both shapes with status 0x00, so the ACK told us
// nothing - the difference only shows up on the host. Measured 2026-11-18 at
// 115200 with a WH_MOUSE_LL witness in the same process as the control:
//
//	LEN=4               -> chip "scrolled 5 notches", host wheel events = 0
//	LEN=5, 0x01 lead    -> host wheel events = 5, delta = -120 each
//	absolute frame + wheel (0x04, byte 7) -> host wheel event, delta = +120
//
// The lead byte is what makes the chip parse the packet as a relative-mouse report.
// Keep it, and keep the frame pinned by TestMouseRelFrameIsFiveBytes.
// The fixed first byte of an absolute-mouse report. The manual (Sec 2.2.4)
// requires it to be 0x02; the button byte goes in the next slot. Do not
// "simplify" this away - see mouseAbsReport for what it cost last time.
const absMarker = 0x02

// The fixed first byte of a relative-mouse report is 0x01 (see mouseRelReport).
const relMarker = 0x01

func mouseRelReport(buttons byte, dx, dy int, wheel int8) []byte {
	return []byte{relMarker, buttons, byte(int8(dx)), byte(int8(dy)), byte(wheel)}
}

// MouseRelFrame is a complete relative-mouse frame.
func MouseRelFrame(buttons byte, dx, dy int, wheel int8) []byte {
	return Frame(0x00, CmdHIDMouseRel, mouseRelReport(buttons, dx, dy, wheel))
}

// Absolute-pointer geometry, measured on this host 2026-11-18 (abs_calibrate.py,
// least squares over a 4096-wide sweep, 12 points per axis, port held open, every
// point read twice):
//
//	cursor_x = 0.46875 * grid_x       (12 samples, max residual 0.5 px)
//	cursor_y = 0.263672 * grid_y      (12 samples, max residual 0.5 px)
//
// 0.46875 = 1920/4096 and 0.263672 = 1080/4096 exactly, i.e. the chip's promise is
// literally true: a 4096 grid scaled to the screen, no offset on either axis. The
// datasheet's example (`X_Cur = 4096 * px / X_MAX`) is therefore right, and the
// inverse is
//
//	grid = round(px * 4096 / screen_size)
//
// The earlier idea of an affine correction with a non-zero constant was an artefact
// of a contaminated first sample; the calibration file abs_calibration.json keeps the
// raw points so that claim can be re-derived without re-running anything.
const absGrid = 4096.0

// PixelsToChip converts a pixel coordinate on a screen of (w,h) into the chip's
// absolute grid.
//
// The one rule that is NOT harmless: sending 0 on both axes is a no-op. Measured on
// this host: park at the centre, send grid (0,0), the pointer does not move; send
// (1,1) and it jumps to the top-left. So the first pixel must address grid 1, and the
// result is clamped to 1..4095 - a request for pixel (0,0) would otherwise leave the
// pointer wherever it happened to be, which reads as "the move silently failed".
func PixelsToChip(px, py, w, h int) (int, int) {
	if w <= 0 || h <= 0 {
		return 1, 1
	}
	cx := int(math.Round(float64(px) * absGrid / float64(w)))
	cy := int(math.Round(float64(py) * absGrid / float64(h)))
	if px <= 0 || cx < 1 {
		cx = 1
	}
	if py <= 0 || cy < 1 {
		cy = 1
	}
	if cx > 4095 {
		cx = 4095
	}
	if cy > 4095 {
		cy = 4095
	}
	return cx, cy
}

// ChipToPixels is the forward map, used by tests and by anything that needs to
// predict where a frame will land before sending it.
func ChipToPixels(gx, gy, w, h int) (float64, float64) {
	return float64(gx) * float64(w) / absGrid, float64(gy) * float64(h) / absGrid
}
