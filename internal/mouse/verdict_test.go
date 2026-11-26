package mouse

// The chip's verdict on a report is part of the report's truth.
//
// writeReport used to read the reply and throw it away (`_ = p.ReadAvailable`),
// so a rejection — 0xE4 DEF_CMD_ERR_SUM, the status the chip sends when a frame's
// checksum is wrong — was indistinguishable from success: the emitter reported
// "sent", Rel.down was cleared, and the pointer had not moved. That is the exact
// class of defect the motion design forbids (the bookkeeping must derive from
// what the WIRE accepted, never from what was intended).
//
// These tests pin the two behaviours that matter and the one that must NOT
// regress: a rejection is an error, an acceptance is not, and a silent chip is
// still tolerated (some firmware never answers HID reports; that is a
// documented condition, not a failure).

import (
	"strings"
	"testing"
)

// A well-formed rejection must surface as an error.
func TestRejectedReplyIsAnError(t *testing.T) {
	f := &fakePort{nak: true}
	r := &Rel{io: f, Gap: 0, btn: 0x01}

	err := r.streamAt(0x01, 3, 0, 0)
	if err == nil {
		t.Fatal("streamAt returned nil for a 0xE4 (DEF_CMD_ERR_SUM) rejection: " +
			"the reply was not judged, so a rejected report looks like a delivered one")
	}
	if !strings.Contains(err.Error(), "ERR_CHECKSUM") || !strings.Contains(err.Error(), "0xE4") {
		t.Fatalf("error does not name the chip status, so an operator cannot diagnose it: %v", err)
	}
}

// A normal ACK must NOT be treated as an error, or every report would fail.
func TestAcceptedReplyIsNotAnError(t *testing.T) {
	f := &fakePort{ack: true}
	r := &Rel{io: f, Gap: 0, btn: 0x01}
	if err := r.streamAt(0x01, 3, 0, 0); err != nil {
		t.Fatalf("streamAt reported an error for a 0x00 (DEF_CMD_SUCCESS) reply: %v", err)
	}
}

// A chip that answers nothing must still be paced, not failed. The pacing tests
// cover the timing; this covers the verdict, because a silent chip must not be
// mistaken for a rejecting one.
func TestSilentChipIsNotAnError(t *testing.T) {
	f := &fakePort{ack: false}
	r := &Rel{io: f, Gap: 0, btn: 0x01}
	if err := r.streamAt(0x01, 3, 0, 0); err != nil {
		t.Fatalf("streamAt failed on a silent chip, which is a documented condition, not an error: %v", err)
	}
}
