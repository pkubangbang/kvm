package ch9329

import (
	"bytes"
	"testing"
)

// These vectors are copied verbatim from the WCH CH9329 串口通信协议 V1.0
// manual, §2.2.2 "CMD_SEND_KB_GENERAL_DATA" worked examples. They are the
// ground truth for the frame checksum:
//
//	SUM = HEAD + ADDR + CMD + LEN + DATA   (the 0x57 0xAB header IS included)
//
// The bug this test exists to prevent: computing the sum over addr..data only,
// which is exactly 0x02 low and makes the chip answer with an ERROR frame
// (echo 0xC0|cmd, status 0xE4 = DEF_CMD_ERR_SUM). That bug produced a false
// "the cable is dead" verdict, so it must never be able to come back silently.

func TestFrameMatchesManualWorkedExamples(t *testing.T) {
	cases := []struct {
		name string
		got  []byte
		want []byte
	}{
		{
			name: "'A' key down (usage 0x04)",
			got:  Frame(0x00, CmdHIDKeyboard, []byte{0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00}),
			want: []byte{0x57, 0xAB, 0x00, 0x02, 0x08, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10},
		},
		{
			name: "'A' key release (all keys up)",
			got:  Frame(0x00, CmdHIDKeyboard, []byte{0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}),
			want: []byte{0x57, 0xAB, 0x00, 0x02, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00, 0x0C},
		},
		{
			name: "left Shift + 'A' down (modifier 0x02)",
			got:  Frame(0x00, CmdHIDKeyboard, []byte{0x02, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00}),
			want: []byte{0x57, 0xAB, 0x00, 0x02, 0x08, 0x02, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00, 0x12},
		},
		{
			// The manual's absolute-mouse example, rebuilt through the real
			// builder: a 1280x768 screen, pixel (100,100) -> grid (320,533).
			// Data[0] MUST be the 0x02 marker and buttons MUST be data[1];
			// the old builder emitted `01 40 01 15 02 00 00`, which the chip
			// ACKed and the host ignored.
			name: "absolute mouse, manual example: 1280x768 px(100,100)",
			got: func() []byte {
				gx, gy := PixelsToChip(100, 100, 1280, 768)
				return MouseAbsFrame(0x00, gx, gy, 0)
			}(),
			want: []byte{0x57, 0xAB, 0x00, 0x04, 0x07, 0x02, 0x00, 0x40, 0x01, 0x15, 0x02, 0x00, 0x67},
		},
		{
			name: "absolute mouse left button down (marker + button)",
			got:  MouseAbsFrame(0x01, 0, 0, 0),
			want: []byte{0x57, 0xAB, 0x00, 0x04, 0x07, 0x02, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x10},
		},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if !bytes.Equal(c.got, c.want) {
				t.Fatalf("frame mismatch\n got: % X\nwant: % X", c.got, c.want)
			}
		})
	}
}

// The header must be part of the sum. A header-less sum is 0x02 low for the
// 'A'-down frame; spell that out so the failure mode is documented in code.
func TestChecksumIncludesHeader(t *testing.T) {
	body := []byte{0x00, 0x02, 0x08, 0x00, 0x00, 0x04, 0x00, 0x00, 0x00, 0x00, 0x00}

	correct := Checksum(body) // addr..data, header added by Frame
	if correct != 0x10 {
		t.Fatalf("Checksum(addr..data) = %#02x, want 0x10 (manual's 'A'-down tail)", correct)
	}

	var headerless byte
	for _, b := range body {
		headerless += b
	}
	if headerless != 0x0E {
		t.Fatalf("header-less sum = %#02x, want 0x0E (what the broken probes emitted)", headerless)
	}
	if correct-headerless != 0x02 {
		t.Fatalf("header contributes %#02x, want 0x02 (0x57+0xAB mod 256)", correct-headerless)
	}
}

// A success reply echoes 0x80|cmd and carries status 0x00; an error reply
// echoes 0xC0|cmd and carries a DEF_CMD_ERR_* status (0xE4 for a bad checksum).
// ParseReply must classify both without confusing them.
func TestReplyEchoClassifiesAcceptVsReject(t *testing.T) {
	ok, found := ParseReply([]byte{0x57, 0xAB, 0x00, 0x82, 0x01, 0x00, 0x85})
	if !found {
		t.Fatal("success reply 57 AB 00 82 01 00 85 was not parsed")
	}
	if ok.Cmd != 0x82 {
		t.Fatalf("echo cmd = %#02x, want 0x82", ok.Cmd)
	}
	if !ok.OK() {
		t.Fatalf("status = %#02x, want 0x00 (DEF_CMD_SUCCESS)", ok.Status)
	}

	bad, found := ParseReply([]byte{0x57, 0xAB, 0x00, 0xC2, 0x01, 0xE4, 0xA9})
	if !found {
		t.Fatal("error reply 57 AB 00 C2 01 E4 A9 was not parsed")
	}
	if bad.OK() {
		t.Fatalf("status = %#02x must not be accepted as OK", bad.Status)
	}
	if bad.Status != 0xE4 {
		t.Fatalf("status = %#02x, want 0xE4 (DEF_CMD_ERR_SUM)", bad.Status)
	}
}

// The exact 14-byte GET_INFO reply this host's firmware V3 actually returns.
// ParseReply used to truncate every reply to 7 bytes, which made the 8-byte
// payload undecodable and every Info field structurally unreachable.
func TestParseReplyKeepsPayload(t *testing.T) {
	raw := []byte{0x57, 0xAB, 0x00, 0x81, 0x08, 0x30, 0x01, 0x00, 0x00, 0x46, 0x00, 0x00, 0x00, 0x02}
	r, ok := ParseReply(raw)
	if !ok {
		t.Fatal("14-byte GET_INFO reply was not parsed")
	}
	if len(r.Raw) != 14 {
		t.Fatalf("Raw length = %d, want 14 (payload must be kept)", len(r.Raw))
	}
	if r.Cmd != 0x81 {
		t.Fatalf("cmd = %#02x, want 0x81 (0x01|0x80 = accepted GET_INFO)", r.Cmd)
	}

	info, err := ParseInfo(r.Raw[5:13])
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	// WCH §2.2.1: [0]=version, [1]=0x00, [2]=LED, [3..7]=reserved.
	if info.Version != 0x30 {
		t.Fatalf("version = %#02x, want 0x30 (V1.0 field position)", info.Version)
	}
	if info.Marker != [2]byte{0x30, 0x01} {
		t.Fatalf("marker = % X, want 30 01", info.Marker)
	}
	if info.LEDState != 0x00 {
		t.Fatalf("LED state = %#02x, want 0x00 (Caps/Num/Scroll all off)", info.LEDState)
	}
}

// CMD_HID_MOUSE_REL (0x05) takes FIVE data bytes: a constant 0x01 lead byte, then
// buttons, dx, dy, wheel (protocol V1.3 §2.2.5; the manual's own worked example is
// 57 AB 00 05 05 01 01 00 00 00 0E for a left-button press).
//
// Getting this wrong is invisible from the chip's side: a four-byte payload is
// ACKed with status 0x00 just like the correct one, and only the host can tell you
// which one it acted on. Measured 2026-11-18 at 115200, WH_MOUSE_LL witness and
// control in one process:
//
//	LEN=4  [btn dx dy wheel]        -> "scrolled 5 notches", host wheel events 0
//	LEN=5  [01 btn dx dy wheel]     -> host wheel events 5, delta -120 each
//
// Pinned so the lead byte cannot be optimised away again.
func TestMouseRelFrameIsFiveBytes(t *testing.T) {
	got := MouseRelFrame(0, 0, 0, -1)
	want := []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x00, 0x00, 0x00, 0xFF, 0x0C}
	if !bytes.Equal(got, want) {
		t.Fatalf("relative wheel frame mismatch\n got: % X\nwant: % X", got, want)
	}
}

// The manual's own relative-mouse example, byte for byte: left button down and then
// released. If these ever fail, the checksum or the lead byte has drifted.
func TestMouseRelMatchesManualExamples(t *testing.T) {
	down := MouseRelFrame(0x01, 0, 0, 0)
	wantDown := []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x01, 0x00, 0x00, 0x00, 0x0E}
	if !bytes.Equal(down, wantDown) {
		t.Fatalf("left-button-down frame mismatch\n got: % X\nwant: % X", down, wantDown)
	}
	up := MouseRelFrame(0x00, 0, 0, 0)
	wantUp := []byte{0x57, 0xAB, 0x00, 0x05, 0x05, 0x01, 0x00, 0x00, 0x00, 0x00, 0x0D}
	if !bytes.Equal(up, wantUp) {
		t.Fatalf("left-button-up frame mismatch\n got: % X\nwant: % X", up, wantUp)
	}
}

// Grid zero is a no-op on the chip: measured on this host, (0,0) left the pointer
// where it was while (1,1) moved it to the top-left. The first pixel must therefore
// address 1, and the full-scale end must stay inside the 12-bit grid.
//
// TestPixelsToChipMatchesTheMeasuredGrid pins the mapping the calibration actually
// produced (abs_calibrate.py, 2026-11-18): the chip's grid is scaled by exactly
// screen/4096 with no offset on either axis. Centre, quarters and the far end are
// checked because those are where a wrong divisor or a stray offset shows up.
func TestPixelsToChipMatchesTheMeasuredGrid(t *testing.T) {
	cases := []struct{ px, py, wantX, wantY int }{
		{480, 270, 1024, 1024},
		{960, 540, 2048, 2048},
		{1440, 810, 3072, 3072},
		{1919, 1079, 4094, 4092},
	}
	for _, c := range cases {
		gx, gy := PixelsToChip(c.px, c.py, 1920, 1080)
		if gx != c.wantX || gy != c.wantY {
			t.Fatalf("pixel (%d,%d) -> grid (%d,%d), want (%d,%d)",
				c.px, c.py, gx, gy, c.wantX, c.wantY)
		}
	}
}

// Round trip: pixel -> grid -> pixel must land within a pixel of where it started,
// which is the accuracy the calibration run measured (<= 0.5 px residual per axis).
func TestPixelsToChipRoundTrip(t *testing.T) {
	for _, px := range []int{1, 100, 480, 960, 1500, 1918} {
		gx, _ := PixelsToChip(px, 540, 1920, 1080)
		back, _ := ChipToPixels(gx, 0, 1920, 1080)
		if diff := back - float64(px); diff > 2 || diff < -2 {
			t.Fatalf("x round trip %d -> grid %d -> %.2f (delta %.2f px)", px, gx, back, diff)
		}
	}
	for _, py := range []int{1, 100, 270, 540, 900, 1078} {
		_, gy := PixelsToChip(960, py, 1920, 1080)
		_, back := ChipToPixels(0, gy, 1920, 1080)
		if diff := back - float64(py); diff > 2 || diff < -2 {
			t.Fatalf("y round trip %d -> grid %d -> %.2f (delta %.2f px)", py, gy, back, diff)
		}
	}
}

// The points abs_calibrate.py actually measured. The chip truncates, so a prediction
// may be up to one pixel above the reading; anything more means the model is wrong.
func TestMeasuredCalibrationPoints(t *testing.T) {
	cases := []struct{ gx, gy, px, py int }{
		{100, 2048, 46, 540},
		{1000, 2048, 468, 540},
		{1700, 2048, 797, 540},
		{2400, 2048, 1125, 540},
		{3100, 2048, 1453, 540},
		{3995, 2048, 1873, 540},
		{2048, 100, 960, 26},
		{2048, 1000, 960, 263},
		{2048, 1700, 960, 448},
		{2048, 2400, 960, 632},
		{2048, 3100, 960, 817},
		{2048, 3995, 960, 1053},
	}
	for _, c := range cases {
		fx, fy := ChipToPixels(c.gx, c.gy, 1920, 1080)
		if d := fx - float64(c.px); d > 1.5 || d < -1.5 {
			t.Fatalf("grid x %d predicts %.2f, measured %d", c.gx, fx, c.px)
		}
		if d := fy - float64(c.py); d > 1.5 || d < -1.5 {
			t.Fatalf("grid y %d predicts %.2f, measured %d", c.gy, fy, c.py)
		}
	}
}
