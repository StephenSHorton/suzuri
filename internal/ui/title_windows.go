//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"
	"golang.org/x/sys/windows"

	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/iconstroke"
)

var procSetPixel = windows.NewLazySystemDLL("gdi32.dll").NewProc("SetPixel")

func setCaptionPixel(hdc win.HDC, x, y int32, color win.COLORREF) {
	if hdc == 0 {
		return
	}
	_, _, _ = procSetPixel.Call(uintptr(hdc), uintptr(x), uintptr(y), uintptr(color))
}

func (u *winUI) releaseTitleFonts() {
	if u == nil {
		return
	}
	if u.titleBrandFont != 0 {
		win.DeleteObject(win.HGDIOBJ(u.titleBrandFont))
		u.titleBrandFont = 0
	}
	if u.titleCupFont != 0 {
		win.DeleteObject(win.HGDIOBJ(u.titleCupFont))
		u.titleCupFont = 0
	}
	u.titleFontPx = 0
}

func (u *winUI) ensureTitleFonts(stripH int32) {
	if u == nil {
		return
	}
	if stripH < 1 {
		stripH = titleStripHeightPx(0)
	}
	if u.titleFontPx == stripH {
		return
	}
	u.releaseTitleFonts()
	u.titleFontPx = stripH
	box := titleBrandBox(stripH)
	u.titleBrandFont = createCJKFont(int(box))
	cup := stripH - 6
	if cup < 12 {
		cup = 12
	}
	u.titleCupFont = createCupFont(int(cup))
}

// titleBrandBox matches paintBrandMark: square, at least 14px, inside the strip.
func titleBrandBox(stripH int32) int32 {
	size := stripH - 8
	if size < 14 {
		size = 14
	}
	if stripH > 2 && size > stripH-2 {
		size = stripH - 2
	}
	if size < 1 {
		return 1
	}
	return size
}

func createCupFont(sizePx int) win.HFONT {
	if sizePx < 10 {
		sizePx = 14
	}
	for _, name := range []string{
		"Segoe UI Symbol",
		"Cascadia Mono",
		"Cascadia Code",
		"Segoe UI",
		"Segoe UI Emoji",
	} {
		h := createNamedFont(name, -int32(sizePx), win.FW_NORMAL)
		if h == 0 {
			continue
		}
		hdc := win.CreateCompatibleDC(0)
		if hdc == 0 {
			win.DeleteObject(win.HGDIOBJ(h))
			continue
		}
		old := win.SelectObject(hdc, win.HGDIOBJ(h))
		ok := fontHasRunes(hdc, '☕')
		win.SelectObject(hdc, old)
		win.DeleteDC(hdc)
		if ok {
			return h
		}
		win.DeleteObject(win.HGDIOBJ(h))
	}
	return 0
}

func (u *winUI) paintBrandMark(hdc win.HDC, stripH int32) {
	if u == nil || hdc == 0 || stripH < 4 {
		return
	}
	u.ensureTitleFonts(stripH)
	font := u.titleBrandFont
	if font == 0 {
		font = u.cjkFont
	}
	if font == 0 {
		return
	}
	size := titleBrandBox(stripH)
	x := int32(8)
	y := (stripH - size) / 2
	if y < 0 {
		y = 0
	}
	rc := win.RECT{Left: x, Top: y, Right: x + size, Bottom: y + size}
	drawCenteredRune(hdc, font, rc, '硯', chrome.PrimR, chrome.PrimG, chrome.PrimB)
}

func (u *winUI) paintCaffeineCup(hdc win.HDC, stripH int32) {
	// Cup is painted once in paintStripTrailingIcons from the shared stroke set.
}

func (u *winUI) paintWinCaption(hdc win.HDC, rect win.RECT) {
	u.paintStripTrailingIcons(hdc, rect)
}

// stripIconDraws is reset at the start of each trailing-icon paint. Tests
// and logs use it to prove bell/coffee/min/max/close each draw once.
var stripIconDraws [5]int

func (u *winUI) paintStripTrailingIcons(hdc win.HDC, rect win.RECT) {
	if u == nil || hdc == 0 || u.chrome.Frame != chrome.FrameWindows {
		return
	}
	clientW := rect.Right - rect.Left
	clientH := rect.Bottom - rect.Top
	if clientW < 1 || clientH < 1 {
		return
	}
	strip := u.chromePixelHeight()
	if strip > clientH {
		strip = clientH
	}
	if strip < 1 {
		return
	}
	for i := range stripIconDraws {
		stripIconDraws[i] = 0
	}
	barR, barG, barB := chrome.BarR, chrome.BarG, chrome.BarB
	fgR, fgG, fgB := chrome.TextR, chrome.TextG, chrome.TextB
	paintChip := func(slot int, kind iconstroke.Kind, box pixRect, fr, fg, fb byte) {
		if box.empty() {
			return
		}
		wr := win.RECT{Left: box.L, Top: box.T, Right: box.R, Bottom: box.B}
		paintOpaqueRGB(hdc, wr, barR, barG, barB)
		s := captionIconSize(box.R-box.L, box.B-box.T)
		cx := (box.L + box.R) / 2
		cy := (box.T + box.B) / 2
		paintCaptionInk(hdc, stripIconInk(kind, cx, cy, s), fr, fg, fb, barR, barG, barB)
		stripIconDraws[slot]++
	}
	chipBox := func(span [2]int) pixRect {
		if span[1] <= span[0] {
			return pixRect{}
		}
		cw := u.metricW
		if cw < 1 {
			cw = cellW
		}
		x0 := 4 + int32(span[0])*cw
		bw := int32(span[1]-span[0]) * cw
		if bw < 4 {
			return pixRect{}
		}
		return pixRect{L: x0, T: 0, R: x0 + bw, B: strip}
	}
	if u.chrome.BellUnread {
		paintChip(0, iconstroke.Bell, chipBox(u.chrome.BellBounds()), chrome.PrimR, chrome.PrimG, chrome.PrimB)
	} else {
		paintChip(0, iconstroke.Bell, chipBox(u.chrome.BellBounds()), chrome.SoftR, chrome.SoftG, chrome.SoftB)
	}
	if u.caffeine != nil && u.caffeine.Active() {
		paintChip(1, iconstroke.Coffee, chipBox(u.chrome.CaffeineBounds()), chrome.PrimR, chrome.PrimG, chrome.PrimB)
	} else {
		paintChip(1, iconstroke.Coffee, chipBox(u.chrome.CaffeineBounds()), chrome.SoftR, chrome.SoftG, chrome.SoftB)
	}
	zoomed := u.hwnd != 0 && win.IsZoomed(u.hwnd)
	for i, b := range u.captionButtons(clientW, strip) {
		if b.empty() {
			continue
		}
		br, bg, bb := barR, barG, barB
		gr, gg, gb := fgR, fgG, fgB
		if u.captionPress && i == u.captionDown {
			br, bg, bb = mixRGB(br, bg, bb, 0, 0, 0, 1, 4)
		} else if i == u.captionHot {
			if i == 2 {
				br, bg, bb = mixRGB(br, bg, bb, 196, 48, 43, 3, 4)
				gr, gg, gb = 255, 255, 255
			} else {
				br, bg, bb = mixRGB(br, bg, bb, 255, 255, 255, 1, 5)
			}
		}
		wr := win.RECT{Left: b.L, Top: b.T, Right: b.R, Bottom: b.B}
		paintOpaqueRGB(hdc, wr, br, bg, bb)
		k := iconstroke.Min
		switch i {
		case 1:
			k = iconstroke.Max
			if zoomed {
				k = iconstroke.Restore
			}
		case 2:
			k = iconstroke.Close
		}
		s := captionIconSize(b.R-b.L, b.B-b.T)
		paintCaptionInk(hdc, stripIconInk(k, (b.L+b.R)/2, (b.T+b.B)/2, s), gr, gg, gb, br, bg, bb)
		stripIconDraws[2+i]++
	}
}

func (u *winUI) trackCaptionHover(hwnd win.HWND, px, py int32) {
	if u == nil || u.chrome.Frame != chrome.FrameWindows {
		return
	}
	hot := u.hitCaptionButton(px, py)
	if hot != u.captionHot {
		u.captionHot = hot
		if hwnd != 0 {
			win.InvalidateRect(hwnd, nil, false)
		}
	}
	if hwnd == 0 || !captionShouldArmLeave(u.captionLeaveTrk, px, py) {
		return
	}
	var tme win.TRACKMOUSEEVENT
	tme.CbSize = uint32(unsafe.Sizeof(tme))
	tme.DwFlags = win.TME_LEAVE
	tme.HwndTrack = hwnd
	if win.TrackMouseEvent(&tme) {
		u.captionLeaveTrk = true
	}
}

func (u *winUI) hitCaptionButtonScreen(lParam uintptr) int {
	if u == nil || u.hwnd == 0 || u.chrome.Frame != chrome.FrameWindows {
		return -1
	}
	pt := win.POINT{
		X: int32(int16(lParam & 0xffff)),
		Y: int32(int16((lParam >> 16) & 0xffff)),
	}
	if !win.ScreenToClient(u.hwnd, &pt) {
		return -1
	}
	return u.hitCaptionButton(pt.X, pt.Y)
}

func (u *winUI) trackCaptionHoverScreen(hwnd win.HWND, lParam uintptr) {
	if u == nil || hwnd == 0 || u.chrome.Frame != chrome.FrameWindows {
		return
	}
	pt := win.POINT{
		X: int32(int16(lParam & 0xffff)),
		Y: int32(int16((lParam >> 16) & 0xffff)),
	}
	if !win.ScreenToClient(hwnd, &pt) {
		return
	}
	hot := u.hitCaptionButton(pt.X, pt.Y)
	if hot != u.captionHot {
		u.captionHot = hot
		win.InvalidateRect(hwnd, nil, false)
	}
	if !captionShouldArmNCLeave(u.captionNCLeaveTrk) {
		return
	}
	var tme win.TRACKMOUSEEVENT
	tme.CbSize = uint32(unsafe.Sizeof(tme))
	tme.DwFlags = win.TME_LEAVE | win.TME_NONCLIENT
	tme.HwndTrack = hwnd
	if win.TrackMouseEvent(&tme) {
		u.captionNCLeaveTrk = true
	}
}

func (u *winUI) clearCaptionHover(hwnd win.HWND) {
	if u == nil {
		return
	}
	hot, trk, _, dirty := captionApplyLeave(u.captionHot, u.captionLeaveTrk)
	u.captionHot = hot
	u.captionPress = false
	u.captionLeaveTrk = trk
	if dirty && hwnd != 0 {
		win.InvalidateRect(hwnd, nil, false)
	}
}

func mixRGB(r, g, b, tr, tg, tb byte, num, den int) (byte, byte, byte) {
	if den < 1 {
		den = 1
	}
	mix := func(a, t byte) byte {
		return byte((int(a)*(den-num) + int(t)*num) / den)
	}
	return mix(r, tr), mix(g, tg), mix(b, tb)
}

func drawCenteredRune(hdc win.HDC, font win.HFONT, r win.RECT, ch rune, cr, cg, cb byte) bool {
	if hdc == 0 || font == 0 || r.Right <= r.Left || r.Bottom <= r.Top {
		return false
	}
	s, err := syscall.UTF16FromString(string(ch))
	if err != nil || len(s) == 0 {
		return false
	}
	old := win.SelectObject(hdc, win.HGDIOBJ(font))
	if old == 0 {
		return false
	}
	defer win.SelectObject(hdc, old)
	win.SetBkMode(hdc, win.TRANSPARENT)
	win.SetTextColor(hdc, win.RGB(cr, cg, cb))
	win.DrawTextEx(hdc, &s[0], -1, &r, win.DT_CENTER|win.DT_VCENTER|win.DT_SINGLELINE|win.DT_NOPREFIX, nil)
	return true
}

func paintOpaqueRGB(hdc win.HDC, r win.RECT, cr, cg, cb byte) {
	if hdc == 0 || r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(cr, cg, cb)}
	brush := win.CreateBrushIndirect(&lb)
	if brush == 0 {
		return
	}
	fillRect(hdc, r, brush)
	win.DeleteObject(win.HGDIOBJ(brush))
}

func paintOpaqueRects(hdc win.HDC, rects []win.RECT, cr, cg, cb byte) {
	if hdc == 0 || len(rects) == 0 {
		return
	}
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(cr, cg, cb)}
	brush := win.CreateBrushIndirect(&lb)
	if brush == 0 {
		return
	}
	for _, r := range rects {
		if r.Right <= r.Left || r.Bottom <= r.Top {
			continue
		}
		fillRect(hdc, r, brush)
	}
	win.DeleteObject(win.HGDIOBJ(brush))
}

// paintCaptionGlyph draws one minimize / maximize-or-restore / close icon.
// Same stroke and AA fringe on all three — no offset shadow copy.
func paintCaptionGlyph(hdc win.HDC, kind int, b pixRect, zoomed bool, cr, cg, cb, br, bg, bb byte) {
	if hdc == 0 {
		return
	}
	s := captionIconSize(b.R-b.L, b.B-b.T)
	cx := (b.L + b.R) / 2
	cy := (b.T + b.B) / 2
	paintCaptionInk(hdc, captionGlyphInk(kind, zoomed, cx, cy, s), cr, cg, cb, br, bg, bb)
}

func paintCaptionInk(hdc win.HDC, ink []captionInk, cr, cg, cb, br, bg, bb byte) {
	if hdc == 0 {
		return
	}
	for _, p := range ink {
		if p.a == 0 {
			continue
		}
		setCaptionPixel(hdc, p.x, p.y, win.RGB(
			mixCover(br, cr, p.a),
			mixCover(bg, cg, p.a),
			mixCover(bb, cb, p.a),
		))
	}
}

func (u *winUI) captionButtons(clientW, stripH int32) [3]pixRect {
	dpi := int32(96)
	if u != nil && u.hwnd != 0 {
		dpi = hwndDPI(u.hwnd)
	}
	return winCaptionButtonsDPI(clientW, stripH, dpi)
}

func (u *winUI) setCaptionDown(hwnd win.HWND, down int) {
	if u == nil {
		return
	}
	press := down >= 0
	if press == u.captionPress && down == u.captionDown {
		return
	}
	u.captionDown = down
	u.captionPress = press
	if hwnd != 0 {
		win.InvalidateRect(hwnd, nil, false)
	}
}
