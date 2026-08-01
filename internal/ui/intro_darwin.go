//go:build darwin

package ui

import (
	"image"
	"math"
	"time"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// matrixRainRunes is the classic digital-rain glyph set (half-width katakana +
// ASCII). Populated in initMatrixRainRunes after the CJK face is registered so
// we only keep runes that have a real glyph index (not .notdef tofu boxes).
var matrixRainRunes []rune

// Preferred rain alphabet — filtered at startup by ttf.Index != 0.
// Half-width first (classic matrix look); fullwidth is the AppleGothic fallback
// when the active CJK face lacks FF61–FF9F.
const matrixRainAlphabet = "ｱｲｳｴｵｶｷｸｹｺｻｼｽｾｿﾀﾁﾂﾃﾄﾅﾆﾇﾈﾉﾊﾋﾌﾍﾎﾏﾐﾑﾒﾓﾔﾕﾖﾗﾘﾙﾚﾛﾜﾝ" +
	"0123456789" +
	":.=*+-<>|" +
	"アイウエオカキクケコサシスセソタチツテトナニヌネノハヒフヘホマミムメモヤユヨラリルレロワン"

// initMatrixRainRunes keeps only glyphs a loaded face can actually paint.
// freetype's GlyphAdvance/Glyph return success for .notdef (index 0) and draw
// hollow boxes — we must filter on Index != 0, not GlyphAdvance.
func initMatrixRainRunes() {
	if len(matrixRainRunes) > 0 {
		return
	}
	var kept []rune
	seen := map[rune]bool{}
	for _, r := range []rune(matrixRainAlphabet) {
		if seen[r] {
			continue
		}
		// CJK / halfwidth / fullwidth: require the CJK face. Never trust Gohu —
		// it maps every missing CJK code point to the same box glyph.
		if isEastAsianRune(r) || isHalfwidthKatakana(r) {
			if !cjkHasRune(r) {
				continue
			}
		} else if !primaryHasRune(r) && !cjkHasRune(r) {
			// ASCII punctuation/digits — primary mono first.
			continue
		}
		seen[r] = true
		kept = append(kept, r)
	}
	if len(kept) == 0 {
		// Last resort — pure ASCII so rain still animates.
		kept = []rune("0123456789:.=*+-<>|")
	}
	matrixRainRunes = kept
}

// matrixPaintMode controls whether streams loop (settings) or wind down (intro).
type matrixPaintMode int

const (
	matrixLoop matrixPaintMode = iota
	matrixSpawn
	matrixWindDown
)

const shellWatermarkRune = '硯'

// rainCell is one painted matrix glyph in cell coordinates.
type rainCell struct {
	X, Y       int
	Ch         rune
	FR, FG, FB byte
}

// matrixRainCells computes a frame of digital rain for the shell band.
func matrixRainCells(cols, rows int, mode matrixPaintMode, t0 time.Time, spawnFor time.Duration, now time.Time) []rainCell {
	initMatrixRainRunes()
	if cols < 1 || rows < 1 || len(matrixRainRunes) == 0 {
		return nil
	}
	if t0.IsZero() {
		t0 = now
	}
	t := now.Sub(t0).Seconds()
	if t < 0 {
		t = 0
	}
	spawnT := spawnFor.Seconds()
	if spawnT < 0 {
		spawnT = 0
	}
	nGlyphs := len(matrixRainRunes)
	trail := rows/2 + 6
	if trail > 18 {
		trail = 18
	}
	if trail < 8 {
		trail = 8
	}

	hr, hg, hb := chrome.TextR, chrome.TextG, chrome.TextB
	pr, pg, pb := chrome.PrimR, chrome.PrimG, chrome.PrimB
	dr, dg, db := chrome.DimR, chrome.DimG, chrome.DimB
	sr, sg, sb := chrome.SoftR, chrome.SoftG, chrome.SoftB

	out := make([]rainCell, 0, cols*trail/2)
	for col := 0; col < cols; col++ {
		seed := uint32(col)*0x9E3779B9 ^ 0xA5A5A5A5
		speed := 0.22 + float64(seed%9)*0.08
		if mode == matrixSpawn || mode == matrixWindDown {
			speed = 0.50 + float64(seed%7)*0.10
		}
		phase := float64(seed%1000) / 17.0
		period := float64(rows + trail + 4)
		rate := speed * float64(rows) / 4.2

		var head int
		switch mode {
		case matrixWindDown:
			headAtStop := math.Mod(spawnT*rate+phase, period)
			head = int(headAtStop + (t-spawnT)*rate*1.45)
		default:
			head = int(math.Mod(t*rate+phase, period))
		}

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
			gi := int(seed>>3) + yCell*3 + int(t*12) + i*7
			if gi < 0 {
				gi = -gi
			}
			out = append(out, rainCell{
				X: col, Y: yCell,
				Ch: matrixRainRunes[gi%nGlyphs],
				FR: fr, FG: fg, FB: fb,
			})
		}
	}
	return out
}

func blendRGB(br, bg, bb, fr, fg, fb byte, a float64) (byte, byte, byte) {
	return blendByte(br, fr, a), blendByte(bg, fg, a), blendByte(bb, fb, a)
}

// paintMatrixRain draws rain cells into the shell band.
func (p *softwarePainter) paintMatrixRain(dst *image.RGBA, padY, shellBot int, cells []rainCell) bool {
	if p == nil || dst == nil || len(cells) == 0 {
		return false
	}
	cw, ch := p.cellW, p.cellH
	drew := false
	for _, c := range cells {
		px := 4 + c.X*cw
		py := padY + c.Y*ch
		if py+ch > shellBot || py < padY {
			continue
		}
		p.drawGlyph(dst, px, py, c.Ch, c.FR, c.FG, c.FB)
		drew = true
	}
	return drew
}

// paintShellWatermark draws a large faint 硯 centered in the shell band.
func (p *softwarePainter) paintShellWatermark(dst *image.RGBA, padY, shellBot int, fade float64) {
	if p == nil || dst == nil || fade <= 0.01 {
		return
	}
	w := dst.Bounds().Dx()
	shellH := shellBot - padY
	shellW := w
	if shellW < 80 || shellH < 60 {
		return
	}
	// Whisper of primary, scaled by fade.
	fr, fg, fb := blendRGB(0, 0, 0, chrome.PrimR, chrome.PrimG, chrome.PrimB, 0.055)
	fr, fg, fb = blendRGB(fr, fg, fb, chrome.SoftR, chrome.SoftG, chrome.SoftB, 0.04)
	fr, fg, fb = blendRGB(0, 0, 0, fr, fg, fb, fade)
	if int(fr)+int(fg)+int(fb) < 8 {
		return
	}

	// Render one fullwidth cell into a small buffer, then nearest-neighbor scale.
	srcW := p.cellW * 2
	srcH := p.cellH
	if srcW < 4 || srcH < 4 {
		return
	}
	src := image.NewRGBA(image.Rect(0, 0, srcW, srcH))
	// Black under glyph so only ink shows when stretched.
	fillRectRGBA(src, 0, 0, srcW, srcH, 0, 0, 0)
	p.drawGlyph(src, 0, 0, shellWatermarkRune, fr, fg, fb)

	side := shellH
	if shellW < side {
		side = shellW
	}
	destH := side * 40 / 100
	if destH < p.cellH*6 {
		destH = p.cellH * 6
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
	dx := (shellW - destW) / 2
	dy := padY + (shellH-destH)/2
	// Nearest-neighbor scale (chunky mono look, not smooth).
	for y := 0; y < destH; y++ {
		sy := y * srcH / destH
		for x := 0; x < destW; x++ {
			sx := x * srcW / destW
			si := src.PixOffset(sx, sy)
			// Skip pure black (underlay).
			if src.Pix[si+0]|src.Pix[si+1]|src.Pix[si+2] == 0 {
				continue
			}
			setRGB(dst, dx+x, dy+y, src.Pix[si+0], src.Pix[si+1], src.Pix[si+2])
		}
	}
}

// paintDimNekoField draws a faint 猫咪 underlay (settings / overlay texture).
func (p *softwarePainter) paintDimNekoField(dst *image.RGBA, padY, shellBot int) {
	if p == nil || dst == nil {
		return
	}
	w := dst.Bounds().Dx()
	cw, ch := p.cellW, p.cellH
	stepX := cw * 2
	if stepX < 2 {
		stepX = 2
	}
	fr := byte((int(chrome.SoftR) + int(chrome.DimR)*11) / 12)
	fg := byte((int(chrome.SoftG) + int(chrome.DimG)*11) / 12)
	fb := byte((int(chrome.SoftB) + int(chrome.DimB)*11) / 12)
	glyphs := []rune{'猫', '咪'}
	col := 0
	for y := padY; y < shellBot; y += ch {
		for x := 0; x < w; x += stepX {
			p.drawGlyph(dst, x, y, glyphs[col%len(glyphs)], fr, fg, fb)
			col++
		}
	}
}

// fillShellMatte fills the shell band with a theme-tinted dark matte.
func fillShellMatte(dst *image.RGBA, padY, shellBot int, withPrimaryWhisper bool) {
	if dst == nil || shellBot <= padY {
		return
	}
	w := dst.Bounds().Dx()
	baseR, baseG, baseB := blendRGB(0, 0, 0, chrome.DimR, chrome.DimG, chrome.DimB, 0.35)
	r, g, b := baseR, baseG, baseB
	if withPrimaryWhisper {
		r, g, b = blendRGB(baseR, baseG, baseB, chrome.PrimR, chrome.PrimG, chrome.PrimB, 0.05)
	}
	fillRectRGBA(dst, 0, padY, w, shellBot-padY, r, g, b)
}
