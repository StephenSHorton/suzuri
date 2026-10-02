//go:build windows

package ui

import (
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

type ncCalcSizeParams struct {
	Rgrc  [3]win.RECT
	Lppos uintptr
}

func (u *winUI) frameCalcSize(hwnd win.HWND, msg uint32, wParam, lParam uintptr) uintptr {
	params := (*ncCalcSizeParams)(unsafe.Pointer(lParam))
	proposed := params.Rgrc[0]
	_ = win.DefWindowProc(hwnd, msg, wParam, lParam)
	params.Rgrc[0].Top = proposed.Top
	if win.IsZoomed(hwnd) {
		var mi win.MONITORINFO
		mi.CbSize = uint32(unsafe.Sizeof(mi))
		if win.GetMonitorInfo(win.MonitorFromWindow(hwnd, win.MONITOR_DEFAULTTONEAREST), &mi) {
			params.Rgrc[0] = mi.RcWork
		}
	}
	return 0
}

func (u *winUI) frameHitTest(hwnd win.HWND, lParam uintptr) uintptr {
	x := int32(int16(lParam & 0xffff))
	y := int32(int16((lParam >> 16) & 0xffff))
	pt := win.POINT{X: x, Y: y}
	if !win.ScreenToClient(hwnd, &pt) {
		return 0
	}
	var rc win.RECT
	if !win.GetClientRect(hwnd, &rc) {
		return 0
	}
	w := rc.Right - rc.Left
	h := rc.Bottom - rc.Top
	strip := u.chromePixelHeight()
	return uintptr(hitTestTitleBar(titleHitQuery{
		X: pt.X, Y: pt.Y,
		ClientW: w, ClientH: h,
		StripH:   strip,
		Buttons:  winCaptionButtons(w, strip),
		Controls: u.chromeControlRects(strip),
	}))
}

// hitCaptionButton is 0 minimize, 1 zoom, 2 close, or -1. Pixel rects, not cells.
func (u *winUI) hitCaptionButton(px, py int32) int {
	if u == nil || u.chrome.Frame != chrome.FrameWindows {
		return -1
	}
	w := u.clientWidth()
	strip := u.chromePixelHeight()
	btns := winCaptionButtons(w, strip)
	for i, b := range btns {
		if b.contains(px, py) {
			return i
		}
	}
	return -1
}

func (u *winUI) clientWidth() int32 {
	if u == nil {
		return 0
	}
	if u.hwnd != 0 {
		var rc win.RECT
		if win.GetClientRect(u.hwnd, &rc) && rc.Right > rc.Left {
			return rc.Right - rc.Left
		}
	}
	return u.width
}

// chromeControlRects are tabs, +, bell, and the cup across the title strip.
// The brand mark is not a control — those pixels stay HTCAPTION.
func (u *winUI) chromeControlRects(stripH int32) []pixRect {
	if u == nil || stripH < 1 {
		return nil
	}
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	var out []pixRect
	add := func(b [2]int) {
		if b[1] <= b[0] {
			return
		}
		out = append(out, pixRect{
			L: 4 + int32(b[0])*cw,
			T: 0,
			R: 4 + int32(b[1])*cw,
			B: stripH,
		})
	}
	for _, b := range u.chrome.TabBounds() {
		add(b)
	}
	add(u.chrome.PlusBounds())
	add(u.chrome.BellBounds())
	add(u.chrome.CaffeineBounds())
	return out
}

func disableWindowRounding(hwnd win.HWND) {
	// DWMWA_WINDOW_CORNER_PREFERENCE = 33, DWMWCP_DONOTROUND = 1.
	pref := int32(1)
	mod := windows.NewLazySystemDLL("dwmapi.dll")
	proc := mod.NewProc("DwmSetWindowAttribute")
	_, _, _ = proc.Call(uintptr(hwnd), 33, uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
}
