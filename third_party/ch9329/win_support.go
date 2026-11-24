//go:build windows

package ch9329

import (
	"fmt"
	"syscall"
	"time"
	"unicode/utf16"
)

// mustDLL loads a Win32 library lazily; the first Proc call resolves it.
func mustDLL(name string) *syscall.LazyDLL { return syscall.NewLazyDLL(name) }

// toUTF16 converts a Go string to a NUL-terminated UTF-16 buffer.
func toUTF16(s string) (*uint16, error) {
	if s == "" {
		return nil, fmt.Errorf("empty path")
	}
	u := utf16.Encode([]rune(s))
	u = append(u, 0)
	return &u[0], nil
}

func nowMillis() int64 { return time.Now().UnixMilli() }
