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
		// CRT is drawn AFTER the cell grid (see paint path) so it is not
		// covered by default-black cell backgrounds.
	default:
		u.paintAmbientGlyphs(hdc, rect, padY, bot, amb, intensity)
	}
}

// paintShellAmbientOver draws effects that must sit above the cell grid (CRT).
func (u *winUI) paintShellAmbientOver(hdc win.HDC, rect win.RECT, padY, bot int32) {
	if u == nil || !u.shellAmbientOn() || u.cfg.ShellAmbient != config.AmbientCRT {
		return
	}
	alt := false
	if t := u.activeTab(); t != nil {
		alt = t.altScreen()
	}
	u.paintCRTAmbient(hdc, rect, padY, bot, effectiveAmbientIntensity(u.cfg, alt))
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

// paintCRTAmbient draws visible scanlines + side vignette over the shell field.
// Painted AFTER the cell grid so default-black cells do not hide the effect.
func (u *winUI) paintCRTAmbient(hdc win.HDC, rect win.RECT, padY, bot int32, intensity float64) {
	defer applog.Recover("paintCRTAmbient", false)
	if intensity <= 0 || bot <= padY {
		return
	}
	// Stronger than the first pass: dark phosphor lines every other pixel row,
	// tinted with theme primary so they read on inkstone/high-contrast voids.
	lineA := 0.28 + intensity*0.35 // 0.28–0.63
	if lineA > 0.7 {
		lineA = 0.7
	}
	// Blend black toward primary for a "lit tube" look.
	lr := blendB(0, chrome.PrimR, 0.15+intensity*0.2)
	lg := blendB(0, chrome.PrimG, 0.15+intensity*0.2)
	lb := blendB(0, chrome.PrimB, 0.15+intensity*0.2)
	// Alpha via dither: draw full-opacity lines but dim color by lineA.
	lr = scaleB(lr, lineA)
	lg = scaleB(lg, lineA)
	lb = scaleB(lb, lineA)
	br := createSolidBrushRGB(win.RGB(lr, lg, lb))
	if br != 0 {
		// Every 3px: one lit line + gap (classic CRT density).
		for y := padY; y < bot; y += 3 {
			fillRect(hdc, win.RECT{Left: rect.Left, Top: y, Right: rect.Right, Bottom: y + 1}, br)
		}
		win.DeleteObject(win.HGDIOBJ(br))
	}
	// Rolling bright band (slow) so motion is obvious when ambient is CRT.
	t0 := u.blinkStart
	if t0.IsZero() {
		t0 = time.Now()
	}
	bandY := padY + int32(mathMod(time.Since(t0).Seconds()*18, float64(bot-padY+1)))
	bandH := int32(3)
	if bandY+bandH > bot {
		bandH = bot - bandY
	}
	if bandH > 0 {
		br2 := createSolidBrushRGB(win.RGB(
			scaleB(chrome.PrimR, 0.25+intensity*0.25),
			scaleB(chrome.PrimG, 0.25+intensity*0.25),
			scaleB(chrome.PrimB, 0.25+intensity*0.25),
		))
		if br2 != 0 {
			fillRect(hdc, win.RECT{Left: rect.Left, Top: bandY, Right: rect.Right, Bottom: bandY + bandH}, br2)
			win.DeleteObject(win.HGDIOBJ(br2))
		}
	}
	// Side vignette (darker edges).
	vigW := rect.Right / 14
	if vigW < 12 {
		vigW = 12
	}
	if vigW > 64 {
		vigW = 64
	}
	va := 0.25 + intensity*0.35
	vr := scaleB(0, va)
	vg := scaleB(0, va)
	vb := scaleB(0, va)
	// Darken by painting near-black strips (visible over shell).
	if vbr := createSolidBrushRGB(win.RGB(vr, vg, vb)); vbr != 0 {
		// Soft stepped vignette.
		for i := int32(0); i < vigW; i += 2 {
			// Outer steps darker.
			a := va * (1 - float64(i)/float64(vigW))
			c := scaleB(20, a)
			if b := createSolidBrushRGB(win.RGB(c, c, c)); b != 0 {
				fillRect(hdc, win.RECT{Left: rect.Left + i, Top: padY, Right: rect.Left + i + 2, Bottom: bot}, b)
				fillRect(hdc, win.RECT{Left: rect.Right - i - 2, Top: padY, Right: rect.Right - i, Bottom: bot}, b)
				win.DeleteObject(win.HGDIOBJ(b))
			}
		}
		win.DeleteObject(win.HGDIOBJ(vbr))
	}
}

func mathMod(a, b float64) float64 {
	if b <= 0 {
		return 0
	}
	v := a - b*float64(int(a/b))
	if v < 0 {
		v += b
	}
	return v
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
