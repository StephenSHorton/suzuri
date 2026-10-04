//go:build windows

package ui

import (
	"unsafe"

	"github.com/lxn/win"

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
	// Keep left/right/bottom from DefWindowProc (resize borders) and pull
	// the client top up into the caption so DWM does not paint a native bar.
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

const (
	dwmwaWindowCornerPreference = 33
	dwmwcpDoNotRound            = 1 // DWMWCP_DONOTROUND — square corners
	dwmwcpRound                 = 2 // kept so a stray ROUND=2 is obvious in review
)

// windowCornerPreference is square. Win11 defaults to rounded; we opt out
// everywhere this HWND is configured (create, glass toggle, activate).
func windowCornerPreference() int32 {
	return dwmwcpDoNotRound
}

// applyWindowChromeFrame reapplies NCCALCSIZE after the HWND is registered
// (CreateWindow's first calc runs before uiMap has the winUI) and forces
// square Win11 corners. Shadow comes from the glass/extend path.
func applyWindowChromeFrame(hwnd win.HWND) {
	if hwnd == 0 {
		return
	}
	dwmSetInt32(hwnd, dwmwaWindowCornerPreference, windowCornerPreference())
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win.SWP_FRAMECHANGED|win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE)
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
		Buttons:  u.captionButtons(w, strip),
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
	btns := u.captionButtons(w, strip)
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
	applyWindowChromeFrame(hwnd)
}
