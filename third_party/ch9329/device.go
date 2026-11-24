package ch9329

import (
	"fmt"
	"time"
)

// Device is a high-level controller: it owns the port, sends frames and
// enforces the inter-frame spacing the chip needs.
type Device struct {
	Port *Port
	// Verbose makes every frame and every reply show up on the trace sink.
	Verbose bool
	// Trace receives one line per frame when Verbose is set.
	Trace func(string)
	// Strict makes a NAK from the chip a hard error. Off by default because
	// some firmwares stay silent on HID reports even when the report is
	// delivered to the host.
	Strict bool
	// Frames counts frames sent.
	Frames int
}

// OpenDevice opens the port and returns a controller.
func OpenDevice(port string, baud int) (*Device, error) {
	p, err := Open(port, baud)
	if err != nil {
		return nil, err
	}
	return &Device{Port: p, Trace: func(string) {}}, nil
}

func (d *Device) tracef(format string, a ...any) {
	if d.Verbose && d.Trace != nil {
		d.Trace(fmt.Sprintf(format, a...))
	}
}

// Close releases the port.
func (d *Device) Close() error { return d.Port.Close() }

// send writes one frame and waits for the chip's acknowledgement.
func (d *Device) send(frame []byte) (Reply, error) {
	d.Frames++
	d.tracef("-> %s", Hex(frame))
	if _, err := d.Port.Write(frame); err != nil {
		return Reply{}, err
	}
	raw := d.Port.ReadAvailable(250)
	r, ok := ParseReply(raw)
	if ok {
		d.tracef("<- %s  ok=%v", Hex(r.Raw), r.OK())
	} else if len(raw) > 0 {
		d.tracef("<- (unparsed) %s", Hex(raw))
	} else {
		d.tracef("<- (no reply)")
	}
	if ok && !r.OK() && d.Strict {
		return r, fmt.Errorf("chip rejected command: %s", r)
	}
	time.Sleep(FrameGap)
	return r, nil
}

// Info probes the chip with CMD_GET_INFO (0x01) and decodes the identity
// payload. A reply of `57 AB 00 C1 00 ...` means the request was accepted;
// some firmware revisions answer with a status-only frame, in which case the
// returned Info is zero-valued and ok reports the status only.
func (d *Device) Info() (Info, []byte, error) {
	f := Frame(0x00, CmdGetInfo, nil)
	d.tracef("-> %s", Hex(f))
	if _, err := d.Port.Write(f); err != nil {
		return Info{}, nil, err
	}
	raw := d.Port.ReadAvailable(400)
	d.tracef("<- %s", Hex(raw))
	r, ok := ParseReply(raw)
	if !ok {
		return Info{}, raw, fmt.Errorf("no parseable reply to GET_INFO (got %d bytes: %s)", len(raw), Hex(raw))
	}
	// Firmware V3 answers GET_INFO with a payload-carrying frame:
	// `57 AB 00 81 08 30 01 00 00 46 00 00 00 02`
	//   cmd = 0x81 (0x01|0x80 = accepted), LEN = 8, then the 8-byte identity
	//   block. Accept the echo for either 0x80|cmd or 0xC0|cmd so a rejection
	//   reports a status instead of silently falling through to the DS branch.
	if (r.Cmd == 0x80|CmdGetInfo || r.Cmd == 0xC0|CmdGetInfo) && len(r.Raw) >= 13 {
		l := int(r.Raw[4])
		if l >= 6 && 5+l <= len(r.Raw) {
			payload := r.Raw[5 : 5+l]
			info, err := ParseInfo(payload)
			if err != nil {
				return Info{}, payload, err
			}
			// Keep the 8-byte identity payload in RawData (ParseInfo sets it) and
			// expose the whole frame separately. Overwriting RawData with the
			// frame made every caller that checked "did we get 8 payload bytes"
			// see 14 and conclude the reply was malformed.
			info.Frame = append([]byte(nil), r.Raw...)
			return info, payload, nil
		}
	}
	// DS V1.2 firmware returns the 18-byte identity block instead.
	for i := 0; i+5 <= len(raw); i++ {
		if raw[i] != 0x57 || raw[i+1] != 0xAB || raw[i+3] != 0xC1 {
			continue
		}
		l := int(raw[i+4])
		if l < 6 || i+5+l > len(raw) {
			continue
		}
		payload := raw[i+5 : i+5+l]
		info, err := ParseInfo(payload)
		if err != nil {
			return Info{}, raw, err
		}
		return info, payload, nil
	}
	return Info{Version: r.Status, RawData: append([]byte(nil), raw...)}, raw, nil
}

// KeyPress sends a key down followed by an all-zero release.
func (d *Device) KeyPress(usage byte, mods byte) error {
	if _, err := d.send(KeyFrame(mods, []byte{usage})); err != nil {
		return err
	}
	time.Sleep(PressGap)
	_, err := d.send(KeyReleaseFrame())
	return err
}

// KeyDown holds a key (no release).
func (d *Device) KeyDown(usage byte, mods byte) error {
	_, err := d.send(KeyFrame(mods, []byte{usage}))
	return err
}

// KeyUp releases everything.
func (d *Device) KeyUp() error {
	_, err := d.send(KeyReleaseFrame())
	return err
}

// Hotkey presses every key in usages at once with mods, then releases.
func (d *Device) Hotkey(mods byte, usages ...byte) error {
	if _, err := d.send(KeyFrame(mods, usages)); err != nil {
		return err
	}
	time.Sleep(PressGap)
	_, err := d.send(KeyReleaseFrame())
	return err
}

// TypeText types an ASCII string on a US layout. Non-ASCII runes are skipped
// and reported in the returned count of skipped characters: the HID boot
// keyboard has no text input path, so "typing Chinese" would require the
// target OS's own input method, not this bridge.
func (d *Device) TypeText(s string, delay time.Duration) (skipped int, err error) {
	for _, r := range s {
		u, shift, kerr := keyUsage(string(r))
		if kerr != nil || u == 0 {
			if r == '\r' {
				continue
			}
			skipped++
			continue
		}
		var mods byte
		if shift {
			mods = ModLeftShift
		}
		if u == 0x28 && r == '\n' {
			mods = 0
		}
		if err = d.KeyPress(u, mods); err != nil {
			return skipped, err
		}
		if delay > 0 {
			time.Sleep(delay)
		}
	}
	return skipped, nil
}

// MouseMoveAbs moves the pointer to chip-space coordinates (0..4095).
func (d *Device) MouseMoveAbs(x, y int) error {
	_, err := d.send(MouseAbsFrame(0, x, y, 0))
	return err
}

// MouseMovePixels moves the pointer to a pixel coordinate on a (w,h) screen.
func (d *Device) MouseMovePixels(px, py, w, h int) error {
	cx, cy := PixelsToChip(px, py, w, h)
	return d.MouseMoveAbs(cx, cy)
}

// MouseDown presses a button at the current absolute position. The chip needs
// a coordinate with every absolute report, so the last known point is resent;
// callers that do not know it should pass the point they just moved to.
func (d *Device) MouseDown(btn byte, x, y int) error {
	_, err := d.send(MouseAbsFrame(btn, x, y, 0))
	return err
}

// MouseUp releases all buttons at (x,y).
func (d *Device) MouseUp(x, y int) error {
	_, err := d.send(MouseAbsFrame(0, x, y, 0))
	return err
}

// Click is move + press + release at chip-space coordinates.
func (d *Device) Click(btn byte, x, y int, hold time.Duration) error {
	if err := d.MouseMoveAbs(x, y); err != nil {
		return err
	}
	time.Sleep(40 * time.Millisecond)
	if err := d.MouseDown(btn, x, y); err != nil {
		return err
	}
	time.Sleep(hold)
	return d.MouseUp(x, y)
}

// Scroll sends a relative wheel report (positive = up).
func (d *Device) Scroll(notches int) error {
	step := int8(1)
	if notches < 0 {
		step = -1
		notches = -notches
	}
	for i := 0; i < notches; i++ {
		if _, err := d.send(MouseRelFrame(0, 0, 0, step)); err != nil {
			return err
		}
	}
	return nil
}
