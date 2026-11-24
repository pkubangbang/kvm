// Package ch9329 implements the WCH CH9329 "USB HID serial bridge" wire
// protocol: a 9600 8N1 byte stream on the host side, USB HID keyboard/mouse
// on the target side.
//
// Frame layout (all commands sent TO the chip):
//
//	0x57 0xAB <addr> <cmd> <len> <data...> <sum>
//	         sum = (0x57 + 0xAB + addr + cmd + len + sum(data)) & 0xFF
//
// The two header bytes ARE part of the checksum (CH9329 串口通信协议 V1.0 §2.1,
// and the manual's worked example: 'A' down ends in 0x10). Omitting them makes
// the sum exactly 0x02 low and the chip answers `57 AB 00 82 01 E4 xx`, i.e.
// command|0xC0 (error frame) with status 0xE4 = DEF_CMD_ERR_SUM.
//
// Reply from the chip (7 bytes):
//
//	0x57 0xAB <addr> <cmd> <status> <sum>     status 0x00 = OK
//
// The chip's CMD space is split: 0x00-0x1F are chip control / config
// commands, 0x80 and above are HID reports. Because a status reply echoes
// the *request* command byte, a keyboard report (0x02) echoes back as
// `57 AB 00 C1 00 xx` — that 0xC1 is not an error, it is `0x80|0x02`.
package ch9329

import (
	"fmt"
	"time"
)

// Chip control commands.
const (
	CmdGetInfo       = 0x01
	CmdSetUsbCfg     = 0x02 // NOTE: 0x02 doubles as the HID keyboard report cmd (see below)
	CmdGetUsbCfg     = 0x03
	CmdSetProtocol   = 0x04
	CmdGetProtocol   = 0x05
	CmdSetKeyLed     = 0x0B
	CmdResetChip     = 0x0F
	CmdSetDeviceName = 0x11
	CmdGetDeviceName = 0x12
)

// HID report commands (as seen in the request, i.e. with the 0x80 bit clear
// when they are echoed in a reply).
const (
	CmdHIDKeyboard   = 0x02 // keyboard report: 8 data bytes
	CmdHIDMouseAbs   = 0x04 // absolute mouse: 7 data bytes
	CmdHIDMouseRel   = 0x05 // relative mouse: 5 data bytes
	CmdHIDMouseDigit = 0x0C // (relative) mouse, digit/pen table — rarely used
)

// The keyboard report command and the SetUsbConfig control command collide at
// 0x02. That is not a bug in this package: the direction decides. A request
// with 8 data bytes is a keyboard report; a 1-byte request is a config write.

// Modifier bits for the keyboard report byte 0.
const (
	ModLeftCtrl   = 0x01
	ModLeftShift  = 0x02
	ModLeftAlt    = 0x04
	ModLeftWin    = 0x08
	ModRightCtrl  = 0x10
	ModRightShift = 0x20
	ModRightAlt   = 0x40
	ModRightWin   = 0x80
)

// Mouse button bits for the button byte of either mouse report.
const (
	BtnLeft   = 0x01
	BtnRight  = 0x02
	BtnMiddle = 0x04
)

// Checksum returns the frame checksum of a body (addr, cmd, len, data...).
// Frame header bytes. Both are part of the checksum (see Checksum).
const (
	Head0 = 0x57
	Head1 = 0xAB
)

// Checksum returns the frame checksum per WCH CH9329 串口通信协议 V1.0 §2.1:
//
//	SUM = HEAD + ADDR + CMD + LEN + Σ(DATA)
//
// The two header bytes ARE part of the sum. Omitting them makes every frame
// exactly 0x02 low and the chip answers with an error frame (echo 0xC0|cmd,
// status 0xE4 = DEF_CMD_ERR_SUM) — which really happened in this repo and
// produced a false "the cable is dead" verdict.
func Checksum(body []byte) byte {
	// 0x57 + 0xAB = 0x102, so the wrap must be explicit: byte arithmetic
	// cannot hold the constant sum 258.
	var s byte = (Head0 + Head1) & 0xFF
	for _, b := range body {
		s += b
	}
	return s
}

// Frame builds a complete frame. data may be nil for zero-length commands.
func Frame(addr byte, cmd byte, data []byte) []byte {
	f := make([]byte, 0, len(data)+6)
	body := make([]byte, 0, len(data)+3)
	body = append(body, addr, cmd, byte(len(data)))
	body = append(body, data...)
	f = append(f, 0x57, 0xAB)
	f = append(f, body...)
	f = append(f, Checksum(body))
	return f
}

// replyStatus extracts the command-execution status from a 7-byte reply.
//
//	seg[3] = echoed command (0x80|cmd success, 0xC0|cmd error)
//	seg[4] = LEN when the chip used the length-carrying layout, otherwise DS V1.2 status
//	seg[5] = status when LEN == 1 (the common single-byte status reply)
//
// DS V1.2 puts the status at index 4 unconditionally; firmware V3 answers with
// `57 AB 00 82 01 00 85`, where index 4 is LEN=0x01 and the status 0x00 sits at
// index 5. Distinguish them by checking whether index 4 is a plausible LEN.
func replyStatus(seg []byte, lenByte byte) byte {
	// A LEN of 1 means exactly one data byte follows, at index 5.
	if lenByte == 1 {
		return seg[5]
	}
	return seg[4]
}

// Reply is a parsed chip acknowledgement.
type Reply struct {
	Addr     byte
	Cmd      byte // as echoed, i.e. 0x80|request-cmd
	Status   byte
	Checksum byte
	Raw      []byte
}

// OK reports whether the chip accepted the last command.
func (r Reply) OK() bool { return r.Status == 0x00 }

// EchoCmd is the request command byte that produced this reply.
func (r Reply) EchoCmd() byte { return r.Cmd &^ 0x80 }

func (r Reply) String() string {
	return fmt.Sprintf("reply cmd=%#02x echo=%#02x status=%#02x (%s)",
		r.Cmd, r.EchoCmd(), r.Status, StatusText(r.Status))
}

// The chip's command-execution status byte (the byte an error frame carries in
// place of the payload's first data byte). WCH CH9329 串口通信协议 V1.0 §2.3
// defines exactly these codes; the sibling Python reference in ../ch9329 and
// this repo's own tests (`frame_test.go`, the worked `57 AB 00 82 01 E4 xx`
// example in the package doc and Checksum) all agree on this range:
//
//	0x00  DEF_CMD_SUCCESS        executed
//	0xE1  DEF_CMD_ERR_TIMEOUT    serial receive timeout
//	0xE2  DEF_CMD_ERR_HEAD       invalid frame header
//	0xE3  DEF_CMD_ERR_CMD        invalid command code
//	0xE4  DEF_CMD_ERR_SUM        checksum mismatch
//	0xE5  DEF_CMD_ERR_PARA       parameter error
//	0xE6  DEF_CMD_ERR_EXEC       execution failed
//
// An earlier StatusText decoded 0x01..0x07 as ERR_HEAD/ERR_CMD/... — a range
// that appears in no manual and matched nothing the chip actually emits, so a
// real 0xE4 came back as "UNKNOWN" and a 0x01 (a LEN byte misread as status)
// came back as a plausible-looking ERR_HEAD. Keep the table pinned to the
// 0xE1..0xE6 family and the TestStatusTextCodes test.
const (
	StatusOK          = 0x00 // DEF_CMD_SUCCESS
	StatusErrTimeout  = 0xE1 // DEF_CMD_ERR_TIMEOUT
	StatusErrHead     = 0xE2 // DEF_CMD_ERR_HEAD
	StatusErrCmd      = 0xE3 // DEF_CMD_ERR_CMD
	StatusErrChecksum = 0xE4 // DEF_CMD_ERR_SUM
	StatusErrParam    = 0xE5 // DEF_CMD_ERR_PARA
	StatusErrExec     = 0xE6 // DEF_CMD_ERR_EXEC
)

// StatusText decodes the chip status byte into its WCH DEF_CMD_ERR_* name.
func StatusText(s byte) string {
	switch s {
	case StatusOK:
		return "OK"
	case StatusErrTimeout:
		return "ERR_TIMEOUT"
	case StatusErrHead:
		return "ERR_HEAD"
	case StatusErrCmd:
		return "ERR_CMD"
	case StatusErrChecksum:
		return "ERR_CHECKSUM"
	case StatusErrParam:
		return "ERR_PARAM"
	case StatusErrExec:
		return "ERR_EXEC"
	}
	return "UNKNOWN"
}

// ParseReply scans b for the first well-formed reply frame and returns it whole,
// payload included.
//
// Layouts seen in practice:
//
//	short ACK:        57 AB addr cmd 01 status sum   (status at index 5)
//	GET_INFO (V3):    57 AB 00 81 08 30 01 00 00 46 00 00 00 02   (14 bytes)
//
// Index 4 is LEN, not status. A rejection echoes 0xC0|cmd instead of 0x80|cmd,
// and the chip's tail byte is its own diagnostic value rather than the DS V1.2
// checksum, so a well-formed header with a high command bit is accepted.
//
// Returning only 7 bytes (as this used to) truncated the 8-byte GET_INFO
// payload, which made every Info field structurally unreachable.
func ParseReply(b []byte) (Reply, bool) {
	for i := 0; i+7 <= len(b); i++ {
		if b[i] != Head0 || b[i+1] != Head1 {
			continue
		}
		if (b[i+3] & 0x80) == 0 {
			continue // not a reply echo
		}
		lenByte := b[i+4]
		frameLen := 5 + int(lenByte) + 1 // header(2) + addr + cmd + len + payload + sum
		if i+frameLen > len(b) || frameLen > 260 {
			frameLen = 7 // payload not fully arrived; fall back to a short ACK
		}
		seg := b[i : i+frameLen]
		return Reply{
			Addr: seg[2], Cmd: seg[3], Status: replyStatus(seg, lenByte),
			Checksum: seg[len(seg)-1], Raw: append([]byte(nil), seg...),
		}, true
	}
	return Reply{}, false
}

// OK reports whether the chip accepted the last command. A V3 firmware status
// frame carries its payload in byte 4, so only the DS V1.2 convention (status
// byte 0) can be judged here; callers that need the payload read Reply.Raw.
func (r Reply) accepted() bool { return r.Status == 0x00 }

// Info is the decoded payload of a CmdGetInfo reply.
//
// WCH 串口通信协议 V1.0 §2.2.1 defines the returned data as, in order:
//
//	[0]    chip version, e.g. 0x30 = V1.0, 0x31 = V1.1
//	[1]    0x00 (constant on V1.x firmware; 0x01 is seen on this host's V3)
//	[2]    keyboard LED state: bit0 Num, bit1 Caps, bit2 Scroll
//	[3..7] reserved
//
// Earlier versions of this struct read [0,1] as a "marker" and [3] as a USB
// enumeration byte. Neither field exists in the manual: [3] is reserved, so the
// reported "usb=0 (no USB host)" was reading an undefined byte even while
// Windows enumerated the bridge's HID keyboard as Status OK. Do not reintroduce
// a decoded USB-enumeration field without a manual citation.
type Info struct {
	Marker      [2]byte
	Version     byte
	USBSpeed    byte
	LEDState    byte
	Sensitivity byte
	// RawData is the 8-byte identity payload (version, USB state, LED bits,
	// reserved). Frame is the complete reply frame it arrived in.
	RawData []byte
	Frame   []byte
}

// ParseInfo decodes the data payload of a GetInfo reply. Expects the frame
// WITHOUT the 57 AB addr cmd len header and WITHOUT the trailing checksum.
func ParseInfo(data []byte) (Info, error) {
	if len(data) < 3 {
		return Info{}, fmt.Errorf("get-info payload too short: %d bytes", len(data))
	}
	info := Info{
		Marker:   [2]byte{data[0], data[1]},
		Version:  data[0],
		LEDState: data[2],
		RawData:  append([]byte(nil), data...),
	}
	return info, nil
}

// USBSpeedText names the byte-3 value of the info reply.
func USBSpeedText(v byte) string {
	switch v {
	case 0x00:
		return "not enumerated (no USB host)"
	case 0x01:
		return "full speed (12 Mbps)"
	case 0x02:
		return "low speed (1.5 Mbps)"
	}
	return "unknown"
}

// FrameGap is the inter-frame spacing the chip needs. The CH9329 buffers one
// HID report at a time; back-to-back frames overrun and are dropped silently.
//
// WCH's own board reply (bbs.elecfans.com, 2022-07-13) recommends >= 5-20 ms
// between HID packets because a *too fast* stream makes the HOST discard
// input events. We sit at the top of that range; the press->release pair is
// spaced by PressGap, not by FrameGap, so raise both together when a host
// ignores injected input.
const FrameGap = 8 * time.Millisecond

// PressGap is how long a key/button is held before the release frame. It is
// also the spacing between the press and release frames of one keystroke, so
// it doubles as an inter-packet gap. 100 ms is well inside a human typist's
// range and far above the 5-20 ms floor WCH quotes; a short 60 ms hold made
// the host see press+release as one coalesced event (or none at all) on the
// affected machine.
const PressGap = 100 * time.Millisecond
