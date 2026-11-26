//go:build windows

package main

// GDI plumbing for the layered overlay.
//
// Stdlib syscall only: no cgo, no third-party bindings, so GOOS=windows still
// cross-builds from Linux.

import (
	"fmt"
	"syscall"
	"unsafe"
)

const (
	gdiClassName = "kvmCrosshairWindow"

	// GetSystemMetrics indices for the primary display.
	smCXScreen = 0
	smCYScreen = 1

	// WM_DESTROY: the only message this overlay reacts to.
	wmDestroy = 0x0002

	// DIB layout: 32-bit premultiplied BGRA, top-down (negative height) so row 0
	// is the top, which is what internal/crosshair.Draw assumes.
	biSize        = 40
	biBitCount    = 32
	biCompression = 0 // BI_RGB

	// CreateDIBSection's colour-use argument: DIB_RGB_COLORS.
	dibRGBColors = 0
)

// wndProc is the window procedure.
//
// Held in a package-level variable on purpose: syscall.NewCallback allocates a
// trampoline and Windows keeps the pointer for the lifetime of the class. A local
// could be collected and the pump would dispatch into freed memory.
var wndProc = syscall.NewCallback(func(hwnd, msg, wparam, lparam uintptr) uintptr {
	if msg == wmDestroy {
		procPostQuitMessage.Call(0)
		return 0
	}
	rc, _, _ := procDefWindowProcW.Call(hwnd, msg, wparam, lparam)
	return rc
})

// bitmapInfoHeader is BITMAPINFOHEADER.
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

// wndClassExW is WNDCLASSEXW.
type wndClassExW struct {
	cbSize        uint32
	style         uint32
	lpfnWndProc   uintptr
	cbClsExtra    int32
	cbWndExtra    int32
	hInstance     uintptr
	hIcon         uintptr
	hCursor       uintptr
	hbrBackground uintptr
	lpszMenuName  *uint16
	lpszClassName *uint16
	hIconSm       uintptr
}

// moduleHandle returns this process's main module.
//
// A REAL handle, not 0: the class is owned by this module and CreateWindowExW
// looks it up there, so registering and creating against mismatched instances is
// the classic "Cannot find window class".
func moduleHandle() (uintptr, error) {
	h, _, err := procGetModuleHandleW.Call(0)
	if h == 0 {
		return 0, fmt.Errorf("GetModuleHandleW: %v", err)
	}
	return h, nil
}

// create registers the class, opens the layered click-through window and builds
// the DIB the crosshair is painted into.
func (r *win32Renderer) create() error {
	const exStyle = wsExLayered | wsExTransparent | wsExTopmost | wsExToolWindow | wsExNoActivate

	hInst, err := moduleHandle()
	if err != nil {
		return err
	}
	cls, err := syscall.UTF16PtrFromString(gdiClassName)
	if err != nil {
		return err
	}

	// style: ZERO, deliberately. CS_OWNDC|CS_HREDRAW|CS_VREDRAW makes
	// RegisterClassExW fail with ERROR_INVALID_PARAMETER on a layered window, and
	// a layered window never receives WM_PAINT anyway.
	wc := wndClassExW{
		cbSize:        uint32(unsafe.Sizeof(wndClassExW{})),
		lpfnWndProc:   wndProc,
		hInstance:     hInst,
		lpszClassName: cls,
	}
	if atom, _, cerr := procRegisterClassExW.Call(uintptr(unsafe.Pointer(&wc))); atom == 0 {
		return fmt.Errorf("RegisterClassExW %q (hInstance=%#x): atom=0 %v", gdiClassName, hInst, cerr)
	}

	hwnd, _, werr := procCreateWindowExW.Call(
		exStyle,
		uintptr(unsafe.Pointer(cls)),
		0,       // no title
		wsPopup, // no chrome
		0, 0, uintptr(r.w), uintptr(r.h),
		0, 0,
		hInst,
		0,
	)
	if hwnd == 0 {
		return fmt.Errorf("CreateWindowExW %q (hInstance=%#x): %v", gdiClassName, hInst, werr)
	}
	r.hwnd = hwnd
	procShowWindow.Call(hwnd, swShowNoActivate)

	return r.newDIB()
}

// newDIB builds the source surface UpdateLayeredWindow blits from.
//
// UpdateLayeredWindow needs hdcSrc to be a MEMORY DC holding a 32-bpp top-down
// DIB section; hdcDst is a screen DC. Handing it a window DC as hdcSrc is what
// fails with ERROR_GEN_FAILURE.
func (r *win32Renderer) newDIB() error {
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return fmt.Errorf("GetDC(NULL) failed")
	}
	r.mem, _, _ = procCreateCompatibleDC.Call(screen)
	procReleaseDC.Call(0, screen)
	if r.mem == 0 {
		return fmt.Errorf("CreateCompatibleDC failed")
	}

	bi := bitmapInfoHeader{
		biSize:        biSize,
		biWidth:       int32(r.w),
		biHeight:      -int32(r.h), // top-down: row 0 is the screen's top
		biPlanes:      1,
		biBitCount:    biBitCount,
		biCompression: biCompression,
	}
	bmp, _, berr := procCreateDIBSection.Call(
		r.mem,
		uintptr(unsafe.Pointer(&bi)),
		dibRGBColors,
		uintptr(unsafe.Pointer(&r.bits)),
		0,
		0,
	)
	if bmp == 0 || r.bits == nil {
		return fmt.Errorf("CreateDIBSection: %v", berr)
	}
	r.bmp = bmp
	procSelectObject.Call(r.mem, bmp)
	clearDIB(r.bits, r.w, r.h)
	return nil
}

// dibBytes exposes the DIB's pixels as a byte slice for crosshair.Draw.
//
// The slice aliases the DIB memory: Draw writes it and the UpdateLayeredWindow
// that follows reads the same pages. No copy.
func dibBytes(bits unsafe.Pointer, w, h int) []byte {
	if bits == nil {
		return nil
	}
	return unsafe.Slice((*byte)(bits), w*h*4)
}

// clearDIB zeroes the bitmap, which in premultiplied BGRA means fully transparent.
func clearDIB(bits unsafe.Pointer, w, h int) {
	b := dibBytes(bits, w, h)
	for i := range b {
		b[i] = 0
	}
}

// premultiply scales each channel by alpha, which is the format ULW_ALPHA
// expects: 255,32,32,255 becomes 255,32,32,255 but 255,255,255,128 becomes 128.
func premultiply(r, g, b, a byte) (byte, byte, byte) {
	pr := func(c byte) byte { return byte(uint32(c) * uint32(a) / 255) }
	return pr(r), pr(g), pr(b)
}
