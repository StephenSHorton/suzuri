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
	face     font.Face
	cjkFace  font.Face
	cellW    int
	cellH    int
	ascent   int
	sizePx   float64
	faceName string
}

// newSoftwarePainter builds a painter for the settings face name + size.
// faceName empty or bundled → embedded Gohu; otherwise a system mono face.
func newSoftwarePainter(faceName string, sizePx int) *softwarePainter {
	if sizePx < 10 {
		sizePx = 14
	}
	if stringsTrimSpace(faceName) == "" {
		faceName = BundledFace
	}
	face := faceForName(faceName, float64(sizePx))
	cjk := cjkFaceForSize(float64(sizePx))
	if face == nil && cjk == nil {
		return &softwarePainter{
			cellW: cellW, cellH: cellH, ascent: cellH - 4,
			sizePx: float64(sizePx), faceName: faceName,
		}
	}
	mFace := face
	if mFace == nil {
		mFace = cjk
	}
	m := mFace.Metrics()
	adv, ok := mFace.GlyphAdvance('M')
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
	return &softwarePainter{
		face:     face,
		cjkFace:  cjk,
		cellW:    cw,
		cellH:    ch,
		ascent:   ascent,
		sizePx:   float64(sizePx),
		faceName: faceName,
	}
}

func (p *softwarePainter) metrics() (cw, ch int) {
	if p == nil {
		return cellW, cellH
	}
	return p.cellW, p.cellH
}

func (p *softwarePainter) close() {
	if p == nil {
		return
	}
	if p.face != nil {
		_ = p.face.Close()
		p.face = nil
	}
	if p.cjkFace != nil {
		_ = p.cjkFace.Close()
		p.cjkFace = nil
	}
}

// paintOpts controls layered chrome/intro/shell paint.
type paintOpts struct {
	Shell        [][]cellPix
	Chrome       [][]cellPix
	Overlay      [][]cellPix
	PadY         int
	ShellBot     int
	CurX, CurY   int
	CurVis       bool
	CurAlpha     float64
	DimShell     bool
	SettingsOpen bool
	// Intro / underlay
	MatrixCells  []rainCell
	WatermarkFade float64
	// Input bar (themed; drawn as solid panel + glyphs)
	InputPrompt   string
	InputLines    []string // visual lines of content (no prompt on row 0 — prompt separate)
	InputCaretRow int
	InputCaretCol int // content col (after prompt on row 0)
	InputEmpty    bool
	InputHint     string
	ShowInput     bool
	CursorStyle   int // config.CursorStyle as int to avoid import cycle issues — use values 0/1/2
}

// paintFrame draws shell + chrome + intro + input + overlay into dst.
func (p *softwarePainter) paintFrame(dst *image.RGBA, o paintOpts) {
	if dst == nil || p == nil {
		return
	}
	w, h := dst.Bounds().Dx(), dst.Bounds().Dy()
	// Theme void background (not hardcoded grey).
	draw.Draw(dst, dst.Bounds(), &image.Uniform{C: color.RGBA{
		R: chrome.VoidR, G: chrome.VoidG, B: chrome.VoidB, A: 255,
	}}, image.Point{}, draw.Src)

	cw, ch := p.cellW, p.cellH
	const padX = 4
	padY, shellBot := o.PadY, o.ShellBot
	if shellBot > h {
		shellBot = h
	}

	// Shell band base (black) — same as Windows BLACK_BRUSH before watermark.
	if shellBot > padY {
		fillRectRGBA(dst, 0, padY, w, shellBot-padY, 0, 0, 0)
	}

	// Center 硯 UNDER shell cells (Windows blitGrid order). Only ink pixels
	// so we never stamp a black "card" over the field.
	if o.WatermarkFade > 0.01 && !o.DimShell {
		p.paintShellWatermark(dst, padY, shellBot, o.WatermarkFade)
	}

	// Shell grid on top of watermark (opaque cell BGs hide the mark).
	for y, row := range o.Shell {
		py := padY + y*ch
		if py+ch > shellBot {
			break
		}
		for x, cell := range row {
			px := padX + x*cw
			if px >= w {
				break
			}
			br, bg, bb := cell.BR, cell.BG, cell.BB
			if o.CurVis && x == o.CurX && y == o.CurY {
				a := o.CurAlpha
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
			// Default black BG: leave watermark visible (skip fill).
			if br != 0 || bg != 0 || bb != 0 {
				fillRectRGBA(dst, px, py, cw, ch, br, bg, bb)
			}
			if cell.Ch != 0 && cell.Ch != ' ' {
				p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
			}
		}
	}

	// Matrix rain (intro or settings underlay) over the shell field.
	if len(o.MatrixCells) > 0 {
		if o.SettingsOpen || o.DimShell {
			// Matte first for settings underlay.
			fillShellMatte(dst, padY, shellBot, true)
		}
		p.paintMatrixRain(dst, padY, shellBot, o.MatrixCells)
	} else if o.DimShell && !o.SettingsOpen {
		// Non-settings overlay: dim matte + 猫咪 texture.
		fillShellMatte(dst, padY, shellBot, false)
		p.paintDimNekoField(dst, padY, shellBot)
	} else if o.DimShell {
		// Generic dim.
		for y := padY; y < shellBot && y < h; y++ {
			for x := 0; x < w; x++ {
				i := dst.PixOffset(x, y)
				dst.Pix[i+0] = dst.Pix[i+0] / 3
				dst.Pix[i+1] = dst.Pix[i+1] / 3
				dst.Pix[i+2] = dst.Pix[i+2] / 3
			}
		}
	}

	// Chrome strip — bar fill first so empty cells show theme bar, not void.
	chromeH := 0
	if len(o.Chrome) > 0 {
		chromeH = len(o.Chrome) * ch
		fillRectRGBA(dst, 0, 0, w, chromeH, chrome.BarR, chrome.BarG, chrome.BarB)
		paintCellStrip(p, dst, o.Chrome, padX, 0, true)
	}

	// Themed Warp input bar.
	if o.ShowInput {
		p.paintInputBar(dst, o, shellBot, h)
	}

	// Overlay card — full-width cell grid with transparent gutters (same as
	// Windows). Lip gloss PlaceHorizontal already centers the card; painting
	// a solid matte over the full measured width blocked matrix rain on the
	// left/right and made the dialog look off-center.
	if len(o.Overlay) > 0 {
		oh := len(o.Overlay) * ch
		shellH := shellBot - padY
		oy := padY + (shellH-oh)/4
		if oy+oh > shellBot {
			oy = shellBot - oh
		}
		if oy < padY {
			oy = padY
		}
		// ox=0: gutter cells are transparent so rain/neko shows through.
		paintCellStrip(p, dst, o.Overlay, 0, oy, false)
	}
}

func (p *softwarePainter) paintInputBar(dst *image.RGBA, o paintOpts, shellBot, clientH int) {
	w := dst.Bounds().Dx()
	cw, ch := p.cellW, p.cellH
	barH := clientH - shellBot
	if barH < ch {
		barH = ch + 4
	}
	top := shellBot
	// Panel fill (theme).
	fillRectRGBA(dst, 0, top, w, barH, chrome.PanelR, chrome.PanelG, chrome.PanelB)
	// Primary accent hairline.
	hair := ch / 10
	if hair < 1 {
		hair = 1
	}
	fillRectRGBA(dst, 0, top, w, hair, chrome.PrimR, chrome.PrimG, chrome.PrimB)
	topPad := ch / 5
	if topPad < 2 {
		topPad = 2
	}
	padTop := top + hair + topPad
	const padX = 8

	prompt := o.InputPrompt
	if prompt == "" {
		prompt = inputBarPrompt
	}
	promptRunes := []rune(prompt)
	promptW := len(promptRunes) * cw

	if o.InputEmpty {
		// Prompt in primary.
		x := padX
		for _, r := range promptRunes {
			p.drawGlyph(dst, x, padTop, r, chrome.PrimR, chrome.PrimG, chrome.PrimB)
			x += cw
		}
		// Placeholder hint in soft.
		if o.InputHint != "" {
			x = padX + promptW + 2*cw
			for _, r := range []rune(o.InputHint) {
				p.drawGlyph(dst, x, padTop, r, chrome.SoftR, chrome.SoftG, chrome.SoftB)
				x += cw
			}
		}
		p.paintInputCaret(dst, padX+promptW, padTop, o.CursorStyle, o.CurAlpha)
		return
	}

	for i, line := range o.InputLines {
		y := padTop + i*ch
		xText := padX + promptW
		if i == 0 {
			x := padX
			for _, r := range promptRunes {
				p.drawGlyph(dst, x, y, r, chrome.PrimR, chrome.PrimG, chrome.PrimB)
				x += cw
			}
		}
		if line != "" {
			x := xText
			for _, r := range []rune(line) {
				p.drawGlyph(dst, x, y, r, chrome.TextR, chrome.TextG, chrome.TextB)
				x += cw
			}
		}
	}
	caretY := padTop + o.InputCaretRow*ch
	caretX := padX + promptW + o.InputCaretCol*cw
	p.paintInputCaret(dst, caretX, caretY, o.CursorStyle, o.CurAlpha)
}

func (p *softwarePainter) paintInputCaret(dst *image.RGBA, x, y, style int, alpha float64) {
	if alpha <= 0 {
		return
	}
	cw, ch := p.cellW, p.cellH
	cr, cg, cb := blendRGB(
		chrome.PanelR, chrome.PanelG, chrome.PanelB,
		chrome.PrimR, chrome.PrimG, chrome.PrimB,
		alpha,
	)
	switch style {
	case 1: // underline
		th := ch / 8
		if th < 2 {
			th = 2
		}
		fillRectRGBA(dst, x, y+ch-th, cw, th, cr, cg, cb)
	case 2: // bar
		th := cw / 5
		if th < 1 {
			th = 1
		}
		fillRectRGBA(dst, x, y, th, ch, cr, cg, cb)
	default: // block
		// Semi-opaque block via blend already applied to color.
		fillRectRGBA(dst, x, y, cw, ch, cr, cg, cb)
	}
}

// paintCellStrip paints a cell grid. barMode fills default-black empty cells as bar.
func paintCellStrip(p *softwarePainter, dst *image.RGBA, cells [][]cellPix, ox, oy int, barMode bool) {
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
			br, bg, bb := cell.BR, cell.BG, cell.BB
			empty := cell.Ch == 0 || cell.Ch == ' '
			if !barMode && empty && isTransparentOverlayBG(br, bg, bb) {
				if cell.Ch != 0 && cell.Ch != ' ' {
					p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
				}
				continue
			}
			if barMode && br == 0 && bg == 0 && bb == 0 {
				// Leave bar underlay (already filled).
				if !empty {
					p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
				}
				continue
			}
			if !(barMode && empty && br == chrome.BarR && bg == chrome.BarG && bb == chrome.BarB) {
				fillRectRGBA(dst, px, py, cw, ch, br, bg, bb)
			}
			if !empty {
				p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
			}
		}
	}
}

func (p *softwarePainter) drawGlyph(dst *image.RGBA, px, py int, r rune, fr, fg, fb byte) {
	if p == nil {
		return
	}
	// Never use Gohu for CJK/halfwidth — it paints .notdef tofu for Index==0.
	// Prefer CJK face for those; primary mono for Latin/ASCII.
	var faces []font.Face
	if isEastAsianRune(r) || isHalfwidthKatakana(r) {
		if p.cjkFace != nil && cjkHasRune(r) {
			faces = append(faces, p.cjkFace)
		}
		// Do NOT fall back to primary for CJK — tofu boxes look worse than a bar.
	} else {
		if p.face != nil && primaryHasRune(r) {
			faces = append(faces, p.face)
		}
		if p.cjkFace != nil && cjkHasRune(r) {
			faces = append(faces, p.cjkFace)
		}
	}
	for _, face := range faces {
		if face == nil {
			continue
		}
		dr, mask, maskp, _, ok := face.Glyph(fixed.P(px, py+p.ascent), r)
		if !ok {
			continue
		}
		col := image.NewUniform(color.RGBA{R: fr, G: fg, B: fb, A: 255})
		draw.DrawMask(dst, dr, col, image.Point{}, mask, maskp, draw.Over)
		return
	}
	// Ultimate fallback for rain: a bright bar so streams never go blank/tofu.
	if isHalfwidthKatakana(r) || isEastAsianRune(r) {
		fillRectRGBA(dst, px+p.cellW/3, py+2, max(1, p.cellW/3), max(1, p.cellH-4), fr, fg, fb)
	}
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func isHalfwidthKatakana(r rune) bool {
	// U+FF61–FF9F halfwidth forms (incl. ｱ-ﾝ used in matrix rain).
	return r >= 0xFF61 && r <= 0xFF9F
}

func fillRectRGBA(dst *image.RGBA, x, y, w, h int, r, g, b byte) {
	bounds := dst.Bounds()
	rect := image.Rect(x, y, x+w, y+h).Intersect(bounds)
	if rect.Empty() {
		return
	}
	for py := rect.Min.Y; py < rect.Max.Y; py++ {
		for px := rect.Min.X; px < rect.Max.X; px++ {
			i := dst.PixOffset(px, py)
			dst.Pix[i+0] = r
			dst.Pix[i+1] = g
			dst.Pix[i+2] = b
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
