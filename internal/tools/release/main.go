// Command release sends a button-release report and nothing else: zero
// displacement, zero button mask. It exists to clear a stuck HID button (a
// process killed mid-drag never sends its release, and the host then treats
// every later click as part of a drag).
package main

import (
	"fmt"

	"github.com/pkubangbang/kvm/internal/config"
	"github.com/pkubangbang/kvm/internal/mouse"
)

func main() {
	cfg := config.Load()
	dv, err := mouse.Open(cfg.Port, cfg.Baud, cfg.ScreenW, cfg.ScreenH)
	if err != nil {
		fmt.Println("open:", err)
		return
	}
	defer dv.Close()
	for i := 0; i < 3; i++ {
		dv.Release()
	}
	fmt.Println("released")
}
