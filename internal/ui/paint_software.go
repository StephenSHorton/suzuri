//go:build darwin

package ui

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// softwarePainter rasterizes terminal cell grids into an RGBA buffer.
type softwarePainter struct {
	face   font.Face
	cellW  int
	cellH  int
	ascent int
}

func newSoftwarePainter(sizePx int) *softwarePainter {
	face := faceForSize(float64(sizePx))
	if face == nil {
		return &softwarePainter{cellW: cellW, cellH: cellH, ascent: cellH - 4}
	}
	m := face.Metrics()
	// Fixed cell pitch: use advance of 'M' when available, else size estimate.
	adv, ok := face.GlyphAdvance('M')
	cw := sizePx*3/5 + 1
	if ok && adv > 0 {
		cw = adv.Round()
	}
	if cw < 6 {
		cw = 6
	}
	ch := m.Height.Round()
	if ch < sizePx {
		ch = sizePx + 2
	}
	ascent := m.Ascent.Round()
	if ascent < 1 {
		ascent = ch - 4
	}
	return &softwarePainter{face: face, cellW: cw, cellH: ch, ascent: ascent}
}

func (p *softwarePainter) metrics() (cw, ch int) {
	if p == nil {
		return cellW, cellH
	}
	return p.cellW, p.cellH
}

func (p *softwarePainter) close() {
	if p != nil && p.face != nil {
		_ = p.face.Close()
		p.face = nil
	}
}

// paintFrame draws shell grid + chrome strips + input bar into dst.
func (p *softwarePainter) paintFrame(
	dst *image.RGBA,
	shell [][]cellPix,
	chromeCells [][]cellPix,
	overlay [][]cellPix,
	inputLines [][]cellPix,
	padY, shellBot int,
	curX, curY int,
	curVis bool,
	curAlpha float64,
	dimShell bool,
) {
	if dst == nil || p == nil {
		return
	}
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	// Void background
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.RGBA{R: chrome.VoidR, G: chrome.VoidG, B: chrome.VoidB, A: 255}}, image.Point{}, draw.Src)

	cw, ch := p.cellW, p.cellH
	const padX = 4

	// Shell grid
	for y, row := range shell {
		py := padY + y*ch
		if py+ch > shellBot {
			break
		}
		for x, cell := range row {
			px := padX + x*cw
			if px >= w {
				break
			}
			// Selection / cursor handled by caller via cell colors.
			br, bg, bb := cell.BR, cell.BG, cell.BB
			if curVis && x == curX && y == curY {
				// Soft cursor blend toward fg.
				a := curAlpha
				if a < 0.15 {
					a = 0.15
				}
				if a > 1 {
					a = 1
				}
				br = blendByte(br, cell.FR, a)
				bg = blendByte(bg, cell.FG, a)
				bb = blendByte(bb, cell.FB, a)
			}
			fillRectRGBA(dst, px, py, cw, ch, br, bg, bb)
			if cell.Ch != 0 && cell.Ch != ' ' {
				p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
			}
		}
	}

	if dimShell {
		// Darken shell under overlay.
		for y := padY; y < shellBot && y < h; y++ {
			for x := 0; x < w; x++ {
				i := dst.PixOffset(x, y)
				dst.Pix[i+0] = dst.Pix[i+0] / 3
				dst.Pix[i+1] = dst.Pix[i+1] / 3
				dst.Pix[i+2] = dst.Pix[i+2] / 3
			}
		}
	}

	// Chrome strip at top
	paintCellStrip(p, dst, chromeCells, 0, 0, padX)

	// Input bar at bottom of shell
	if len(inputLines) > 0 {
		// Hairline
		hairY := shellBot
		if hairY < h {
			for x := 0; x < w; x++ {
				setRGB(dst, x, hairY, 40, 44, 52)
			}
		}
		iy := shellBot + 1
		paintCellStrip(p, dst, inputLines, padX, iy, 0)
	}

	// Overlay card (centered-ish over shell)
	if len(overlay) > 0 {
		oh := len(overlay) * ch
		ow := 0
		for _, row := range overlay {
			if len(row) > ow {
				ow = len(row)
			}
		}
		ow *= cw
		ox := (w - ow) / 2
		if ox < padX {
			ox = padX
		}
		oy := padY + 8
		if oy+oh > shellBot {
			oy = padY
		}
		// Card background matte
		fillRectRGBA(dst, ox-4, oy-4, ow+8, oh+8, 18, 18, 22)
		paintCellStrip(p, dst, overlay, ox, oy, 0)
	}
}

func paintCellStrip(p *softwarePainter, dst *image.RGBA, cells [][]cellPix, ox, oy, _ int) {
	if p == nil || len(cells) == 0 {
		return
	}
	cw, ch := p.cellW, p.cellH
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	for y, row := range cells {
		py := oy + y*ch
		if py >= h {
			break
		}
		for x, cell := range row {
			px := ox + x*cw
			if px >= w {
				break
			}
			// Transparent overlay BG → skip fill
			if isTransparentOverlayBG(cell.BR, cell.BG, cell.BB) {
				if cell.Ch != 0 && cell.Ch != ' ' {
					p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
				}
				continue
			}
			fillRectRGBA(dst, px, py, cw, ch, cell.BR, cell.BG, cell.BB)
			if cell.Ch != 0 && cell.Ch != ' ' {
				p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
			}
		}
	}
}

func (p *softwarePainter) drawGlyph(dst *image.RGBA, px, py int, r rune, fr, fg, fb byte) {
	if p.face == nil {
		return
	}
	dr, mask, maskp, _, ok := p.face.Glyph(fixed.P(px, py+p.ascent), r)
	if !ok {
		return
	}
	col := image.NewUniform(color.RGBA{R: fr, G: fg, B: fb, A: 255})
	draw.DrawMask(dst, dr, col, image.Point{}, mask, maskp, draw.Over)
}

func fillRectRGBA(dst *image.RGBA, x, y, w, h int, r, g, b byte) {
	bounds := dst.Bounds()
	rect := image.Rect(x, y, x+w, y+h).Intersect(bounds)
	if rect.Empty() {
		return
	}
	col := color.RGBA{R: r, G: g, B: b, A: 255}
	for py := rect.Min.Y; py < rect.Max.Y; py++ {
		for px := rect.Min.X; px < rect.Max.X; px++ {
			i := dst.PixOffset(px, py)
			dst.Pix[i+0] = col.R
			dst.Pix[i+1] = col.G
			dst.Pix[i+2] = col.B
			dst.Pix[i+3] = 255
		}
	}
}

func setRGB(dst *image.RGBA, x, y int, r, g, b byte) {
	if !image.Pt(x, y).In(dst.Bounds()) {
		return
	}
	i := dst.PixOffset(x, y)
	dst.Pix[i+0] = r
	dst.Pix[i+1] = g
	dst.Pix[i+2] = b
	dst.Pix[i+3] = 255
}

func blendByte(a, b byte, t float64) byte {
	if t <= 0 {
		return a
	}
	if t >= 1 {
		return b
	}
	return byte(float64(a)*(1-t) + float64(b)*t)
}

// isTransparentOverlayBG is true for cells that should not cover the dim underlay.
func isTransparentOverlayBG(r, g, b byte) bool {
	if r == 0 && g == 0 && b == 0 {
		return true
	}
	if r == chrome.VoidR && g == chrome.VoidG && b == chrome.VoidB {
		return true
	}
	if r == chrome.DimR && g == chrome.DimG && b == chrome.DimB {
		return true
	}
	return false
}


