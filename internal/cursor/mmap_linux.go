//go:build linux

package cursor

import (
	"os"
	"syscall"
)

// mmapRegion is a read-only mapping of a published feed file.
type mmapRegion struct {
	data []byte
}

func mmapRead(path string, size int) (*mmapRegion, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	b, err := syscall.Mmap(int(f.Fd()), 0, size, syscall.PROT_READ, syscall.MAP_SHARED)
	if err != nil {
		return nil, err
	}
	return &mmapRegion{data: b}, nil
}

// Bytes exposes the mapping.
func (m *mmapRegion) Bytes() []byte { return m.data }

// Close releases the mapping.
func (m *mmapRegion) Close() error {
	if m.data == nil {
		return nil
	}
	err := syscall.Munmap(m.data)
	m.data = nil
	return err
}
