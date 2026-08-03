//go:build windows

package ui

import (
	"math"
	"syscall"
	"time"

	"github.com/lxn/win"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// Half-width katakana + digits/symbols — classic “digital rain” glyph set.
// MS Gothic / Yu Gothic cover these; we fall back to the mono face for ASCII.
var matrixRainRunes = []rune(
	"ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ" +
		"0123456789" +
		":.=*+-<>|",
)

// pre-encoded UTF-16 for TextOut (built once).
var matrixRainGlyphs [][]uint16

func init() {
	matrixRainGlyphs = make([][]uint16, 0, len(matrixRainRunes))
	for _, r := range matrixRainRunes {
		s, err := syscall.UTF16FromString(string(r))
		if err != nil || len(s) < 2 {
			continue
		}
		matrixRainGlyphs = append(matrixRainGlyphs, s)
	}
}

// matrixPaintMode controls whether streams loop (settings) or wind down (intro).
type matrixPaintMode int

const (
	// matrixLoop: continuous wrap — used under Settings forever.
	matrixLoop matrixPaintMode = iota
	// matrixSpawn: looping rain during intro spawn window.
	matrixSpawn
	// matrixWindDown: no new wraps — heads keep falling until off-screen.
	matrixWindDown
)

// paintDimMatrix draws themed digital rain into [top,bot).
// Returns true if any glyph was painted (used to end intro wind-down).
//
// t0 is the clock origin for motion (blinkStart for settings, intro start for boot).
// spawnFor is how long streams may wrap before wind-down (intro only).
// intensity scales glyph brightness (1 = full intro/settings, ~0.2 = shell backdrop).
func (u *winUI) paintDimMatrix(hdc win.HDC, rect win.RECT, top, bot int32, mode matrixPaintMode, t0 time.Time, spawnFor time.Duration) (drew bool) {
	return u.paintDimMatrixIntensity(hdc, rect, top, bot, mode, t0, spawnFor, 1)
}

func (u *winUI) paintDimMatrixIntensity(hdc win.HDC, rect win.RECT, top, bot int32, mode matrixPaintMode, t0 time.Time, spawnFor time.Duration, intensity float64) (drew bool) {
	if hdc == 0 || bot <= top || len(matrixRainGlyphs) == 0 {
		return false
	}
	if intensity <= 0 {
		return false
	}
	if intensity > 1 {
		intensity = 1
	}
	if t0.IsZero() {
		t0 = u.blinkStart
	}
	cw, ch := u.metricW, u.metricH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	// Half-width glyphs → one mono cell per column.
	stepX := cw
	if stepX < 1 {
		stepX = 1
	}
	rows := int((bot - top + ch - 1) / ch)
	if rows < 1 {
		return false
	}
	cols := int((rect.Right + stepX - 1) / stepX)
	if cols < 1 {
		return false
	}

	font := u.cjkFont
	if font == 0 {
		font = u.font
	}
	if font == 0 {
		return false
	}

	saved := win.SaveDC(hdc)
	if saved == 0 {
		return false
	}
	defer win.RestoreDC(hdc, saved)
	_ = win.IntersectClipRect(hdc, 0, top, rect.Right, bot)

	oldF := win.SelectObject(hdc, win.HGDIOBJ(font))
	defer win.SelectObject(hdc, oldF)
	win.SetBkMode(hdc, win.TRANSPARENT)

	// Theme roles (updated by ApplyTheme / settings preview).
	hr, hg, hb := chrome.TextR, chrome.TextG, chrome.TextB // head — bright fg
	pr, pg, pb := chrome.PrimR, chrome.PrimG, chrome.PrimB // body — accent
	dr, dg, db := chrome.DimR, chrome.DimG, chrome.DimB   // fade into matte
	sr, sg, sb := chrome.SoftR, chrome.SoftG, chrome.SoftB

	t := time.Since(t0).Seconds()
	if t < 0 {
		t = 0
	}
	spawnT := spawnFor.Seconds()
	if spawnT < 0 {
		spawnT = 0
	}
	nGlyphs := len(matrixRainGlyphs)
	// Trail length scales a bit with height but stays cheap to paint.
	trail := rows/2 + 6
	if trail > 18 {
		trail = 18
	}
	if trail < 8 {
		trail = 8
	}

	for col := 0; col < cols; col++ {
		seed := uint32(col)*0x9E3779B9 ^ 0xA5A5A5A5
		// Settings overlay: full speed range including slow streams.
		speed := 0.22 + float64(seed%9)*0.08
		if mode == matrixSpawn || mode == matrixWindDown {
			// Intro: no slow tail — streams must clear soon after spawn ends.
			// Floor ~0.50 drops the lowest ~third of the settings distribution.
			speed = 0.50 + float64(seed%7)*0.10 // ~0.50–1.10
		}
		phase := float64(seed%1000) / 17.0
		period := float64(rows + trail + 4)
		rate := speed * float64(rows) / 4.2 // head cells / second

		var head int
		switch mode {
		case matrixWindDown:
			// Position as of spawn end (still looping), then fall without wrap.
			headAtStop := math.Mod(spawnT*rate+phase, period)
			// Slightly faster after spawn so the curtain clears promptly.
			head = int(headAtStop + (t-spawnT)*rate*1.45)
		default: // matrixLoop, matrixSpawn
			headF := math.Mod(t*rate+phase, period)
			head = int(headF)
		}

		x := int32(col) * stepX
		for i := 0; i < trail; i++ {
			yCell := head - i
			if yCell < 0 || yCell >= rows {
				continue
			}
			var fr, fg, fb byte
			switch {
			case i == 0:
				fr, fg, fb = hr, hg, hb
			case i == 1:
				fr, fg, fb = pr, pg, pb
			case i < 4:
				a := float64(i-1) / 3.0
				fr, fg, fb = blendRGB(pr, pg, pb, sr, sg, sb, a)
			default:
				fade := 1.0 - float64(i)/float64(trail)
				fade = fade * fade
				fr, fg, fb = blendRGB(dr, dg, db, sr, sg, sb, fade)
				if int(fr)+int(fg)+int(fb) < 48 {
					continue
				}
			}
			if intensity < 1 {
				fr, fg, fb = blendRGB(0, 0, 0, fr, fg, fb, intensity)
				// Drop near-black tail cells so shell rain stays a whisper.
				if int(fr)+int(fg)+int(fb) < 18 {
					continue
				}
			}
			gi := int(seed>>3) + yCell*3 + int(t*12) + i*7
			if gi < 0 {
				gi = -gi
			}
			s := matrixRainGlyphs[gi%nGlyphs]
			win.SetTextColor(hdc, win.RGB(fr, fg, fb))
			py := top + int32(yCell)*ch
			win.TextOut(hdc, x, py, &s[0], int32(len(s)-1))
			drew = true
		}
	}
	return drew
}

// shellMatrixIntensity is how bright persistent shell rain is vs settings/intro.
const shellMatrixIntensity = 0.20

// shellMatrixAltScreenIntensity — see intro_darwin.go (same rationale).
const shellMatrixAltScreenIntensity = 0.48
