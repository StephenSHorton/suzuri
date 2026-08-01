//go:build windows

package ui

import (
	"syscall"

	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// Brand watermark glyph — large dim 硯 in the shell center (tab-strip brand stays).
const shellWatermarkRune = '硯'

// paintShellWatermark draws a large, faint brand mark centered in the shell
// band (under cell glyphs). Hidden on alt-screen so full-screen apps stay clean.
//
// Style matches the tab-strip 硯: same CJK face at cell metrics, then nearest-
// neighbor scaled up so it stays chunky/mono like the chrome brand — not a
// smooth large display face.
func (u *winUI) paintShellWatermark(hdc win.HDC, rect win.RECT, padY, shellBot int32) {
	if hdc == 0 || shellBot <= padY {
		return
	}
	tab := u.activeTab()
	if tab != nil && tab.altScreen() {
		return
	}

	// Intro: stay invisible until rain clears, then ease in (no hard pop).
	fade := u.watermarkFade()
	if fade <= 0.01 {
		return
	}

	shellH := shellBot - padY
	shellW := rect.Right - rect.Left
	if shellW < 80 || shellH < 60 {
		return
	}

	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	// Fullwidth CJK cell — same footprint as the strip brand glyph.
	srcW := cw * 2
	srcH := ch
	if srcW < 2 || srcH < 2 {
		return
	}

	// Same face used for top-left 硯 (CJK fallback), not a smooth UI display font.
	font := u.cjkFont
	if font == 0 {
		font = u.fontBold
	}
	if font == 0 {
		font = u.font
	}
	if font == 0 {
		return
	}

	s, err := syscall.UTF16FromString(string(shellWatermarkRune))
	if err != nil || len(s) < 2 {
		return
	}

	// Render one cell-sized glyph into an offscreen bitmap, then StretchBlt
	// without smoothing so it keeps the terminal / Gohu-adjacent look.
	memDC := win.CreateCompatibleDC(hdc)
	if memDC == 0 {
		return
	}
	defer win.DeleteDC(memDC)
	bmp := win.CreateCompatibleBitmap(hdc, srcW, srcH)
	if bmp == 0 {
		return
	}
	defer win.DeleteObject(win.HGDIOBJ(bmp))
	oldBmp := win.SelectObject(memDC, win.HGDIOBJ(bmp))
	if oldBmp == 0 {
		return
	}
	defer win.SelectObject(memDC, oldBmp)

	// Same pure black as blitGrid (BLACK_BRUSH) so the scaled glyph has no
	// grey “card” behind it — only the 硯 ink should show.
	fillRect(memDC, win.RECT{Left: 0, Top: 0, Right: srcW, Bottom: srcH},
		win.HBRUSH(win.GetStockObject(win.BLACK_BRUSH)))

	oldF := win.SelectObject(memDC, win.HGDIOBJ(font))
	// Theme-tinted whisper, scaled by fade-in after matrix intro.
	fr, fg, fb := blendRGB(0, 0, 0, chrome.PrimR, chrome.PrimG, chrome.PrimB, 0.055)
	fr, fg, fb = blendRGB(fr, fg, fb, chrome.SoftR, chrome.SoftG, chrome.SoftB, 0.04)
	fr, fg, fb = blendRGB(0, 0, 0, fr, fg, fb, fade)
	win.SetBkMode(memDC, win.TRANSPARENT)
	win.SetTextColor(memDC, win.RGB(fr, fg, fb))
	win.TextOut(memDC, 0, 0, &s[0], int32(len(s)-1))
	win.SelectObject(memDC, oldF)

	// Destination size: ~40% of shorter shell axis, keep glyph aspect (2:1 cell).
	side := shellH
	if shellW < side {
		side = shellW
	}
	destH := side * 40 / 100
	if destH < ch*6 {
		destH = ch * 6
	}
	if destH > 200 {
		destH = 200
	}
	destW := destH * srcW / srcH
	if destW > shellW*85/100 {
		destW = shellW * 85 / 100
		destH = destW * srcH / srcW
	}
	if destW < 1 || destH < 1 {
		return
	}

	dx := rect.Left + (shellW-destW)/2
	dy := padY + (shellH-destH)/2
	if dx < rect.Left {
		dx = rect.Left
	}
	if dy < padY {
		dy = padY
	}

	// COLORONCOLOR / STRETCH_DELETESCANS = nearest neighbor (no smooth filter).
	oldMode := win.SetStretchBltMode(hdc, win.COLORONCOLOR)
	_ = win.StretchBlt(hdc, dx, dy, destW, destH, memDC, 0, 0, srcW, srcH, win.SRCCOPY)
	if oldMode != 0 {
		win.SetStretchBltMode(hdc, oldMode)
	}
}
