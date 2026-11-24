//go:build linux

package cursor

// The ring-buffer coordinate feed.
//
// The shell extension owns the ONE fact only the compositor can answer — where
// the pointer is — and the question the agent asks is always "where is it NOW,
// and where was it in the last second". A message broker (D-Bus, a socket) is
// the wrong shape for that: every frame becomes a delivery guarantee nobody
// needs, and the cheapest frame to lose is exactly the one nobody read.
//
// So the contract is a fixed 1 KiB mapping:
//
//	magic        u32  "KVMR" (0x524D564B)
//	capacity     u32  64 slots
//	write_index  u32  monotonic; slot = idx & (capacity-1)
//	heartbeat    u32  +1 at least once a second, even when the pointer is still
//	record[64]   u32 seq, i32 x, i32 y, u64 timestamp_ns (CLOCK_MONOTONIC)
//
// The writer never blocks and never grows: a full ring overwrites the oldest
// slot, which is the right thing to lose. The reader never writes and never
// sleeps; it re-reads until seq is stable, so a torn frame is retried rather
// than trusted.
//
// File: $XDG_RUNTIME_DIR/kvm-cursor.ring (fallback /tmp/kvm-cursor.ring).

import (
	"encoding/binary"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"
)

const (
	RingMagic      uint32 = 0x524D564B // "KVMR"
	RingCapacity   uint32 = 64         // one second at 60 Hz
	ringHeaderSize        = 64
	// ringRecordSize is 24, not 16: the record is seq u32 + x i32 + y i32 +
	// timestamp u64 = 20 bytes, padded to 24 so each slot stays aligned and
	// the 8-byte timestamp never bleeds into the next slot's seq.
	ringRecordSize = 24
	RingFileSize   = ringHeaderSize + ringRecordSize*int(RingCapacity)

	// ringMaxAge bounds how stale the FILE may be before the position is
	// declared unknown. It is measured from the ring file's mtime, NOT from a
	// record timestamp: the writer rewrites the whole 1600-byte block on every
	// tick (~60 Hz), so the mtime is stamped by the writer's kernel and is host
	// time, while timestamp_ns comes from the extension's own Date.now() and
	// can skew from the host clock by tens of seconds (suspend/resume, an NTP
	// step, a container or VM clock). Using the record clock made a live feed
	// look dead and a long-dead one look fresh, depending on which way the skew
	// happened to point.
	ringMaxAge = 3 * time.Second
)

// RingPath is where the extension publishes the ring.
func RingPath() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "kvm-cursor.ring")
	}
	return "/tmp/kvm-cursor.ring"
}

// Point is one sampled position.
type Point struct {
	X, Y int
	At   time.Time
	Seq  uint32
}

// RingSource reads the newest point, and the recent trail, from the mapping.
//
// It opens the file read-only and mmaps it once; every Position/Recent call is
// pure memory traffic afterwards. The file is re-opened only when it was
// replaced (a fresh session, or the extension restarting), detected by the
// mapping's magic going absent.
type RingSource struct {
	path string

	mu   sync.Mutex
	data []byte
	m    *mmapRegion
	stat os.FileInfo
	size int64
}

// NewRingSource opens the ring at path (empty string = RingPath()).
func NewRingSource(path string) *RingSource {
	if path == "" {
		path = RingPath()
	}
	return &RingSource{path: path}
}

func (s *RingSource) Describe() string { return "ring " + s.path }

// Position returns the newest point if it is fresh.
func (s *RingSource) Position() (int, int, bool) {
	p, ok := s.Latest()
	if !ok {
		return 0, 0, false
	}
	return p.X, p.Y, true
}

// Latest returns the newest published point.
//
// The head is NOT computed from a header index. Measured against the extension
// that actually writes this file: it keeps a 64-slot ring but its record
// sequence (offset 8) counts every report and runs past 64 (observed 1126035),
// while its own slot selection takes that counter modulo 64. Applying
// `seq-1` to a slot number therefore reads a slot the writer never wrote for
// that record: the reader observed a position up to a full lap old while the
// writer had already moved on, which the controller saw as "the pointer did not
// move". The only assumption left is the record layout, so the newest record is
// the one with the greatest timestamp.
func (s *RingSource) Latest() (Point, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.mapLocked()
	if !ok {
		return Point{}, false
	}
	if binary.LittleEndian.Uint32(b[0:4]) != RingMagic {
		return Point{}, false
	}
	// Heartbeat: at least one per second, so a zero heartbeat means no writer.
	if binary.LittleEndian.Uint32(b[12:16]) == 0 {
		return Point{}, false
	}
	var best Point
	found := false
	for i := uint32(0); i < RingCapacity; i++ {
		p, ok := readSlot(b, i)
		if !ok {
			continue
		}
		if !found || p.At.After(best.At) {
			best, found = p, true
		}
	}
	if !found {
		return Point{}, false
	}
	// Liveness is the FILE's age, not the record's: mtime is host time.
	// A stat on every call is deliberate — the writer replaces the file per
	// publish, so caching a FileInfo would be wrong for the same reason a
	// cached mmap is (see mapLocked).
	fi, err := os.Stat(s.path)
	if err != nil || time.Since(fi.ModTime()) > ringMaxAge {
		return Point{}, false
	}
	return best, true
}

// Recent returns up to within of the newest points, newest first. Slots the
// writer has already overwritten are skipped, so a slow reader gets a shorter
// trail rather than a wrong one.
//
// It is built the same way as Latest — by timestamp, newest first — because slot
// arithmetic on the header index is not trustworthy on this writer (see
// Latest).
func (s *RingSource) Recent(within time.Duration) []Point {
	s.mu.Lock()
	defer s.mu.Unlock()
	b, ok := s.mapLocked()
	if !ok || binary.LittleEndian.Uint32(b[0:4]) != RingMagic {
		return nil
	}
	// Same liveness gate as Latest: a dead feed has no trail to offer, and the
	// file's mtime (host clock) is the only trustworthy age here.
	if fi, err := os.Stat(s.path); err != nil || time.Since(fi.ModTime()) > ringMaxAge {
		return nil
	}
	// The `within` window is measured against the FEED'S OWN newest record, not
	// the host clock: within is a relative span ("the last second of motion"),
	// and the writer's clock is monotonic for its own records even when it
	// disagrees with the host's. Filtering against time.Now() would reintroduce
	// the skew bug in a new place — and would silently empty the trail.
	var newest time.Time
	haveNewest := false
	for i := uint32(0); i < RingCapacity; i++ {
		if p, ok := readSlot(b, i); ok && (!haveNewest || p.At.After(newest)) {
			newest, haveNewest = p.At, true
		}
	}
	if !haveNewest {
		return nil
	}
	oldest := newest.Add(-within)
	all := make([]Point, 0, RingCapacity)
	for i := uint32(0); i < RingCapacity; i++ {
		if p, ok := readSlot(b, i); ok && !p.At.Before(oldest) {
			all = append(all, p)
		}
	}
	// Newest first; ties broken by sequence.
	sort.Slice(all, func(i, j int) bool {
		if all[i].At.Equal(all[j].At) {
			return all[i].Seq > all[j].Seq
		}
		return all[i].At.After(all[j].At)
	})
	return all
}

// readSlot reads slot idx, retrying while the writer tears it.
func readSlot(b []byte, idx uint32) (Point, bool) {
	off := ringHeaderSize + int(idx%RingCapacity)*ringRecordSize
	if off+ringRecordSize > len(b) {
		return Point{}, false
	}
	for attempt := 0; attempt < 4; attempt++ {
		s1 := binary.LittleEndian.Uint32(b[off : off+4])
		x := int(int32(binary.LittleEndian.Uint32(b[off+4 : off+8])))
		y := int(int32(binary.LittleEndian.Uint32(b[off+8 : off+12])))
		ns := binary.LittleEndian.Uint64(b[off+12 : off+20])
		s2 := binary.LittleEndian.Uint32(b[off : off+4])
		if s1 == s2 && s1 != 0 {
			return Point{X: x, Y: y, At: time.Unix(0, int64(ns)), Seq: s1}, true
		}
	}
	return Point{}, false
}

// mapLocked reads the ring fresh from disk on every call. The extension
// rewrites the file with REPLACE_DESTINATION on every publish, which swaps
// the inode — any cached mmap of the old inode would stay stale forever —
// so a short read is both simpler and always current.
func (s *RingSource) mapLocked() ([]byte, bool) {
	b, err := os.ReadFile(s.path)
	if err != nil || len(b) < int(RingFileSize) {
		return nil, false
	}
	return b, true
}

// Close unmaps the ring.
func (s *RingSource) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m != nil {
		err := s.m.Close()
		s.m, s.data = nil, nil
		return err
	}
	return nil
}

// Wait blocks until a writer appears and publishes a fresh point, or the
// timeout expires. A short-lived CLI calls this instead of Position so it does
// not report "unknown" merely because it started between two beats.
func (s *RingSource) Wait(timeout time.Duration) (Point, bool) {
	deadline := time.Now().Add(timeout)
	for {
		if p, ok := s.Latest(); ok {
			return p, true
		}
		if time.Now().After(deadline) {
			return Point{}, false
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (s *RingSource) String() string { return fmt.Sprintf("%s (%d B)", s.Describe(), s.size) }
