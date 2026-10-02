//go:build windows

package ui

import (
	"syscall"
	"unsafe"

	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

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
	if u == nil || hdc == 0 || stripH < 4 {
		return
	}
	u.ensureTitleFonts(stripH)
	if u.titleCupFont == 0 {
		// Cell glyph stays as the visible cup when the symbol face has no ☕.
		return
	}
	b := u.chrome.CaffeineBounds()
	if b[1] <= b[0] {
		return
	}
	cw := u.metricW
	if cw < 1 {
		cw = cellW
	}
	x0 := 4 + int32(b[0])*cw
	bw := int32(b[1]-b[0]) * cw
	if bw < 4 {
		return
	}
	// Cover the lipgloss cup so only the strip-sized glyph shows.
	paintOpaqueRGB(hdc, win.RECT{Left: x0, Top: 0, Right: x0 + bw, Bottom: stripH}, chrome.BarR, chrome.BarG, chrome.BarB)
	fr, fg, fb := chrome.SoftR, chrome.SoftG, chrome.SoftB
	if u.caffeine != nil && u.caffeine.Active() {
		fr, fg, fb = chrome.PrimR, chrome.PrimG, chrome.PrimB
	}
	rc := win.RECT{Left: x0, Top: 2, Right: x0 + bw, Bottom: stripH - 2}
	drawCenteredRune(hdc, u.titleCupFont, rc, '☕', fr, fg, fb)
}

func (u *winUI) paintWinCaption(hdc win.HDC, rect win.RECT) {
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
	btns := winCaptionButtons(clientW, strip)
	zoomed := u.hwnd != 0 && win.IsZoomed(u.hwnd)
	for i, b := range btns {
		if b.empty() {
			continue
		}
		br, bg, bb := chrome.BarR, chrome.BarG, chrome.BarB
		gr, gg, gb := chrome.TextR, chrome.TextG, chrome.TextB
		if i == u.captionHot {
			if i == 2 {
				br, bg, bb = mixRGB(br, bg, bb, 196, 48, 43, 3, 4)
				gr, gg, gb = 255, 255, 255
			} else {
				br, bg, bb = mixRGB(br, bg, bb, 255, 255, 255, 1, 5)
			}
		}
		wr := win.RECT{Left: b.L, Top: b.T, Right: b.R, Bottom: b.B}
		paintOpaqueRGB(hdc, wr, br, bg, bb)
		paintCaptionGlyph(hdc, i, b, zoomed, gr, gg, gb, br, bg, bb)
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
	if u.captionLeaveTrk || hwnd == 0 {
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

// paintCaptionGlyph draws minimize / maximize-or-restore / close, centered.
// kind is the Windows visual order: 0 min, 1 zoom, 2 close.
func paintCaptionGlyph(hdc win.HDC, kind int, b pixRect, zoomed bool, cr, cg, cb, br, bg, bb byte) {
	h := b.B - b.T
	bw := b.R - b.L
	s := h * 2 / 5
	if s < 10 {
		s = 10
	}
	if s > h-6 {
		s = h - 6
	}
	if bw > 8 && s > bw-8 {
		s = bw - 8
	}
	if s < 6 {
		s = 6
	}
	cx := (b.L + b.R) / 2
	cy := (b.T + b.B) / 2
	stroke := s / 8
	if stroke < 1 {
		stroke = 1
	}
	switch kind {
	case 0:
		paintOpaqueRects(hdc, []win.RECT{{
			Left: cx - s/2, Top: cy - stroke/2, Right: cx + s/2, Bottom: cy - stroke/2 + stroke,
		}}, cr, cg, cb)
	case 1:
		if zoomed {
			back := win.RECT{Left: cx - s/5, Top: cy - s/2, Right: cx + s/2, Bottom: cy + s/5}
			front := win.RECT{Left: cx - s/2, Top: cy - s/5, Right: cx + s/5, Bottom: cy + s/2}
			paintOpaqueRects(hdc, outlineRects(back, stroke), cr, cg, cb)
			paintOpaqueRGB(hdc, front, br, bg, bb)
			paintOpaqueRects(hdc, outlineRects(front, stroke), cr, cg, cb)
			return
		}
		sq := win.RECT{Left: cx - s/2, Top: cy - s/2, Right: cx + s/2, Bottom: cy + s/2}
		paintOpaqueRects(hdc, outlineRects(sq, stroke), cr, cg, cb)
	default:
		paintOpaqueRects(hdc, crossRects(cx-s/2, cy-s/2, s, stroke), cr, cg, cb)
	}
}

func outlineRects(r win.RECT, t int32) []win.RECT {
	if t < 1 {
		t = 1
	}
	if r.Right-r.Left <= t*2 || r.Bottom-r.Top <= t*2 {
		return []win.RECT{r}
	}
	return []win.RECT{
		{Left: r.Left, Top: r.Top, Right: r.Right, Bottom: r.Top + t},
		{Left: r.Left, Top: r.Bottom - t, Right: r.Right, Bottom: r.Bottom},
		{Left: r.Left, Top: r.Top, Right: r.Left + t, Bottom: r.Bottom},
		{Left: r.Right - t, Top: r.Top, Right: r.Right, Bottom: r.Bottom},
	}
}

func crossRects(x, y, s, t int32) []win.RECT {
	if t < 2 {
		t = 2
	}
	var out []win.RECT
	for i := int32(0); i < s; i += t / 2 {
		out = append(out,
			win.RECT{Left: x + i, Top: y + i, Right: x + i + t, Bottom: y + i + t},
			win.RECT{Left: x + s - t - i, Top: y + i, Right: x + s - i, Bottom: y + i + t},
		)
	}
	return out
}
