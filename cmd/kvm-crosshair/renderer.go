//go:build linux

package main

// renderer is one way of putting the crosshair on screen.
type renderer interface {
	Draw(x, y int) error
	Describe() string
	Close()
}
