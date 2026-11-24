//go:build linux

package cursor

import (
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ringSlot is one record as the extension writes it. at makes the record's
// timestamp explicit where a test depends on ORDER: the reader selects the
// newest record by timestamp, because the header index is not a valid slot
// selector on the real writer (it counts past the ring's capacity).
type ringSlot struct {
	seq  uint32
	x, y int32
	at   time.Time
}

// writeRing creates a ring file at path with the given slots written, the way
// the GNOME extension does: header + records, newest at write_index-1.
//
// mtime NOTE: os.WriteFile stamps mtime = now, and liveness is derived from that
// mtime (see Latest). A test that wants a DEAD ring must therefore call
// os.Chtimes AFTER writeRing returns — backdating before the write is silently
// undone, which would make a staleness test pass for the wrong reason.
func writeRing(t *testing.T, path string, idx uint32, slots map[uint32]ringSlot) {
	t.Helper()
	buf := make([]byte, RingFileSize)
	binary.LittleEndian.PutUint32(buf[0:4], RingMagic)
	binary.LittleEndian.PutUint32(buf[4:8], RingCapacity)
	binary.LittleEndian.PutUint32(buf[8:12], idx)
	binary.LittleEndian.PutUint32(buf[12:16], 1) // heartbeat
	for slot, v := range slots {
		seq := v.seq
		x := v.x
		y := v.y
		off := ringHeaderSize + int(slot%RingCapacity)*ringRecordSize
		binary.LittleEndian.PutUint32(buf[off:off+4], seq)
		binary.LittleEndian.PutUint32(buf[off+4:off+8], uint32(x))
		binary.LittleEndian.PutUint32(buf[off+8:off+12], uint32(y))
		at := v.at
		if at.IsZero() {
			at = time.Now()
		}
		binary.LittleEndian.PutUint64(buf[off+12:off+20], uint64(at.UnixNano()))
	}
	if err := os.WriteFile(path, buf, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestRingSourceLatest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "kvm-cursor.ring")
	// The writer fills slot idx-1, then bumps write_index to idx. So with
	// write_index = 6 the NEWEST record is slot 5; slot 6 is the empty next
	// slot. Reading slot 6 (the old behaviour) returned the previous lap's
	// record (10,20) — a full ring behind, which showed up as 1 s of latency.
	old := time.Now().Add(-time.Second)
	writeRing(t, path, 6, map[uint32]ringSlot{
		4: {seq: 4, x: 10, y: 20, at: old},
		5: {seq: 5, x: 100, y: 200, at: old.Add(time.Millisecond)},
	})
	s := NewRingSource(path)
	defer s.Close()
	x, y, ok := s.Position()
	if !ok || x != 100 || y != 200 {
		t.Fatalf("Position() = %d,%d,%v; want 100,200,true", x, y, ok)
	}
}

func TestRingSourceMissingFile(t *testing.T) {
	s := NewRingSource(filepath.Join(t.TempDir(), "absent.ring"))
	defer s.Close()
	if _, _, ok := s.Position(); ok {
		t.Fatal("Position() on a missing ring must be ok=false")
	}
}

func TestRingSourceBadMagic(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.ring")
	if err := os.WriteFile(path, make([]byte, RingFileSize), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewRingSource(path)
	defer s.Close()
	if _, _, ok := s.Position(); ok {
		t.Fatal("Position() with no magic must be ok=false")
	}
}

// TestRingSourceStale pins the LIVENESS contract, which has two independent
// gates and is deliberately NOT derived from the record's own timestamp:
//
//  1. heartbeat == 0 means "no writer" and rejects the feed outright;
//  2. the ring FILE's mtime is the age clock. It is stamped by the local kernel
//     on the writer's replace_contents() call, i.e. in HOST time, unlike
//     timestamp_ns which the extension stamps with its own Date.now().
//
// Gate 2 is what makes suspend/resume and NTP steps harmless: the old code
// compared the record's wall clock against the reader's, so a >3 s disagreement
// between the shell's clock and the reader's silently declared a live feed dead
// (and, symmetrically, a stale feed live). Neither test below could have caught
// that, because the fixture stamped records with the host clock.
func TestRingSourceStale(t *testing.T) {
	dir := t.TempDir()

	// (a) heartbeat 0 = no writer, even though the file was just written and is
	// therefore maximally fresh by mtime.
	hbZero := filepath.Join(dir, "hb-zero.ring")
	buf := make([]byte, RingFileSize)
	binary.LittleEndian.PutUint32(buf[0:4], RingMagic)
	binary.LittleEndian.PutUint32(buf[4:8], RingCapacity)
	binary.LittleEndian.PutUint32(buf[8:12], 1)
	binary.LittleEndian.PutUint32(buf[12:16], 0) // heartbeat 0 = no writer
	off := ringHeaderSize
	binary.LittleEndian.PutUint32(buf[off:off+4], 1)
	binary.LittleEndian.PutUint64(buf[off+12:off+20], uint64(time.Now().UnixNano()))
	if err := os.WriteFile(hbZero, buf, 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewRingSource(hbZero)
	defer s.Close()
	if _, _, ok := s.Position(); ok {
		t.Fatal("Position() with heartbeat 0 must be ok=false")
	}

	// (b) a live heartbeat but an aged FILE must also reject: the writer died
	// without zeroing anything. os.Chtimes MUST come after the write, because
	// os.WriteFile stamps mtime itself and would silently undo it.
	aged := filepath.Join(dir, "aged.ring")
	writeRing(t, aged, 2, map[uint32]ringSlot{
		0: {seq: 1, x: 10, y: 20},
		1: {seq: 2, x: 11, y: 21},
	})
	old := time.Now().Add(-2 * ringMaxAge)
	if err := os.Chtimes(aged, old, old); err != nil {
		t.Fatal(err)
	}
	s2 := NewRingSource(aged)
	defer s2.Close()
	if _, _, ok := s2.Position(); ok {
		t.Fatalf("Position() on a ring whose mtime is %v old must be ok=false (ringMaxAge=%v)", 2*ringMaxAge, ringMaxAge)
	}
	if pts := s2.Recent(time.Minute); pts != nil {
		t.Fatalf("Recent() on a dead feed must be nil, got %d points", len(pts))
	}

	// (c) a record whose TIMESTAMP is old but whose file is fresh must be
	// ACCEPTED. This is the exact inversion of the old contract and the
	// regression guard for it: the record clock is ordering/diagnostic only.
	skewed := filepath.Join(dir, "skewed.ring")
	writeRing(t, skewed, 2, map[uint32]ringSlot{
		0: {seq: 1, x: 30, y: 40, at: time.Now().Add(-time.Hour)},
		1: {seq: 2, x: 31, y: 41, at: time.Now().Add(-time.Hour + time.Millisecond)},
	})
	s3 := NewRingSource(skewed)
	defer s3.Close()
	x, y, ok := s3.Position()
	if !ok || x != 31 || y != 41 {
		t.Fatalf("Position() with a fresh file but hour-old record stamps = %d,%d,%v; want 31,41,true", x, y, ok)
	}
}

func TestRingSourceRecent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ring")
	base := time.Now().Add(-time.Second)
	writeRing(t, path, 5, map[uint32]ringSlot{
		1: {seq: 1, x: 1, y: 1, at: base.Add(1 * time.Millisecond)},
		2: {seq: 2, x: 2, y: 2, at: base.Add(2 * time.Millisecond)},
		3: {seq: 3, x: 3, y: 3, at: base.Add(3 * time.Millisecond)},
		4: {seq: 4, x: 4, y: 4, at: base.Add(4 * time.Millisecond)},
		5: {seq: 5, x: 5, y: 5, at: base.Add(5 * time.Millisecond)},
	})
	s := NewRingSource(path)
	defer s.Close()
	pts := s.Recent(2 * time.Second)
	if len(pts) != 5 {
		t.Fatalf("Recent() = %d points; want 5", len(pts))
	}
	if pts[0].X != 5 {
		t.Fatalf("newest point = %v; want x=5", pts[0])
	}
}
