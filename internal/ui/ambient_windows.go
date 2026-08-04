//go:build windows

package ui

import (
	"syscall"
	"time"

	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/applog"
	"github.com/StephenSHorton/suzuri/internal/chrome"
	"github.com/StephenSHorton/suzuri/internal/config"
)

// themeAmbientColors samples current GDI theme roles for underlays.
func themeAmbientColors() ambientColors {
	return ambientColors{
		pr: chrome.PrimR, pg: chrome.PrimG, pb: chrome.PrimB,
		sr: chrome.SoftR, sg: chrome.SoftG, sb: chrome.SoftB,
		mr: chrome.MuteR, mg: chrome.MuteG, mb: chrome.MuteB,
		tr: chrome.TextR, tg: chrome.TextG, tb: chrome.TextB,
	}
}

// shellAmbientOn is true when settings pick any always-on underlay.
func (u *winUI) shellAmbientOn() bool {
	return u != nil && u.cfg.AmbientActive()
}

// shellMatrixOn is true when ambient is classic rain (legacy name).
func (u *winUI) shellMatrixOn() bool {
	return u != nil && u.cfg.ShellAmbient == config.AmbientRain
}

// paintShellAmbient draws the configured always-on underlay under the shell.
func (u *winUI) paintShellAmbient(hdc win.HDC, rect win.RECT, padY, bot int32) {
	if hdc == 0 || bot <= padY || u == nil {
		return
	}
	amb := u.cfg.ShellAmbient
	if amb == "" || amb == config.AmbientNone {
		return
	}
	alt := false
	if t := u.activeTab(); t != nil {
		alt = t.altScreen()
	}
	intensity := effectiveAmbientIntensity(u.cfg, alt)
	if intensity <= 0 {
		return
	}
	switch amb {
	case config.AmbientRain:
		u.paintDimMatrixIntensity(hdc, rect, padY, bot, matrixLoop, u.blinkStart, 0, intensity)
	case config.AmbientCRT:
		u.paintCRTAmbient(hdc, rect, padY, bot, intensity)
	default:
		u.paintAmbientGlyphs(hdc, rect, padY, bot, amb, intensity)
	}
}

func (u *winUI) paintAmbientGlyphs(hdc win.HDC, rect win.RECT, padY, bot int32, kind string, intensity float64) {
	defer applog.Recover("paintAmbientGlyphs", false)
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	rows := int((bot - padY + ch - 1) / ch)
	cols := int((rect.Right + cw - 1) / cw)
	if rows < 1 || cols < 1 {
		return
	}
	t0 := u.blinkStart
	if t0.IsZero() {
		t0 = time.Now()
	}
	cells := ambientGlyphCells(kind, cols, rows, t0, time.Now(), themeAmbientColors(), intensity)
	if len(cells) == 0 {
		return
	}
	font := u.font
	if font == 0 {
		return
	}
	saved := win.SaveDC(hdc)
	if saved == 0 {
		return
	}
	defer win.RestoreDC(hdc, saved)
	_ = win.IntersectClipRect(hdc, 0, padY, rect.Right, bot)
	oldF := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, oldF)
	win.SetBkMode(hdc, win.TRANSPARENT)
	for _, c := range cells {
		if c.Ch == 0 {
			continue
		}
		s, err := syscall.UTF16FromString(string(c.Ch))
		if err != nil || len(s) < 2 {
			continue
		}
		win.SetTextColor(hdc, win.RGB(c.FR, c.FG, c.FB))
		win.TextOut(hdc, int32(c.X)*cw, padY+int32(c.Y)*ch, &s[0], int32(len(s)-1))
	}
}

// paintCRTAmbient draws scanlines + soft side vignette (pixel level).
func (u *winUI) paintCRTAmbient(hdc win.HDC, rect win.RECT, padY, bot int32, intensity float64) {
	defer applog.Recover("paintCRTAmbient", false)
	if intensity <= 0 {
		return
	}
	// Scanlines every 2px — dim toward void.
	lineA := intensity * 0.22
	if lineA > 0.35 {
		lineA = 0.35
	}
	lr := byte(float64(chrome.VoidR) * (1 - lineA))
	lg := byte(float64(chrome.VoidG) * (1 - lineA))
	lb := byte(float64(chrome.VoidB) * (1 - lineA))
	// Slight primary tint on scanlines.
	lr = blendB(lr, chrome.PrimR, lineA*0.25)
	lg = blendB(lg, chrome.PrimG, lineA*0.25)
	lb = blendB(lb, chrome.PrimB, lineA*0.25)
	br := createSolidBrushRGB(win.RGB(lr, lg, lb))
	if br != 0 {
		for y := padY; y < bot; y += 2 {
			fillRect(hdc, win.RECT{Left: 0, Top: y, Right: rect.Right, Bottom: y + 1}, br)
		}
		win.DeleteObject(win.HGDIOBJ(br))
	}
	// Soft left/right vignette strips.
	vigW := rect.Right / 18
	if vigW < 8 {
		vigW = 8
	}
	if vigW > 48 {
		vigW = 48
	}
	va := intensity * 0.35
	vr := byte(float64(chrome.VoidR) * (1 - va*0.5))
	vg := byte(float64(chrome.VoidG) * (1 - va*0.5))
	vb := byte(float64(chrome.VoidB) * (1 - va*0.5))
	if vbr := createSolidBrushRGB(win.RGB(vr, vg, vb)); vbr != 0 {
		fillRect(hdc, win.RECT{Left: 0, Top: padY, Right: vigW, Bottom: bot}, vbr)
		fillRect(hdc, win.RECT{Left: rect.Right - vigW, Top: padY, Right: rect.Right, Bottom: bot}, vbr)
		win.DeleteObject(win.HGDIOBJ(vbr))
	}
}

// paintInkWashIntro draws expanding ink blot curtain.
func (u *winUI) paintInkWashIntro(hdc win.HDC, rect win.RECT) {
	defer applog.Recover("paintInkWashIntro", false)
	padY := u.shellPadY()
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	if bot <= padY {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	rows := int((bot - padY + ch - 1) / ch)
	cols := int((rect.Right + cw - 1) / cw)
	t0 := u.matrixIntroStart
	cells := inkWashCells(cols, rows, t0, matrixIntroSpawn, time.Now(), themeAmbientColors())
	if len(cells) == 0 {
		if time.Since(t0) > matrixIntroSpawn {
			u.finishMatrixIntro()
		}
		return
	}
	u.paintRainCellList(hdc, rect, padY, bot, cells)
}

// paintCRTIntro draws scanline boot curtain.
func (u *winUI) paintCRTIntro(hdc win.HDC, rect win.RECT) {
	defer applog.Recover("paintCRTIntro", false)
	padY := u.shellPadY()
	bot := u.shellBottomY(rect.Bottom - rect.Top)
	if bot <= padY {
		return
	}
	// Pixel scanlines full intensity during intro.
	t := time.Since(u.matrixIntroStart).Seconds()
	flash := 0.55
	if t < 0.3 {
		flash = 0.85
	}
	if t > matrixIntroSpawn.Seconds() {
		u.finishMatrixIntro()
		return
	}
	fade := 1.0
	sp := matrixIntroSpawn.Seconds()
	if t > sp*0.55 {
		fade = 1 - (t-sp*0.55)/(sp*0.55)
		if fade < 0 {
			u.finishMatrixIntro()
			return
		}
	}
	u.paintCRTAmbient(hdc, rect, padY, bot, flash*fade)
	// Plus glyph scan rows for extra phosphor.
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	rows := int((bot - padY + ch - 1) / ch)
	cols := int((rect.Right + cw - 1) / cw)
	cells := crtIntroCells(cols, rows, u.matrixIntroStart, matrixIntroSpawn, time.Now(), themeAmbientColors())
	u.paintRainCellList(hdc, rect, padY, bot, cells)
}

func (u *winUI) paintRainCellList(hdc win.HDC, rect win.RECT, padY, bot int32, cells []rainCell) {
	if len(cells) == 0 || hdc == 0 {
		return
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	font := u.font
	if u.cjkFont != 0 {
		// Prefer CJK for block/shade glyphs when available.
		font = u.cjkFont
	}
	if font == 0 {
		return
	}
	saved := win.SaveDC(hdc)
	if saved == 0 {
		return
	}
	defer win.RestoreDC(hdc, saved)
	_ = win.IntersectClipRect(hdc, 0, padY, rect.Right, bot)
	oldF := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, oldF)
	win.SetBkMode(hdc, win.TRANSPARENT)
	for _, c := range cells {
		if c.Ch == 0 {
			continue
		}
		s, err := syscall.UTF16FromString(string(c.Ch))
		if err != nil || len(s) < 2 {
			continue
		}
		win.SetTextColor(hdc, win.RGB(c.FR, c.FG, c.FB))
		win.TextOut(hdc, int32(c.X)*cw, padY+int32(c.Y)*ch, &s[0], int32(len(s)-1))
	}
}
