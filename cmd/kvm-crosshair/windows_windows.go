//go:build windows

package main

// Win32 plumbing for the crosshair: the singleton (named mutex), the control
// channel (named pipe), and the overlay renderer.
//
// Everything here is stdlib syscall only — no cgo, no third-party Windows
// bindings — so the binary still cross-builds from Linux with GOOS=windows.

import (
	"fmt"
	"syscall"
	"unsafe"

	"github.com/pkubangbang/kvm/internal/crosshair"
)

var (
	kernel32 = syscall.NewLazyDLL("kernel32.dll")
	user32   = syscall.NewLazyDLL("user32.dll")
	gdi32    = syscall.NewLazyDLL("gdi32.dll")

	procCreateMutexW        = kernel32.NewProc("CreateMutexW")
	procGetLastError        = kernel32.NewProc("GetLastError")
	procCreateNamedPipeW    = kernel32.NewProc("CreateNamedPipeW")
	procConnectNamedPipe    = kernel32.NewProc("ConnectNamedPipe")
	procDisconnectNamedPipe = kernel32.NewProc("DisconnectNamedPipe")
	procReadFile            = kernel32.NewProc("ReadFile")
	procWriteFile           = kernel32.NewProc("WriteFile")
	procCreateFileW         = kernel32.NewProc("CreateFileW")
	procCreateWindowExW     = user32.NewProc("CreateWindowExW")
	procDefWindowProcW      = user32.NewProc("DefWindowProcW")
	procRegisterClassExW    = user32.NewProc("RegisterClassExW")
	procUpdateLayeredWindow = user32.NewProc("UpdateLayeredWindow")
	procShowWindow          = user32.NewProc("ShowWindow")
	procDestroyWindow       = user32.NewProc("DestroyWindow")
	procGetDC               = user32.NewProc("GetDC")
	procReleaseDC           = user32.NewProc("ReleaseDC")
	procGetSystemMetrics    = user32.NewProc("GetSystemMetrics")
	procGetModuleHandleW    = kernel32.NewProc("GetModuleHandleW")
	procPostQuitMessage     = user32.NewProc("PostQuitMessage")
	// The message pump. Without these the window's queue is never drained, the
	// window never answers WM_NULL, and Windows marks the process "Not
	// Responding" — which is exactly how the overlay hung: a stale layered
	// surface left on screen with nothing servicing it.
	procPeekMessageW       = user32.NewProc("PeekMessageW")
	procTranslateMessage   = user32.NewProc("TranslateMessage")
	procDispatchMessageW   = user32.NewProc("DispatchMessageW")
	procCreateCompatibleDC = gdi32.NewProc("CreateCompatibleDC")
	procCreateDIBSection   = gdi32.NewProc("CreateDIBSection")
	procSelectObject       = gdi32.NewProc("SelectObject")
	procDeleteObject       = gdi32.NewProc("DeleteObject")
	procDeleteDC           = gdi32.NewProc("DeleteDC")
)

const (
	errorAlreadyExists  = 183
	errorPipeConnected  = 535
	wsExLayered         = 0x00080000
	wsExTransparent     = 0x00000020
	wsExTopmost         = 0x00000008
	wsExToolWindow      = 0x00000080
	wsExNoActivate      = 0x08000000
	wsPopup             = 0x80000000
	swShowNoActivate    = 4
	pipeAccessInbound   = 0x00000001
	pipeTypeMessage     = 0x00000004
	pipeReadmodeMessage = 0x00000002
	invalidHandleValue  = ^uintptr(0)
	genericWrite        = 0x40000000
	openExisting        = 3

	// UpdateLayeredWindow flags and the BLENDFUNCTION that goes with ULW_ALPHA:
	// AC_SRC_OVER with AC_SRC_ALPHA, meaning each pixel's blue/green/red/alpha
	// byte is premultiplied by its own alpha channel.
	// ULW_COLORKEY punches out every pixel that matches the key colour (black),
	// so the zeroed area of a full-screen overlay shows the desktop through it.
	// ULW_ALPHA would instead multiply the destination by each pixel's alpha,
	// which is what turned a zeroed overlay into a black veil.
	ulwColourKey = 0x00000001
	colourKey    = 0x00000000

	// Message-pump constants. PM_REMOVE = 1 (take the message out of the queue).
	pmRemove = 0x0001
	// WM_QUIT: posted by PostQuitMessage; PeekMessageW returns it directly rather
	// than filling the MSG, so checking the return value is how the loop ends.
	wmQuit = 0x0012
)

// ---------------------------------------------------------------------------
// Singleton: a named mutex whose ownership dies with the process.
// ---------------------------------------------------------------------------

type singleton struct{ h uintptr }

// Close releases ownership. The kernel would do this on process death anyway;
// this makes a clean exit immediate.
func (s singleton) Close() {
	if s.h != 0 {
		syscall.CloseHandle(syscall.Handle(s.h))
	}
}

// acquireSingleton returns acquired=false when another process owns the mutex.
//
// This is the Windows equivalent of LOCK_EX|LOCK_NB failing with EWOULDBLOCK: a
// non-error "someone else is already the crosshair".
func acquireSingleton() (singleton, bool, error) {
	name, err := syscall.UTF16PtrFromString(`Local\kvm-crosshair-overlay`)
	if err != nil {
		return singleton{}, false, err
	}
	h, _, callErr := procCreateMutexW.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return singleton{}, false, fmt.Errorf("CreateMutexW: %v", callErr)
	}
	// CreateMutexW succeeds even when the mutex exists; GetLastError returning
	// ERROR_ALREADY_EXISTS is the only distinguisher.
	if last, _, _ := procGetLastError.Call(); last == errorAlreadyExists {
		syscall.CloseHandle(syscall.Handle(h))
		return singleton{}, false, nil
	}
	return singleton{h: h}, true, nil
}

// ---------------------------------------------------------------------------
// Control channel: a named pipe carrying "on" / "off".
// ---------------------------------------------------------------------------

type pipeServer struct {
	handle uintptr
}

func pipePath() string { return `\\.\pipe\kvm-crosshair` }

func listenPipe() (*pipeServer, error) {
	path, err := syscall.UTF16PtrFromString(pipePath())
	if err != nil {
		return nil, err
	}
	h, _, callErr := procCreateNamedPipeW.Call(
		uintptr(unsafe.Pointer(path)),
		pipeAccessInbound,
		pipeTypeMessage|pipeReadmodeMessage,
		1, // one control client at a time is enough
		0, 0, 0, 0,
	)
	if h == invalidHandleValue || syscall.Handle(h) == syscall.InvalidHandle {
		return nil, fmt.Errorf("CreateNamedPipeW: %v", callErr)
	}
	return &pipeServer{handle: h}, nil
}

// Accept blocks for one client and returns the verb it sent.
func (p *pipeServer) Accept() (string, error) {
	ok, _, err := procConnectNamedPipe.Call(p.handle, 0)
	if ok == 0 {
		if last, _, _ := procGetLastError.Call(); last != errorPipeConnected {
			return "", fmt.Errorf("ConnectNamedPipe: %v", err)
		}
	}
	defer procDisconnectNamedPipe.Call(p.handle)
	buf := make([]byte, 32)
	n := uint32(0)
	read, _, rerr := procReadFile.Call(p.handle, uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)), uintptr(unsafe.Pointer(&n)), 0)
	if read == 0 {
		return "", fmt.Errorf("ReadFile: %v", rerr)
	}
	return string(buf[:n]), nil
}

// Close releases the pipe server.
func (p *pipeServer) Close() { syscall.CloseHandle(syscall.Handle(p.handle)) }

// sendPipe writes one control verb to a running daemon.
//
// A missing daemon is reported as "not running", matching the Linux message, so
// `hud off` with no crosshair is the same experience on both platforms.
func sendPipe(msg string) error {
	path, err := syscall.UTF16PtrFromString(pipePath())
	if err != nil {
		return err
	}
	h, _, callErr := procCreateFileW.Call(
		uintptr(unsafe.Pointer(path)),
		genericWrite,
		0, 0, openExisting, 0, 0,
	)
	if h == invalidHandleValue || syscall.Handle(h) == syscall.InvalidHandle {
		return fmt.Errorf("crosshair is not running (%v)", callErr)
	}
	defer syscall.CloseHandle(syscall.Handle(h))
	b := []byte(msg)
	n := uint32(0)
	ok, _, werr := procWriteFile.Call(h, uintptr(unsafe.Pointer(&b[0])),
		uintptr(len(b)), uintptr(unsafe.Pointer(&n)), 0)
	if ok == 0 {
		return fmt.Errorf("write to the control pipe: %v", werr)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Overlay: a layered, click-through, topmost window.
// ---------------------------------------------------------------------------

// win32Renderer paints the crosshair into a DIB section and hands it to
// UpdateLayeredWindow. Reusing internal/crosshair.Draw means the shape, span and
// colour are byte-identical to the X11 overlay — only the transport differs.
type win32Renderer struct {
	hwnd uintptr
	mem  uintptr // memory DC holding the DIB (hdcSrc)
	bmp  uintptr
	bits unsafe.Pointer
	w, h int
}

func newWin32Renderer() (*win32Renderer, error) {
	// Size the overlay to the whole primary display: a smaller window would clip
	// the arms.
	sw, sh, err := screenSize()
	if err != nil {
		return nil, err
	}
	r := &win32Renderer{w: sw, h: sh}
	if err := r.create(); err != nil {
		r.Close()
		return nil, err
	}
	return r, nil
}

// screenSize asks the OS for the primary display size.
//
// Deliberately NOT taken from the chip's absolute grid: they coincide on a
// single display, but the grid is the emitter's address space while the overlay
// must cover what the desktop actually is.
func screenSize() (int, int, error) {
	w, _, _ := procGetSystemMetrics.Call(smCXScreen)
	h, _, _ := procGetSystemMetrics.Call(smCYScreen)
	if w == 0 || h == 0 {
		return 0, 0, fmt.Errorf("no primary display")
	}
	return int(w), int(h), nil
}

func (r *win32Renderer) Describe() string { return "win32 layered window" }

func (r *win32Renderer) Close() {
	if r.bmp != 0 && r.mem != 0 {
		procSelectObject.Call(r.mem, 0)
	}
	if r.bmp != 0 {
		procDeleteObject.Call(r.bmp)
	}
	if r.mem != 0 {
		procDeleteDC.Call(r.mem)
	}
	if r.hwnd != 0 {
		procDestroyWindow.Call(r.hwnd)
	}
}

// Draw erases the previous crosshair and paints one at (x,y).
func (r *win32Renderer) Draw(x, y int) error {
	// Clear to fully transparent, then paint the arms with the shared renderer so
	// the pixels match X11 exactly.
	clearDIB(r.bits, r.w, r.h)
	// The DIB is BGRA, while crosshair.Draw takes RGBA. Passing R,G,B straight
	// through would render the red arms BLUE: swap the channels instead.
	const red, green, blue, alpha = 255, 0, 0, 255
	pr, pg, pb := premultiply(blue, green, red, alpha)
	crosshair.Draw(dibBytes(r.bits, r.w, r.h), r.w, r.h, 0, 0, x, y, pr, pg, pb, alpha)
	return r.present()
}

type point32 struct{ X, Y int32 }

type size32 struct{ CX, CY int32 }

// blendFunction is BLENDFUNCTION.
type blendFunction struct {
	BlendOp             byte
	BlendFlags          byte
	SourceConstantAlpha byte
	AlphaFormat         byte
}

// present pushes the accumulated bitmap to the screen.
//
// hdcDst must be a SCREEN DC and hdcSrc the memory DC holding the DIB; the
// blend function must be AC_SRC_OVER/AC_SRC_ALPHA so per-pixel alpha is honoured.
func (r *win32Renderer) present() error {
	screen, _, _ := procGetDC.Call(0)
	if screen == 0 {
		return fmt.Errorf("GetDC(NULL) failed")
	}
	defer procReleaseDC.Call(0, screen)

	size := size32{int32(r.w), int32(r.h)}
	origin := point32{0, 0}
	// A COLOUR KEY, not per-pixel alpha.
	//
	// The first version passed ULW_ALPHA with AC_SRC_ALPHA: a zeroed pixel then
	// multiplies the destination by its alpha and a full-screen overlay erased
	// the desktop to black. The second passed ULW_ALPHA with AlphaFormat=0, which
	// Windows does not honour — the black kept coming through. ULW_COLORKEY is the
	// combination that actually works for "paint only this shape": every zeroed
	// pixel equals the key colour and is punched out, and the arms are drawn
	// opaque. No blend function is needed with a key.
	ok, _, err := procUpdateLayeredWindow.Call(
		r.hwnd,
		screen,                           // hdcDst: screen DC for the window's position
		0,                                // pptDst: keep the current position
		uintptr(unsafe.Pointer(&size)),   // psize: the surface size
		r.mem,                            // hdcSrc: memory DC with the DIB selected
		uintptr(unsafe.Pointer(&origin)), // pptSrc
		colourKey,                        // pcolorkey: zeroed pixels are transparent
		0,                                // pblend: not used with a colour key
		ulwColourKey,
	)
	if ok == 0 {
		return fmt.Errorf("UpdateLayeredWindow: %v", err)
	}
	return nil
}
