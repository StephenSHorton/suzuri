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

const (
	smCXFrame        = 32
	smCYFrame        = 33
	smCXPaddedBorder = 92
)

func winFrameBorderPx() (x, y int32) {
	return frameResizeBorder(
		win.GetSystemMetrics(smCXFrame),
		win.GetSystemMetrics(smCYFrame),
		win.GetSystemMetrics(smCXPaddedBorder),
	)
}

func winWorkArea(hwnd win.HWND) frameRect {
	var mi win.MONITORINFO
	mi.CbSize = uint32(unsafe.Sizeof(mi))
	if win.GetMonitorInfo(win.MonitorFromWindow(hwnd, win.MONITOR_DEFAULTTONEAREST), &mi) {
		return frameRect{
			Left: mi.RcWork.Left, Top: mi.RcWork.Top,
			Right: mi.RcWork.Right, Bottom: mi.RcWork.Bottom,
		}
	}
	return frameRect{}
}

func (u *winUI) frameCalcSize(hwnd win.HWND, _ uint32, wParam, lParam uintptr) uintptr {
	// Do not call DefWindowProc. It insets the caption and leaves Windows
	// using mixed NC metrics — the next drag-resize then drops ~7px
	// (SM_CYFRAME + SM_CXPADDEDBORDER) or shifts Y.
	bx, by := winFrameBorderPx()
	zoomed := win.IsZoomed(hwnd)
	work := frameRect{}
	if zoomed {
		work = winWorkArea(hwnd)
	}
	if wParam == 0 {
		r := (*win.RECT)(unsafe.Pointer(lParam))
		got := frameClientFromWindow(frameRect{r.Left, r.Top, r.Right, r.Bottom}, work, zoomed, bx, by)
		r.Left, r.Top, r.Right, r.Bottom = got.Left, got.Top, got.Right, got.Bottom
		return 0
	}
	params := (*ncCalcSizeParams)(unsafe.Pointer(lParam))
	p := params.Rgrc[0]
	got := frameClientFromWindow(frameRect{p.Left, p.Top, p.Right, p.Bottom}, work, zoomed, bx, by)
	params.Rgrc[0] = win.RECT{Left: got.Left, Top: got.Top, Right: got.Right, Bottom: got.Bottom}
	return 0
}

const (
	dwmwaWindowCornerPreference = 33
	dwmwcpDoNotRound            = 1 // DWMWCP_DONOTROUND — square corners
	dwmwcpRound                 = 2 // kept so a stray ROUND=2 is obvious in review

	cChildrenTitleBar    = 5
	stateSystemInvisible = 0x00008000
)

type titleBarInfoEx struct {
	CbSize     uint32
	RcTitleBar win.RECT
	Rgstate    [cChildrenTitleBar + 1]uint32
	Rgrect     [cChildrenTitleBar + 1]win.RECT
}

func hideDWMCaptionButtons(info *titleBarInfoEx) {
	if info == nil {
		return
	}
	// rgrect[2]=min, [3]=max, [4]=help, [5]=close
	for i := 2; i <= 5 && i < len(info.Rgrect); i++ {
		info.Rgstate[i] |= stateSystemInvisible
		info.Rgrect[i] = win.RECT{}
	}
}

// windowCornerPreference is square. Win11 defaults to rounded; we opt out
// everywhere this HWND is configured (create, glass toggle, activate).
func windowCornerPreference() int32 {
	return dwmwcpDoNotRound
}

func handleFrameSysCommand(hwnd win.HWND, wParam uintptr) bool {
	if hwnd == 0 {
		return false
	}
	switch wParam & 0xFFF0 {
	case win.SC_MINIMIZE:
		win.ShowWindow(hwnd, win.SW_MINIMIZE)
		return true
	case win.SC_MAXIMIZE:
		win.ShowWindow(hwnd, win.SW_MAXIMIZE)
		return true
	case win.SC_RESTORE:
		win.ShowWindow(hwnd, win.SW_RESTORE)
		return true
	case win.SC_CLOSE:
		win.PostMessage(hwnd, win.WM_CLOSE, 0, 0)
		return true
	default:
		return false
	}
}

// applyFrameChromeStyle strips WS_SYSMENU/MINIMIZEBOX/MAXIMIZEBOX so a
// sheet-of-glass extend cannot stamp native caption sprites over ours.
func applyFrameChromeStyle(hwnd win.HWND) bool {
	if hwnd == 0 {
		return false
	}
	style := uint32(win.GetWindowLong(hwnd, win.GWL_STYLE))
	next := frameChromeStyle(style)
	if next == style {
		return false
	}
	win.SetWindowLong(hwnd, win.GWL_STYLE, int32(next))
	return true
}

// applyWindowChromeFrame reapplies NCCALCSIZE after the HWND is registered
// (CreateWindow's first calc runs before uiMap has the winUI) and forces
// square Win11 corners. Shadow comes from the glass/extend path.
func applyWindowChromeFrame(hwnd win.HWND) {
	if hwnd == 0 {
		return
	}
	applyFrameChromeStyle(hwnd)
	if u := uiFor(hwnd); u != nil && u.inSizeMove {
		u.glassRefreshDeferred = true
		return
	}
	dwmSetInt32(hwnd, dwmwaWindowCornerPreference, windowCornerPreference())
	win.SetWindowPos(hwnd, 0, 0, 0, 0, 0,
		win.SWP_FRAMECHANGED|win.SWP_NOMOVE|win.SWP_NOSIZE|win.SWP_NOZORDER|win.SWP_NOACTIVATE)
	// FRAMECHANGED resets DwmExtend *and* HostBackdrop. Re-apply the
	// full classic composition (backdrop + extend + accent) — extend
	// alone left accent/HostBackdrop stale after the style change.
	if u := uiFor(hwnd); u != nil {
		if int(uiWatchDepth.Load()) > 1 {
			u.scheduleGlassRefresh()
			return
		}
		u.pushGlassBackdrop(true, 0)
	}
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
