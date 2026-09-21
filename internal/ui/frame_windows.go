//go:build windows

package ui

import (
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"
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
	const border = 6
	if pt.Y < border {
		return win.HTTOP
	}
	if pt.Y >= rc.Bottom-border {
		return win.HTBOTTOM
	}
	if pt.X < border {
		return win.HTLEFT
	}
	if pt.X >= rc.Right-border {
		return win.HTRIGHT
	}
	ch := u.metricH
	if ch < 1 {
		ch = cellH
	}
	if pt.Y >= int32(ch) {
		return 0
	}
	cellX := u.pixelToChromeCol(pt.X)
	if hitFrameButton(u.chrome, cellX) >= 0 || u.hitBell(pt.X) || u.hitCaffeine(pt.X) || u.hitPlus(pt.X) || u.hitTab(pt.X) >= 0 {
		return win.HTCLIENT
	}
	return win.HTCAPTION
}

func disableWindowRounding(hwnd win.HWND) {
	// DWMWA_WINDOW_CORNER_PREFERENCE = 33, DWMWCP_DONOTROUND = 1.
	pref := int32(1)
	mod := windows.NewLazySystemDLL("dwmapi.dll")
	proc := mod.NewProc("DwmSetWindowAttribute")
	_, _, _ = proc.Call(uintptr(hwnd), 33, uintptr(unsafe.Pointer(&pref)), unsafe.Sizeof(pref))
}
