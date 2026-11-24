//go:build !linux

package cursor

// The ring is a Linux/SysV-mmap feed; other platforms have no publisher.

import "time"

// Point is one sampled position.
type Point struct {
	X, Y int
	At   time.Time
	Seq  uint32
}

// RingSource is a no-op reader on platforms without the feed.
type RingSource struct{}

// NewRingSource returns a reader that never has a position.
func NewRingSource(path string) *RingSource { return &RingSource{} }

func (s *RingSource) Describe() string                         { return "ring (unsupported)" }
func (s *RingSource) Position() (int, int, bool)               { return 0, 0, false }
func (s *RingSource) Latest() (Point, bool)                    { return Point{}, false }
func (s *RingSource) Recent(within time.Duration) []Point      { return nil }
func (s *RingSource) Wait(timeout time.Duration) (Point, bool) { return Point{}, false }
func (s *RingSource) Close() error                             { return nil }
