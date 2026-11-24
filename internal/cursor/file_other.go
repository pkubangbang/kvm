//go:build !linux

package cursor

import (
	"os"
	"strconv"
	"strings"
	"time"
)

// Position implements Source for a file-backed feed on platforms that publish
// one the same way; a platform with a native query (Windows GetCursorPos,
// macOS CGEventGetLocation) should provide its own Source instead.
func (f *FileSource) Position() (int, int, bool) {
	st, err := os.Stat(f.Path)
	if err != nil {
		return 0, 0, false
	}
	if f.MaxAge > 0 && time.Since(st.ModTime()) > f.MaxAge {
		return 0, 0, false
	}
	data, err := os.ReadFile(f.Path)
	if err != nil {
		return 0, 0, false
	}
	fields := strings.Fields(string(data))
	if len(fields) < 2 {
		return 0, 0, false
	}
	x, e1 := strconv.Atoi(fields[0])
	y, e2 := strconv.Atoi(fields[1])
	if e1 != nil || e2 != nil {
		return 0, 0, false
	}
	return x, y, true
}
