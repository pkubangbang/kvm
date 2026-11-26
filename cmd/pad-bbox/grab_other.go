//go:build !windows

package main

import "fmt"

// grabGray is Windows-only; elsewhere take the screenshot yourself and pass
// -shot. On Linux the X11 capture lives in internal/capture.
func grabGray() (*gray, error) {
	return nil, fmt.Errorf("-grab is only implemented on Windows; use -shot")
}
