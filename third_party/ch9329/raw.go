package ch9329

// RawFrame sends an arbitrary frame (the caller is responsible for building a
// well-formed one) and returns whatever the chip answered within 400 ms.
func (d *Device) RawFrame(b []byte) ([]byte, error) {
	d.Frames++
	d.tracef("-> %s", Hex(b))
	if _, err := d.Port.Write(b); err != nil {
		return nil, err
	}
	raw := d.Port.ReadAvailable(400)
	d.tracef("<- %s", Hex(raw))
	return raw, nil
}
