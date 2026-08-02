//go:build darwin

package ui

import (
	"image"
	"math"
	"time"

	"golang.org/x/image/math/fixed"

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
// Callers must paint this UNDER shell cell glyphs (only ink is written).
//
// Shape must read as one solid symbol: anti-aliased low-alpha edges looked
// "fragmented in dimness". We binarize the glyph mask, nearest-neighbor scale
// (chunky mono, like Windows StretchBlt), then stamp one uniform quiet color.
func (p *softwarePainter) paintShellWatermark(dst *image.RGBA, padY, shellBot int, fade float64) {
	if p == nil || dst == nil || fade <= 0.01 {
		return
	}
	if !cjkHasRune(shellWatermarkRune) || p.cjkFace == nil {
		return
	}
	w := dst.Bounds().Dx()
	shellH := shellBot - padY
	shellW := w
	if shellW < 80 || shellH < 60 {
		return
	}

	// Quiet but even ink — one solid shade (not brighter; just coherent).
	// ~8–12% of primary so strokes read as a shape on black.
	inkA := 0.10 * fade
	if inkA < 0.03 {
		return
	}
	fr := blendByte(0, chrome.PrimR, inkA)
	fg := blendByte(0, chrome.PrimG, inkA)
	fb := blendByte(0, chrome.PrimB, inkA)
	// Floor so the mark never dissolves into single sparse pixels.
	if fr < 10 && chrome.PrimR > 0 {
		fr = 10
	}
	if fg < 10 && chrome.PrimG > 0 {
		fg = 10
	}
	if fb < 10 && chrome.PrimB > 0 {
		fb = 10
	}

	// Rasterize at a moderate size for clean topology, then NN-scale up.
	// Cell-sized source was too small — AA crumbs scaled into static.
	const srcPx = 64
	face := cjkFaceForSize(float64(srcPx) * 0.72)
	if face == nil {
		return
	}
	defer func() { _ = face.Close() }()
	m := face.Metrics()
	ascent := m.Ascent.Round()
	// Baseline near optical middle of the square (not top-heavy).
	dr, mask, maskp, _, ok := face.Glyph(fixed.P(srcPx/6, ascent+(srcPx-ascent)/3), shellWatermarkRune)
	if !ok || mask == nil {
		return
	}
	// Hard-threshold mask → solid 1-bit bitmap (no AA haze).
	bin := make([]bool, srcPx*srcPx)
	hasInk := false
	minX, minY := srcPx, srcPx
	maxX, maxY := -1, -1
	for y := dr.Min.Y; y < dr.Max.Y; y++ {
		for x := dr.Min.X; x < dr.Max.X; x++ {
			if x < 0 || y < 0 || x >= srcPx || y >= srcPx {
				continue
			}
			_, _, _, a := mask.At(maskp.X+(x-dr.Min.X), maskp.Y+(y-dr.Min.Y)).RGBA()
			// Mid threshold: keep body strokes, drop fringe dust.
			if a >= 0x8000 {
				bin[y*srcPx+x] = true
				hasInk = true
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}
	if !hasInk || maxX < minX || maxY < minY {
		return
	}
	// 1px close: fill single-pixel holes so strokes stay connected.
	for y := 1; y < srcPx-1; y++ {
		for x := 1; x < srcPx-1; x++ {
			i := y*srcPx + x
			if bin[i] {
				continue
			}
			n := 0
			if bin[i-1] {
				n++
			}
			if bin[i+1] {
				n++
			}
			if bin[i-srcPx] {
				n++
			}
			if bin[i+srcPx] {
				n++
			}
			if n >= 3 {
				bin[i] = true
				if x < minX {
					minX = x
				}
				if y < minY {
					minY = y
				}
				if x > maxX {
					maxX = x
				}
				if y > maxY {
					maxY = y
				}
			}
		}
	}

	// Crop to ink bounds so empty bitmap padding doesn't shift the visual center.
	inkW := maxX - minX + 1
	inkH := maxY - minY + 1
	if inkW < 2 || inkH < 2 {
		return
	}

	side := shellH
	if shellW < side {
		side = shellW
	}
	// Fit ink box into ~40% of the shorter shell axis (preserve aspect).
	destSide := side * 40 / 100
	if destSide < p.cellH*7 {
		destSide = p.cellH * 7
	}
	if destSide > 220 {
		destSide = 220
	}
	var destW, destH int
	if inkW >= inkH {
		destW = destSide
		destH = destSide * inkH / inkW
	} else {
		destH = destSide
		destW = destSide * inkW / inkH
	}
	if destW > shellW*80/100 {
		destW = shellW * 80 / 100
		destH = destW * inkH / inkW
	}
	if destW < 1 || destH < 1 {
		return
	}

	// Geometric center of the shell band, then a small optical lift —
	// CJK squares read low when purely geometric; ~6% of mark height up.
	dx := (shellW - destW) / 2
	dy := padY + (shellH-destH)/2 - destH/12
	if dy < padY {
		dy = padY
	}
	if dy+destH > shellBot {
		dy = shellBot - destH
	}

	// Nearest-neighbor from cropped ink bounds only.
	for y := 0; y < destH; y++ {
		sy := minY + y*inkH/destH
		if sy > maxY {
			sy = maxY
		}
		for x := 0; x < destW; x++ {
			sx := minX + x*inkW/destW
			if sx > maxX {
				sx = maxX
			}
			if !bin[sy*srcPx+sx] {
				continue
			}
			setRGB(dst, dx+x, dy+y, fr, fg, fb)
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
