//go:build darwin

package ui

import (
	"image"
	"math"
)

// drawCellGlyph paints box-drawing and block elements edge-to-edge in the
// cell, the way Windows Terminal / our GDI path do. Font ink for Gohu (and
// most monos) has padding, so stacked │/║ and joined ─ lines show gaps
// between rows/columns. Returns true if the rune was handled.
func (p *softwarePainter) drawCellGlyph(dst *image.RGBA, px, py int, r rune, fr, fg, fb byte) bool {
	if p == nil || dst == nil {
		return false
	}
	cw, ch := p.cellW, p.cellH
	if cw < 1 {
		cw = cellW
	}
	if ch < 1 {
		ch = cellH
	}
	cell := image.Rect(px, py, px+cw, py+ch)
	if r >= 0x2580 && r <= 0x259F {
		return drawBlockElementSW(dst, r, cell, fr, fg, fb)
	}
	if r >= 0x2500 && r <= 0x257F {
		return drawBoxDrawingSW(dst, r, cell, fr, fg, fb)
	}
	// Powerline-ish triangles (optional solid wedges).
	switch r {
	case '', '▶', '▸', '►':
		fillTriangleSW(dst, cell, fr, fg, fb, true)
		return true
	case '', '◀', '◂', '◄':
		fillTriangleSW(dst, cell, fr, fg, fb, false)
		return true
	}
	return false
}

func cellMidSW(cell image.Rectangle) (cx, cy int) {
	return (cell.Min.X + cell.Max.X) / 2, (cell.Min.Y + cell.Max.Y) / 2
}

func lineThickSW(cell image.Rectangle, heavy bool) int {
	cw := cell.Dx()
	ch := cell.Dy()
	t := ch / 10
	if cw < ch {
		t = cw / 10
	}
	if heavy {
		t = ch / 6
		if cw < ch {
			t = cw / 6
		}
	}
	if t < 1 {
		t = 1
	}
	if heavy && t < 2 {
		t = 2
	}
	return t
}

func fillRGBSW(dst *image.RGBA, r image.Rectangle, fr, fg, fb byte) {
	if r.Empty() {
		return
	}
	fillRectRGBA(dst, r.Min.X, r.Min.Y, r.Dx(), r.Dy(), fr, fg, fb)
}

func hStrokeSW(dst *image.RGBA, x0, x1, y, thick int, fr, fg, fb byte) {
	if thick < 1 {
		thick = 1
	}
	if x1 < x0 {
		x0, x1 = x1, x0
	}
	top := y - thick/2
	fillRGBSW(dst, image.Rect(x0, top, x1, top+thick), fr, fg, fb)
}

func vStrokeSW(dst *image.RGBA, x, y0, y1, thick int, fr, fg, fb byte) {
	if thick < 1 {
		thick = 1
	}
	if y1 < y0 {
		y0, y1 = y1, y0
	}
	left := x - thick/2
	fillRGBSW(dst, image.Rect(left, y0, left+thick, y1), fr, fg, fb)
}

type armSW uint8

const (
	armN armSW = 1 << iota
	armS
	armE
	armW
)

func drawArmsSW(dst *image.RGBA, cell image.Rectangle, a armSW, heavy bool, fr, fg, fb byte) {
	cx, cy := cellMidSW(cell)
	t := lineThickSW(cell, heavy)
	if a&armN != 0 {
		vStrokeSW(dst, cx, cell.Min.Y, cy+t/2, t, fr, fg, fb)
	}
	if a&armS != 0 {
		vStrokeSW(dst, cx, cy-t/2, cell.Max.Y, t, fr, fg, fb)
	}
	if a&armE != 0 {
		hStrokeSW(dst, cx-t/2, cell.Max.X, cy, t, fr, fg, fb)
	}
	if a&armW != 0 {
		hStrokeSW(dst, cell.Min.X, cx+t/2, cy, t, fr, fg, fb)
	}
}

func drawBoxDrawingSW(dst *image.RGBA, r rune, cell image.Rectangle, fr, fg, fb byte) bool {
	switch r {
	case '─': // U+2500
		_, cy := cellMidSW(cell)
		hStrokeSW(dst, cell.Min.X, cell.Max.X, cy, lineThickSW(cell, false), fr, fg, fb)
		return true
	case '│': // U+2502
		cx, _ := cellMidSW(cell)
		vStrokeSW(dst, cx, cell.Min.Y, cell.Max.Y, lineThickSW(cell, false), fr, fg, fb)
		return true
	case '┌':
		drawArmsSW(dst, cell, armS|armE, false, fr, fg, fb)
		return true
	case '┐':
		drawArmsSW(dst, cell, armS|armW, false, fr, fg, fb)
		return true
	case '└':
		drawArmsSW(dst, cell, armN|armE, false, fr, fg, fb)
		return true
	case '┘':
		drawArmsSW(dst, cell, armN|armW, false, fr, fg, fb)
		return true
	case '├':
		drawArmsSW(dst, cell, armN|armS|armE, false, fr, fg, fb)
		return true
	case '┤':
		drawArmsSW(dst, cell, armN|armS|armW, false, fr, fg, fb)
		return true
	case '┬':
		drawArmsSW(dst, cell, armS|armE|armW, false, fr, fg, fb)
		return true
	case '┴':
		drawArmsSW(dst, cell, armN|armE|armW, false, fr, fg, fb)
		return true
	case '┼':
		drawArmsSW(dst, cell, armN|armS|armE|armW, false, fr, fg, fb)
		return true
	case '━':
		_, cy := cellMidSW(cell)
		hStrokeSW(dst, cell.Min.X, cell.Max.X, cy, lineThickSW(cell, true), fr, fg, fb)
		return true
	case '┃':
		cx, _ := cellMidSW(cell)
		vStrokeSW(dst, cx, cell.Min.Y, cell.Max.Y, lineThickSW(cell, true), fr, fg, fb)
		return true
	case '┏':
		drawArmsSW(dst, cell, armS|armE, true, fr, fg, fb)
		return true
	case '┓':
		drawArmsSW(dst, cell, armS|armW, true, fr, fg, fb)
		return true
	case '┗':
		drawArmsSW(dst, cell, armN|armE, true, fr, fg, fb)
		return true
	case '┛':
		drawArmsSW(dst, cell, armN|armW, true, fr, fg, fb)
		return true
	case '┣':
		drawArmsSW(dst, cell, armN|armS|armE, true, fr, fg, fb)
		return true
	case '┫':
		drawArmsSW(dst, cell, armN|armS|armW, true, fr, fg, fb)
		return true
	case '┳':
		drawArmsSW(dst, cell, armS|armE|armW, true, fr, fg, fb)
		return true
	case '┻':
		drawArmsSW(dst, cell, armN|armE|armW, true, fr, fg, fb)
		return true
	case '╋':
		drawArmsSW(dst, cell, armN|armS|armE|armW, true, fr, fg, fb)
		return true
	case '╭', '╮', '╯', '╰': // U+256D–U+2570
		drawRoundCornerSW(dst, cell, r, fr, fg, fb)
		return true
	case '═':
		_, cy := cellMidSW(cell)
		t := lineThickSW(cell, false)
		gap := t + 1
		hStrokeSW(dst, cell.Min.X, cell.Max.X, cy-gap, t, fr, fg, fb)
		hStrokeSW(dst, cell.Min.X, cell.Max.X, cy+gap, t, fr, fg, fb)
		return true
	case '║':
		cx, _ := cellMidSW(cell)
		t := lineThickSW(cell, false)
		gap := t + 1
		vStrokeSW(dst, cx-gap, cell.Min.Y, cell.Max.Y, t, fr, fg, fb)
		vStrokeSW(dst, cx+gap, cell.Min.Y, cell.Max.Y, t, fr, fg, fb)
		return true
	case '╔':
		drawArmsSW(dst, cell, armS|armE, true, fr, fg, fb)
		return true
	case '╗':
		drawArmsSW(dst, cell, armS|armW, true, fr, fg, fb)
		return true
	case '╚':
		drawArmsSW(dst, cell, armN|armE, true, fr, fg, fb)
		return true
	case '╝':
		drawArmsSW(dst, cell, armN|armW, true, fr, fg, fb)
		return true
	}
	return false
}

func drawRoundCornerSW(dst *image.RGBA, cell image.Rectangle, which rune, fr, fg, fb byte) {
	cx, cy := cellMidSW(cell)
	t := lineThickSW(cell, false)
	cw, ch := cell.Dx(), cell.Dy()

	r := cw
	if ch < r {
		r = ch
	}
	r = r/2 - t
	if r < 2 {
		r = 2
	}
	if maxArm := cw / 2; r > maxArm-1 && maxArm > 2 {
		r = maxArm - 1
	}
	if maxArm := ch / 2; r > maxArm-1 && maxArm > 2 {
		r = maxArm - 1
	}

	switch which {
	case '╭':
		acx, acy := cx+r, cy+r
		strokeEllipseCWSW(dst, acx, acy, r, r, t, math.Pi, 3*math.Pi/2, fr, fg, fb)
		hStrokeSW(dst, acx, cell.Max.X, cy, t, fr, fg, fb)
		vStrokeSW(dst, cx, acy, cell.Max.Y, t, fr, fg, fb)
	case '╮':
		acx, acy := cx-r, cy+r
		strokeEllipseCWSW(dst, acx, acy, r, r, t, 3*math.Pi/2, 2*math.Pi, fr, fg, fb)
		hStrokeSW(dst, cell.Min.X, acx, cy, t, fr, fg, fb)
		vStrokeSW(dst, cx, acy, cell.Max.Y, t, fr, fg, fb)
	case '╯':
		acx, acy := cx-r, cy-r
		strokeEllipseCWSW(dst, acx, acy, r, r, t, 0, math.Pi/2, fr, fg, fb)
		hStrokeSW(dst, cell.Min.X, acx, cy, t, fr, fg, fb)
		vStrokeSW(dst, cx, cell.Min.Y, acy, t, fr, fg, fb)
	case '╰':
		acx, acy := cx+r, cy-r
		strokeEllipseCWSW(dst, acx, acy, r, r, t, math.Pi/2, math.Pi, fr, fg, fb)
		hStrokeSW(dst, acx, cell.Max.X, cy, t, fr, fg, fb)
		vStrokeSW(dst, cx, cell.Min.Y, acy, t, fr, fg, fb)
	}
}

func strokeEllipseCWSW(dst *image.RGBA, cx, cy, rx, ry, thick int, start, end float64, fr, fg, fb byte) {
	if rx < 1 || ry < 1 {
		return
	}
	if thick < 1 {
		thick = 1
	}
	avgR := float64(rx+ry) / 2
	arcLen := math.Abs(end-start) * avgR
	steps := int(arcLen + 0.5)
	if steps < 16 {
		steps = 16
	}
	if steps > 96 {
		steps = 96
	}
	half := thick / 2
	for i := 0; i <= steps; i++ {
		th := start + (end-start)*float64(i)/float64(steps)
		x := cx + int(math.Round(float64(rx)*math.Cos(th)))
		y := cy + int(math.Round(float64(ry)*math.Sin(th)))
		fillRGBSW(dst, image.Rect(x-half, y-half, x-half+thick, y-half+thick), fr, fg, fb)
	}
}

func drawBlockElementSW(dst *image.RGBA, r rune, cell image.Rectangle, fr, fg, fb byte) bool {
	cw, ch := cell.Dx(), cell.Dy()
	switch r {
	case '█':
		fillRGBSW(dst, cell, fr, fg, fb)
		return true
	case '▀':
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y, cell.Max.X, cell.Min.Y+ch/2), fr, fg, fb)
		return true
	case '▄':
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y+ch/2, cell.Max.X, cell.Max.Y), fr, fg, fb)
		return true
	case '▌':
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y, cell.Min.X+cw/2, cell.Max.Y), fr, fg, fb)
		return true
	case '▐':
		fillRGBSW(dst, image.Rect(cell.Min.X+cw/2, cell.Min.Y, cell.Max.X, cell.Max.Y), fr, fg, fb)
		return true
	case '▔':
		h := max(1, ch/8)
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y, cell.Max.X, cell.Min.Y+h), fr, fg, fb)
		return true
	case '▁':
		h := max(1, ch/8)
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Max.Y-h, cell.Max.X, cell.Max.Y), fr, fg, fb)
		return true
	case '▏':
		w := max(1, cw/8)
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y, cell.Min.X+w, cell.Max.Y), fr, fg, fb)
		return true
	case '▎':
		w := max(1, cw/4)
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y, cell.Min.X+w, cell.Max.Y), fr, fg, fb)
		return true
	case '▍':
		w := max(1, (cw*3)/8)
		fillRGBSW(dst, image.Rect(cell.Min.X, cell.Min.Y, cell.Min.X+w, cell.Max.Y), fr, fg, fb)
		return true
	case '░':
		fillShadeSW(dst, cell, fr, fg, fb, 1)
		return true
	case '▒':
		fillShadeSW(dst, cell, fr, fg, fb, 2)
		return true
	case '▓':
		fillShadeSW(dst, cell, fr, fg, fb, 3)
		return true
	}
	if r >= 0x2580 && r <= 0x259F {
		fillRGBSW(dst, cell, fr, fg, fb)
		return true
	}
	return false
}

func fillShadeSW(dst *image.RGBA, cell image.Rectangle, fr, fg, fb byte, level int) {
	step := 4 - level
	if step < 1 {
		step = 1
	}
	for y := cell.Min.Y; y < cell.Max.Y; y += step {
		for x := cell.Min.X; x < cell.Max.X; x += step {
			if ((x-cell.Min.X)/step+(y-cell.Min.Y)/step)%2 == 0 {
				x1 := x + step
				if x1 > cell.Max.X {
					x1 = cell.Max.X
				}
				y1 := y + step
				if y1 > cell.Max.Y {
					y1 = cell.Max.Y
				}
				fillRGBSW(dst, image.Rect(x, y, x1, y1), fr, fg, fb)
			}
		}
	}
}

// fillTriangleSW: right=true → ▶, right=false → ◀.
func fillTriangleSW(dst *image.RGBA, cell image.Rectangle, fr, fg, fb byte, right bool) {
	cw, ch := cell.Dx(), cell.Dy()
	if cw < 1 || ch < 1 {
		return
	}
	half := ch / 2
	if half < 1 {
		half = 1
	}
	for row := 0; row < ch; row++ {
		var span int
		if row <= half {
			span = cw * row / half
		} else {
			span = cw * (ch - 1 - row) / half
		}
		if span < 1 {
			span = 1
		}
		y := cell.Min.Y + row
		if right {
			fillRGBSW(dst, image.Rect(cell.Min.X, y, cell.Min.X+span, y+1), fr, fg, fb)
		} else {
			fillRGBSW(dst, image.Rect(cell.Max.X-span, y, cell.Max.X, y+1), fr, fg, fb)
		}
	}
}
