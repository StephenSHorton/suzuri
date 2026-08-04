//go:build darwin

package ui

import (
	"image"
	"image/color"
	"image/draw"

	"golang.org/x/image/font"
	"golang.org/x/image/math/fixed"
	xdraw "golang.org/x/image/draw"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

// softwarePainter rasterizes terminal cell grids into an RGBA buffer.
type softwarePainter struct {
	face        font.Face
	cjkFace     font.Face
	symbolsFace font.Face // Apple Symbols — ☕ and other UI marks mono lacks
	cellW       int
	cellH       int
	ascent      int
	sizePx      float64
	faceName    string
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
	sym := symbolsFaceForSize(float64(sizePx))
	if face == nil && cjk == nil {
		return &softwarePainter{
			cellW: cellW, cellH: cellH, ascent: cellH - 4,
			sizePx: float64(sizePx), faceName: faceName, symbolsFace: sym,
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
	// Prefer font Height, but always reserve room for full ink (ascent+descent).
	// Gohu @14 needs ~12 ascent + ~3 descent; a rounded Height of 14 left only
	// 2px under the baseline and clipped j/g/y/p/q (and the next cell's BG
	// fill covers any overflow).
	ascent := m.Ascent.Round()
	descent := m.Descent.Round()
	if descent < 0 {
		descent = -descent
	}
	ch := m.Height.Round()
	minInk := ascent + descent
	if minInk < 1 {
		minInk = sizePx
	}
	// +1 so descenders never share a pixel row with the next cell's top edge.
	if ch < minInk+1 {
		ch = minInk + 1
	}
	if ch < sizePx {
		ch = sizePx + 2
	}
	if ascent < 1 {
		ascent = ch - descent
		if ascent < 1 {
			ascent = ch - 4
		}
	}
	// Keep baseline high enough that descent fits inside the cell.
	if ascent+descent > ch {
		ascent = ch - descent
		if ascent < 1 {
			ascent = 1
		}
	}
	return &softwarePainter{
		face:        face,
		cjkFace:     cjk,
		symbolsFace: sym,
		cellW:       cw,
		cellH:       ch,
		ascent:      ascent,
		sizePx:      float64(sizePx),
		faceName:    faceName,
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
	// Intro / underlay (MatrixCells paint over the shell field after the grid).
	MatrixCells   []rainCell
	// ShellMatrixCells: quiet always-on rain/grain/waves UNDER shell glyphs.
	ShellMatrixCells []rainCell
	// CRTScanlines: 0..1 intensity for horizontal scanlines + soft side vignette.
	CRTScanlines  float64
	WatermarkFade float64
	// Sub-line smooth scroll (0..1): shift shell content up by frac*cellH.
	ScrollFrac float64
	// Scrollbar (host-drawn; Charm has no native host scrollbar).
	ScrollThumbY int
	ScrollThumbH int
	ScrollTrack  bool // paint track when true
	// Input bar (themed; drawn as solid panel + glyphs)
	InputPrompt   string
	InputLines    []string // visual lines of content (no prompt on row 0 — prompt separate)
	InputCaretRow int
	InputCaretCol int // content col (after prompt on row 0)
	InputEmpty    bool
	InputHint     string
	InputCwd      string // shortened path above the command line
	InputGhost    string // soft Tab-completion preview after caret
	ShowInput     bool
	CursorStyle   int // config.CursorStyle as int to avoid import cycle issues — use values 0/1/2
	// Scrollback-attached images (primary buffer only; alt-screen leaves empty).
	Images []visImage
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

	// Always-on shell ambient under glyphs (grain/waves/fireflies/rain).
	// CRT scanlines paint AFTER the grid so empty cells don't hide them.
	if len(o.ShellMatrixCells) > 0 && !o.DimShell {
		p.paintMatrixRain(dst, padY, shellBot, o.ShellMatrixCells)
	}

	// Center 硯 UNDER shell cells (Windows blitGrid order). Only ink pixels
	// so we never stamp a black "card" over the field.
	if o.WatermarkFade > 0.01 && !o.DimShell {
		p.paintShellWatermark(dst, padY, shellBot, o.WatermarkFade)
	}

	// Sub-line smooth scroll: shift grid up by a fraction of a cell.
	yShift := 0
	if o.ScrollFrac > 0.001 && o.ScrollFrac < 1 {
		yShift = int(float64(ch) * o.ScrollFrac)
	}

	// Shell grid on top of watermark (opaque cell BGs hide the mark).
	// Leave a few px on the right for the scrollbar track.
	const scrollGutter = 8
	shellRight := w - scrollGutter
	for y, row := range o.Shell {
		py := padY + y*ch - yShift
		if py+ch <= padY || py >= shellBot {
			continue
		}
		for x, cell := range row {
			px := padX + x*cw
			if px >= shellRight {
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

	// Scrollbar on the right edge of the shell band (not Charm — host paint).
	if o.ScrollTrack && shellBot > padY+8 {
		trackX := w - 6
		trackTop := padY + 2
		trackH := shellBot - padY - 4
		// Dim track
		fillRectRGBA(dst, trackX, trackTop, 4, trackH, 28, 28, 32)
		if o.ScrollThumbH > 0 {
			ty := trackTop + o.ScrollThumbY
			th := o.ScrollThumbH
			if ty < trackTop {
				ty = trackTop
			}
			if ty+th > trackTop+trackH {
				th = trackTop + trackH - ty
			}
			// Soft primary thumb
			tr := blendByte(40, chrome.PrimR, 0.45)
			tg := blendByte(40, chrome.PrimG, 0.45)
			tb := blendByte(44, chrome.PrimB, 0.45)
			fillRectRGBA(dst, trackX, ty, 4, th, tr, tg, tb)
		}
	}

	// Host-rendered scrollback images (Windows GDI StretchBlt parity).
	if len(o.Images) > 0 && !o.DimShell {
		p.paintShellImages(dst, o, padY, shellBot, yShift)
	}

	// CRT scanlines over the grid (must not be covered by cell fills).
	if o.CRTScanlines > 0.01 && !o.DimShell {
		p.paintCRTScanlines(dst, padY, shellBot, o.CRTScanlines)
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

	if cwd := stringsTrimSpace(o.InputCwd); cwd != "" {
		maxCols := (w - padX - 8) / cw
		if maxCols < 8 {
			maxCols = 8
		}
		label := truncateRunes(cwd, maxCols)
		x := padX
		for _, r := range []rune(label) {
			p.drawGlyph(dst, x, padTop, r, chrome.SoftR, chrome.SoftG, chrome.SoftB)
			x += cw
		}
		padTop += ch
	}

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
	if o.InputGhost != "" {
		x := caretX
		for _, r := range []rune(o.InputGhost) {
			p.drawGlyph(dst, x, caretY, r, chrome.MuteR, chrome.MuteG, chrome.MuteB)
			x += cw
		}
	}
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
	// Stretch box-drawing / blocks to the full cell so stacked │ and joined ─
	// form continuous lines (Gohu and most monos leave ink padding → gaps).
	if p.drawCellGlyph(dst, px, py, r, fr, fg, fb) {
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
		// UI marks mono faces omit (caffeine ☕, dingbats, …).
		if p.symbolsFace != nil && symbolsHasRune(r) {
			faces = append(faces, p.symbolsFace)
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

// paintPaneGrid draws a VT cell grid into a pane's content rect.
// Does not stamp a solid black card — shell band / always-on rain already
// filled the field (Windows blitGrid: default black BG leaves rain visible).
func (p *softwarePainter) paintPaneGrid(dst *image.RGBA, grid [][]cellPix, g paneGeom, curX, curY int, curVis bool, curAlpha float64) {
	if dst == nil || p == nil || g.w < 1 || g.h < 1 {
		return
	}
	cw, ch := p.cellW, p.cellH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	const padX = 2
	shellRight := int(g.x) + int(g.w) - 2
	for y, row := range grid {
		py := int(g.y) + y*ch
		if py+ch <= int(g.y) || py >= int(g.y)+int(g.h) {
			continue
		}
		for x, cell := range row {
			px := int(g.x) + padX + x*cw
			if px >= shellRight {
				break
			}
			br, bg, bb := cell.BR, cell.BG, cell.BB
			if curVis && x == curX && y == curY {
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
			// Default black BG: leave shell rain / watermark visible (skip fill).
			if br != 0 || bg != 0 || bb != 0 {
				fillRectRGBA(dst, px, py, cw, ch, br, bg, bb)
			}
			if cell.Ch != 0 && cell.Ch != ' ' {
				p.drawGlyph(dst, px, py, cell.Ch, cell.FR, cell.FG, cell.FB)
			}
		}
	}
}

// paintPaneImages draws scrollback images clipped to a pane layout rect.
func (p *softwarePainter) paintPaneImages(dst *image.RGBA, vis []visImage, g paneGeom) {
	if dst == nil || p == nil || len(vis) == 0 || g.w < 40 {
		return
	}
	cw, ch := p.cellW, p.cellH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	padX := int(g.x) + 4
	padY := int(g.y)
	shellBot := int(g.y + g.h)
	availW := int(g.w) - 8
	if availW < 40 {
		return
	}
	for _, v := range vis {
		if v.img == nil || v.img.img == nil {
			continue
		}
		src := v.img.img
		sb := src.Bounds()
		sw, sh := sb.Dx(), sb.Dy()
		if sw < 1 || sh < 1 {
			continue
		}
		yTop := padY + v.viewRow*ch
		bodyTop := yTop
		bodyRows := v.span
		if v.span > 1 {
			bodyTop = yTop + ch
			bodyRows = v.span - 1
		}
		yBot := bodyTop + bodyRows*ch
		if yBot > shellBot {
			yBot = shellBot
		}
		if bodyTop >= shellBot || yBot <= padY {
			continue
		}
		boxH := yBot - bodyTop
		if boxH < 8 {
			continue
		}
		dw, dh := fitPreferNative(sw, sh, availW, boxH)
		if dw < 1 || dh < 1 {
			continue
		}
		drawTop := bodyTop
		if drawTop < padY {
			drawTop = padY
		}
		dr := image.Rect(padX, drawTop, padX+dw, drawTop+dh)
		clip := image.Rect(int(g.x), padY, int(g.x+g.w), shellBot)
		dr = dr.Intersect(clip).Intersect(dst.Bounds())
		if dr.Empty() {
			continue
		}
		fillRectRGBA(dst, dr.Min.X-1, dr.Min.Y-1, dr.Dx()+2, 1, 60, 60, 70)
		fillRectRGBA(dst, dr.Min.X-1, dr.Max.Y, dr.Dx()+2, 1, 60, 60, 70)
		fillRectRGBA(dst, dr.Min.X-1, dr.Min.Y-1, 1, dr.Dy()+2, 60, 60, 70)
		fillRectRGBA(dst, dr.Max.X, dr.Min.Y-1, 1, dr.Dy()+2, 60, 60, 70)
		xdraw.CatmullRom.Scale(dst, dr, src, sb, xdraw.Over, nil)
	}
}

// paintPaneTitles draws the mini title strip on each multi-pane leaf.
func (p *softwarePainter) paintPaneTitles(dst *image.RGBA, layouts []paneGeom, cw, ch int) {
	if dst == nil || p == nil || len(layouts) < 2 {
		return
	}
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	for _, g := range layouts {
		if g.titleH < 1 || g.pane == nil {
			continue
		}
		// Focused: slightly brighter bar.
		br, bg, bb := chrome.BarR, chrome.BarG, chrome.BarB
		if g.focused {
			br = blendByte(br, chrome.PrimR, 0.25)
			bg = blendByte(bg, chrome.PrimG, 0.25)
			bb = blendByte(bb, chrome.PrimB, 0.25)
		}
		fillRectRGBA(dst, int(g.x), int(g.titleY), int(g.w), int(g.titleH), br, bg, bb)
		title := g.pane.displayTitle()
		if title == "" {
			title = "shell"
		}
		maxCols := int(g.w)/cw - 2
		if maxCols < 1 {
			maxCols = 1
		}
		label := truncateRunes(title, maxCols)
		fr, fg, fb := chrome.SoftR, chrome.SoftG, chrome.SoftB
		if g.focused {
			fr, fg, fb = chrome.TextR, chrome.TextG, chrome.TextB
		}
		x := int(g.x) + 4
		y := int(g.titleY)
		for _, r := range []rune(label) {
			p.drawGlyph(dst, x, y, r, fr, fg, fb)
			x += cw
		}
	}
}

// paintPaneSashes draws shared dividers between sibling panes.
func (p *softwarePainter) paintPaneSashes(dst *image.RGBA, sashes []sashGeom, shell struct{ x, y, w, h int32 }) {
	if dst == nil {
		return
	}
	// Outer perimeter.
	if shell.w > 0 && shell.h > 0 {
		fillRectRGBA(dst, int(shell.x), int(shell.y), int(shell.w), 1, 40, 40, 48)
		fillRectRGBA(dst, int(shell.x), int(shell.y+shell.h-1), int(shell.w), 1, 40, 40, 48)
		fillRectRGBA(dst, int(shell.x), int(shell.y), 1, int(shell.h), 40, 40, 48)
		fillRectRGBA(dst, int(shell.x+shell.w-1), int(shell.y), 1, int(shell.h), 40, 40, 48)
	}
	for _, s := range sashes {
		fillRectRGBA(dst, int(s.x), int(s.y), int(s.w), int(s.h), 48, 48, 56)
	}
}

// paintOverlayOnly re-draws a floating card strip over existing shell content.
func (p *softwarePainter) paintOverlayOnly(dst *image.RGBA, overlay [][]cellPix, padY, shellBot int) {
	if dst == nil || p == nil || len(overlay) == 0 {
		return
	}
	ch := p.cellH
	if ch < 1 {
		ch = cellH
	}
	oh := len(overlay) * ch
	shellH := shellBot - padY
	oy := padY + (shellH-oh)/4
	if oy+oh > shellBot {
		oy = shellBot - oh
	}
	if oy < padY {
		oy = padY
	}
	paintCellStrip(p, dst, overlay, 0, oy, false)
}

// paintPaneInputBar draws one pane's command line into g.barY/g.barH.
func (p *softwarePainter) paintPaneInputBar(dst *image.RGBA, o paintOpts, g paneGeom) {
	if dst == nil || p == nil || g.barH < 1 {
		return
	}
	cw, ch := p.cellW, p.cellH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	top := int(g.barY)
	barH := int(g.barH)
	x0 := int(g.x)
	bw := int(g.w)
	if bw < 1 {
		return
	}
	fillRectRGBA(dst, x0, top, bw, barH, chrome.PanelR, chrome.PanelG, chrome.PanelB)
	hair, topPad, _ := inputBarVPads(int32(ch))
	fillRectRGBA(dst, x0, top, bw, int(hair), chrome.PrimR, chrome.PrimG, chrome.PrimB)
	padTop := top + int(hair) + int(topPad)
	const padX = 8

	if cwd := stringsTrimSpace(o.InputCwd); cwd != "" {
		maxCols := (bw - padX - 8) / cw
		if maxCols < 8 {
			maxCols = 8
		}
		label := truncateRunes(cwd, maxCols)
		x := x0 + padX
		for _, r := range []rune(label) {
			p.drawGlyph(dst, x, padTop, r, chrome.SoftR, chrome.SoftG, chrome.SoftB)
			x += cw
		}
		padTop += ch
	}

	prompt := o.InputPrompt
	if prompt == "" {
		prompt = inputBarPrompt
	}
	promptRunes := []rune(prompt)
	promptW := len(promptRunes) * cw

	if o.InputEmpty {
		x := x0 + padX
		for _, r := range promptRunes {
			p.drawGlyph(dst, x, padTop, r, chrome.PrimR, chrome.PrimG, chrome.PrimB)
			x += cw
		}
		if o.InputHint != "" {
			x = x0 + padX + promptW + 2*cw
			for _, r := range []rune(o.InputHint) {
				p.drawGlyph(dst, x, padTop, r, chrome.SoftR, chrome.SoftG, chrome.SoftB)
				x += cw
			}
		}
		if o.CurAlpha > 0 {
			p.paintInputCaret(dst, x0+padX+promptW, padTop, o.CursorStyle, o.CurAlpha)
		}
		return
	}

	for i, line := range o.InputLines {
		y := padTop + i*ch
		xText := x0 + padX + promptW
		if i == 0 {
			x := x0 + padX
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
	if o.CurAlpha > 0 {
		caretY := padTop + o.InputCaretRow*ch
		caretX := x0 + padX + promptW + o.InputCaretCol*cw
		if o.InputGhost != "" {
			x := caretX
			for _, r := range []rune(o.InputGhost) {
				p.drawGlyph(dst, x, caretY, r, chrome.MuteR, chrome.MuteG, chrome.MuteB)
				x += cw
			}
		}
		p.paintInputCaret(dst, caretX, caretY, o.CursorStyle, o.CurAlpha)
	}
}

// paintShellImages draws scrollback-attached bitmaps over reserved image rows.
// Caption row (sub=0) is left as cells; the body span is filled with the image.
func (p *softwarePainter) paintShellImages(dst *image.RGBA, o paintOpts, padY, shellBot, yShift int) {
	if dst == nil || p == nil || len(o.Images) == 0 {
		return
	}
	cw, ch := p.cellW, p.cellH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	const padX = 4
	w := dst.Bounds().Dx()
	availW := w - padX - 12 // leave scrollbar gutter
	if availW < 40 {
		return
	}
	for _, v := range o.Images {
		if v.img == nil || v.img.img == nil {
			continue
		}
		src := v.img.img
		sb := src.Bounds()
		sw, sh := sb.Dx(), sb.Dy()
		if sw < 1 || sh < 1 {
			continue
		}
		yTop := padY + v.viewRow*ch - yShift
		bodyTop := yTop
		bodyRows := v.span
		if v.span > 1 {
			bodyTop = yTop + ch // first row is caption
			bodyRows = v.span - 1
		}
		yBot := bodyTop + bodyRows*ch
		if yBot > shellBot {
			yBot = shellBot
		}
		if bodyTop >= shellBot || yBot <= padY {
			continue
		}
		boxH := yBot - bodyTop
		if boxH < 8 {
			continue
		}
		dw, dh := fitPreferNative(sw, sh, availW, boxH)
		if dw < 1 || dh < 1 {
			continue
		}
		drawTop := bodyTop
		if drawTop < padY {
			if bodyTop+dh < padY {
				continue
			}
			drawTop = padY
		}
		dr := image.Rect(padX, drawTop, padX+dw, drawTop+dh)
		// Clip to shell band.
		clip := image.Rect(0, padY, w, shellBot)
		dr = dr.Intersect(clip).Intersect(dst.Bounds())
		if dr.Empty() {
			continue
		}
		// Soft border.
		fillRectRGBA(dst, dr.Min.X-1, dr.Min.Y-1, dr.Dx()+2, 1, 60, 60, 70)
		fillRectRGBA(dst, dr.Min.X-1, dr.Max.Y, dr.Dx()+2, 1, 60, 60, 70)
		fillRectRGBA(dst, dr.Min.X-1, dr.Min.Y-1, 1, dr.Dy()+2, 60, 60, 70)
		fillRectRGBA(dst, dr.Max.X, dr.Min.Y-1, 1, dr.Dy()+2, 60, 60, 70)
		xdraw.CatmullRom.Scale(dst, dr, src, sb, xdraw.Over, nil)
	}
}

// paintKittyPlacements draws Grok/Kitty graphics protocol images over the
// shell cell grid (prompt previews, inline media). Cell coords are 0-based.
func (p *softwarePainter) paintKittyPlacements(dst *image.RGBA, places []kittyPlace, gfx *kittyGfxState, padY, shellBot int) {
	if p == nil || dst == nil || gfx == nil || len(places) == 0 {
		return
	}
	cw, ch := p.cellW, p.cellH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	const padX = 4
	// Stable paint order: lower z first so higher z draws on top.
	ordered := append([]kittyPlace(nil), places...)
	for i := 0; i < len(ordered); i++ {
		for j := i + 1; j < len(ordered); j++ {
			if ordered[j].z < ordered[i].z {
				ordered[i], ordered[j] = ordered[j], ordered[i]
			}
		}
	}
	for _, pl := range ordered {
		img := gfx.image(pl.id)
		if img == nil {
			continue
		}
		sb := img.Bounds()
		// Optional source crop (Kitty x,y,w,h in pixels).
		if pl.srcW > 0 && pl.srcH > 0 {
			r := image.Rect(pl.srcX, pl.srcY, pl.srcX+pl.srcW, pl.srcY+pl.srcH).Intersect(sb)
			if !r.Empty() {
				sb = r
			}
		}
		sw, sh := sb.Dx(), sb.Dy()
		if sw < 1 || sh < 1 {
			continue
		}
		px := padX + pl.col*cw
		py := padY + pl.row*ch
		boxW := pl.cols * cw
		boxH := pl.rows * ch
		if boxW < 4 || boxH < 4 {
			continue
		}
		dw, dh := fitPreferNative(sw, sh, boxW, boxH)
		if dw < 1 || dh < 1 {
			continue
		}
		// Center in placement box.
		ox := px + (boxW-dw)/2
		oy := py + (boxH-dh)/2
		dr := image.Rect(ox, oy, ox+dw, oy+dh)
		clip := image.Rect(0, padY, dst.Bounds().Dx(), shellBot)
		dr = dr.Intersect(clip).Intersect(dst.Bounds())
		if dr.Empty() {
			continue
		}
		xdraw.CatmullRom.Scale(dst, dr, img, sb, xdraw.Over, nil)
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

// paintCRTScanlines draws horizontal scanlines + soft side vignette over the grid.
func (p *softwarePainter) paintCRTScanlines(dst *image.RGBA, padY, shellBot int, intensity float64) {
	if dst == nil || intensity <= 0 || shellBot <= padY {
		return
	}
	if intensity > 1 {
		intensity = 1
	}
	w := dst.Bounds().Dx()
	// Darken every 3rd row (visible phosphor lines).
	lineA := 0.25 + intensity*0.35
	if lineA > 0.65 {
		lineA = 0.65
	}
	for y := padY; y < shellBot; y += 3 {
		for x := 0; x < w; x++ {
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = byte(float64(dst.Pix[i+0]) * (1 - lineA))
			dst.Pix[i+1] = byte(float64(dst.Pix[i+1]) * (1 - lineA))
			dst.Pix[i+2] = byte(float64(dst.Pix[i+2]) * (1 - lineA))
		}
	}
	// Side vignette
	vigW := w / 14
	if vigW < 12 {
		vigW = 12
	}
	if vigW > 64 {
		vigW = 64
	}
	va := 0.22 + intensity*0.3
	for y := padY; y < shellBot; y++ {
		for x := 0; x < vigW; x++ {
			a := va * (1 - float64(x)/float64(vigW))
			i := dst.PixOffset(x, y)
			dst.Pix[i+0] = byte(float64(dst.Pix[i+0]) * (1 - a))
			dst.Pix[i+1] = byte(float64(dst.Pix[i+1]) * (1 - a))
			dst.Pix[i+2] = byte(float64(dst.Pix[i+2]) * (1 - a))
			j := dst.PixOffset(w-1-x, y)
			dst.Pix[j+0] = byte(float64(dst.Pix[j+0]) * (1 - a))
			dst.Pix[j+1] = byte(float64(dst.Pix[j+1]) * (1 - a))
			dst.Pix[j+2] = byte(float64(dst.Pix[j+2]) * (1 - a))
		}
	}
}
