//go:build windows

package main

// In-process screen capture (-grab), so a pad measurement needs no ffmpeg, no
// PNG on disk and no second process.
//
// MEASURED on this rig (1920x1080, 2026-11-26): `ffmpeg -f gdigrab` writing one
// PNG to disk costs ~2050 ms per frame; a GDI CopyFromScreen into a Bitmap in
// memory costs ~17-19 ms, of which the copy itself is ~12 ms and the rest is
// allocating the DIB and building a managed Bitmap around the bits. The old path
// therefore paid ~2000 ms to produce a file that was immediately decoded again.
//
// This grabs once into a 32-bpp DIB and reads straight to grey (BT.601 luma,
// integer weights), exactly as loadGray does for a PNG, so the detector sees the
// same plane either way.

import (
	"fmt"
	"syscall"
	"unsafe"
)

var (
	user32                 = syscall.NewLazyDLL("user32.dll")
	gdi32                  = syscall.NewLazyDLL("gdi32.dll")
	procSetProcessDPIAware = user32.NewProc("SetProcessDPIAware")
	procGetDC              = user32.NewProc("GetDC")
	procReleaseDC          = user32.NewProc("ReleaseDC")
	procGetSystemMetrics   = user32.NewProc("GetSystemMetrics")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
	procBitBlt             = gdi32.NewProc("BitBlt")
)

// grabGray captures the virtual desktop and returns it as a luminance plane.
func grabGray() (*gray, error) {
	// Physical pixels: without this, a scaled display hands back a stretched,
	// re-rendered copy and every size read off it would be wrong.
	procSetProcessDPIAware.Call()

	// The virtual desktop, in physical pixels: negative origins allowed.
	x, _, _ := procGetSystemMetrics.Call(76) // SM_XVIRTUALSCREEN
	y, _, _ := procGetSystemMetrics.Call(77) // SM_YVIRTUALSCREEN
	w, _, _ := procGetSystemMetrics.Call(78) // SM_CXVIRTUALSCREEN
	h, _, _ := procGetSystemMetrics.Call(79) // SM_CYVIRTUALSCREEN
	if w == 0 || h == 0 {
		w = 1920
		h = 1080
	}

	srcDC, _, _ := procGetDC.Call(0)
	if srcDC == 0 {
		return nil, fmt.Errorf("GetDC failed")
	}
	defer procReleaseDC.Call(0, srcDC)
	memDC, _, _ := procCreateCompatibleDC.Call(srcDC)
	if memDC == 0 {
		return nil, fmt.Errorf("CreateCompatibleDC failed")
	}
	defer procDeleteDC.Call(memDC)

	var bi bitmapInfo
	bi.bmiHeader.biSize = uint32(unsafe.Sizeof(bi.bmiHeader))
	bi.bmiHeader.biWidth = int32(w)
	bi.bmiHeader.biHeight = -int32(h) // top-down
	bi.bmiHeader.biPlanes = 1
	bi.bmiHeader.biBitCount = 32
	bi.bmiHeader.biCompression = biRGB

	var bits unsafe.Pointer
	dib, _, _ := procCreateDIBSection.Call(memDC, uintptr(unsafe.Pointer(&bi)),
		dibRGBColors, uintptr(unsafe.Pointer(&bits)), 0, 0)
	if dib == 0 || bits == nil {
		return nil, fmt.Errorf("CreateDIBSection failed")
	}
	defer procDeleteObject.Call(dib)
	old, _, _ := procSelectObject.Call(memDC, dib)
	srcRop := uintptr(0x00CC0020) // SRCCOPY
	ok, _, err := procBitBlt.Call(memDC, 0, 0, w, h, srcDC, x, y, srcRop)
	procSelectObject.Call(memDC, old)
	if ok == 0 {
		return nil, fmt.Errorf("BitBlt failed: %v", err)
	}

	// BGRA rows, stride = 4*w (32 bpp rows are DWORD aligned by construction).
	pix := unsafe.Slice((*byte)(bits), int(w*h*4))
	g := &gray{w: int(w), h: int(h), px: make([]float64, int(w*h))}
	for i := 0; i < int(w*h); i++ {
		b := pix[i*4]
		gg := pix[i*4+1]
		r := pix[i*4+2]
		g.px[i] = float64(299*int(r)+587*int(gg)+114*int(b)) / 1000
	}
	return g, nil
}

const (
	biRGB        = 0
	dibRGBColors = 0
)

type bitmapInfoHeader struct {
	biSize          uint32
	biWidth         int32
	biHeight        int32
	biPlanes        uint16
	biBitCount      uint16
	biCompression   uint32
	biSizeImage     uint32
	biXPelsPerMeter int32
	biYPelsPerMeter int32
	biClrUsed       uint32
	biClrImportant  uint32
}

type bitmapInfo struct {
	bmiHeader bitmapInfoHeader
	bmiColors [1]uint32
}
