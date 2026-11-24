# Windows backend — implementation handoff

You will build this on a real Windows machine. This document is the complete
spec: what to call, which constants matter, the traps that cost time, and how to
know it works. It deliberately mirrors the Linux design so the CLI stays the

---

## 1. What must be true when you are done

| Acceptance | How you prove it |
|---|---|
| the overlay is **click-through** | with the crosshair on, click a button in Notepad under the lines — the click must land, not be swallowed |
| nothing on this path moves the pointer | never call `SetCursorPos`; audit with `rg SetCursorPos .` returning nothing |

## 2. Two capabilities, two APIs

### 2.1 Read the pointer (no movement)

```go
var pt winPoint // struct{ X, Y int32 }
r, _, err := procGetCursorPos.Call(uintptr(unsafe.Pointer(&pt)))
```

- `user32!GetCursorPos` returns **physical screen pixels**.
- It never moves anything — there is no side effect.
- Poll it on a timer (8–16 ms). There is no event to subscribe to that is worth
  the complexity (`WH_MOUSE_LL` needs a hook and adds failure modes).

### 2.2 Draw an always-on-top, click-through overlay

`CreateWindowExW` with these extended styles — **all four are required**:

| Style | Value | Why |
|---|---|---|
| `WS_EX_LAYERED` | `0x00080000` | required for `SetLayeredWindowAttributes` and for transparency |
| `WS_EX_TRANSPARENT` | `0x00000020` | **this is what makes it click-through** — hit-testing passes to the window below |
| `WS_EX_TOPMOST` | `0x00000008` | above normal windows |
| `WS_EX_TOOLWINDOW` | `0x00000080` | keeps it out of Alt+Tab and the taskbar |
| `WS_EX_NOACTIVATE` | `0x08000000` | **recommended** — the overlay must never take focus (add it to the four above) |

Window styles: `WS_POPUP | WS_VISIBLE` (`0x80000000 | 0x10000000`), no border,
no caption. Position `(0,0)`, size `GetSystemMetrics(SM_CXSCREEN/SM_CYSCREEN)`.

Then make one colour transparent:

```go
SetLayeredWindowAttributes(hwnd, colourKey, 0, LWA_COLORKEY) // LWA_COLORKEY = 0x1
```

`colourKey` is in `0x00BBGGRR` order. Use magenta `0x00FF00FF`: fill the whole
window with it in `WM_PAINT`, then draw the lines in pure red `0x000000FF`. The
magenta disappears; only the red lines are visible.

> Alternative (nicer edges, more code): create a 32-bit premultiplied DIB section
> and call `UpdateLayeredWindow`. Use it only if you need anti-aliased lines;
> for 1 px lines the colour key is exact and far simpler.

## 3. The paint loop

```
WM_PAINT:
  hdc = BeginPaint(hwnd, &ps)
  GetClientRect            → the full screen rect
  FillRect(full, magenta)  → transparent
  GetCursorPos             → x, y          (LIVE read, every paint)
  FillRect(x,   0, 1, H, red)              the column
  FillRect(0,   y, W, 1, red)              the row
  EndPaint(hwnd, &ps)
```

Drive it with a timer (`SetTimer(hwnd, 1, 8, 0)` → `InvalidateRect(hwnd, 0, 0)`
on `WM_TIMER`), or invalidate from a goroutine. 8 ms matches the Linux tick.

## 4. Go plumbing in this repo

- `x/sys/windows` is already a dependency (`golang.org/x/sys` in `go.mod`).
- Put the file behind `//go:build windows`, in `internal/mouse/`, named
  from it that your Windows file now provides (Go will refuse duplicate
- Syscalls: `syscall.NewLazyDLL("user32.dll")` +
  `NewProc("GetCursorPos")`, etc. `syscall.NewCallback(fn)` wraps a Go func for
  `WNDPROC`. **Keep the callback alive**: assign it to a package-level variable,
  or the GC may collect it and Windows will crash on the next message.
- The message loop must run on a locked OS thread:
  `runtime.LockOSThread()` in the goroutine that creates the window and pumps
  `GetMessage`. Creating the window on one thread and pumping on another is a
  classic crash.

## 5. Traps that cost time (each one is a real failure mode)

1. **`WS_EX_TRANSPARENT` alone is not click-through for layered windows** unless
   the window is also layered — set both, and set the layered attributes.
2. **DPI**: under per-monitor-DPI awareness, `GetCursorPos` returns physical
   pixels while `GetClientRect` may return logical ones. Make the process
   **per-monitor DPI aware** (`SetProcessDpiAwarenessContext(
   DPI_AWARENESS_CONTEXT_PER_MONITOR_AWARE_V2)`) and use physical pixels
   everywhere. Mixing the two puts the crosshair off by the scale factor.
3. **Multi-monitor**: `SM_CXSCREEN` only covers the primary monitor. Use
   `GetSystemMetrics(SM_XVIRTUALSCREEN, SM_YVIRTUALSCREEN, SM_CXVIRTUALSCREEN,
   SM_CYVIRTUALSCREEN)` and offset by the virtual-screen origin, or the crosshair
   is clipped on secondary screens.
4. **Antialiased edges** pick up magenta. Draw axis-aligned `FillRect` only —
   never a `LineTo` with a pen width > 1.
5. **Full-screen exclusive games** bypass DWM composition; no always-on-top
   window is visible there. Document it; it is not fixable.
6. **`InvalidateRect` from another thread** is allowed but cheap to get wrong —
   prefer a timer in the window thread.
7. **Forgetting `EndPaint`** leaks the DC and the window stops repainting
   silently after ~10k paints.

## 6. Verification harness

offline renderer logic, then asserts the drawn lines coincide with the published
pointer position. A Windows equivalent can assert:
`GetCursorPos == the point the paint used`, window `GetWindowLong(GWL_EXSTYLE)`
`FindWindow("kvmCrosshairWindow", nil) == 0`.

Use PowerShell to read back the styles:

```powershell
Add-Type @"
using System; using System.Runtime.InteropServices;
public class W { [DllImport("user32.dll", CharSet=CharSet.Unicode)]
  public static extern IntPtr FindWindow(string c, string n);
  [DllImport("user32.dll")] public static extern int GetWindowLong(IntPtr h, int i); }
"@
$h = [W]::FindWindow("kvmCrosshairWindow", $null)
"hwnd=$h exstyle=0x{0:X}" -f [W]::GetWindowLong($h, -20)
```

Expected: `exstyle` has bits `0x08000000` (NOACTIVATE), `0x00000020`
(TRANSPARENT), `0x00080000` (LAYERED), `0x00000008` (TOPMOST).

## 7. What to send back

- The output of the acceptance table in §1, with a screenshot showing the
  crosshair over a maximised window.
- Anything in §5 that behaved differently on your Windows version.
