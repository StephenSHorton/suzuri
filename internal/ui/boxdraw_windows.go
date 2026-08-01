//go:build windows

package ui

import (
	"math"

	"github.com/lxn/win"
)

// drawCellGlyph paints special terminal glyphs the way Windows Terminal does:
// stretch box-drawing and block elements to the full cell so adjacent cells
// form continuous lines/panels (no font ink padding gaps).
// Returns true if the rune was handled (caller must not TextOut it).
func drawCellGlyph(hdc win.HDC, r rune, cell win.RECT, fr, fg, fb byte) bool {
	if r >= 0x2580 && r <= 0x259F {
		return drawBlockElement(hdc, r, cell, fr, fg, fb)
	}
	if r >= 0x2500 && r <= 0x257F {
		return drawBoxDrawing(hdc, r, cell, fr, fg, fb)
	}
	// Powerline-ish triangles often used in prompts (optional solid wedges).
	switch r {
	case '', '▶', '▸', '►': // U+E0B0 solid right
		fillTriangle(hdc, cell, fr, fg, fb, triRight)
		return true
	case '', '◀', '◂', '◄':
		fillTriangle(hdc, cell, fr, fg, fb, triLeft)
		return true
	}
	return false
}

func cellMid(cell win.RECT) (cx, cy int32) {
	return (cell.Left + cell.Right) / 2, (cell.Top + cell.Bottom) / 2
}

func lineThick(cell win.RECT, heavy bool) int32 {
	cw := cell.Right - cell.Left
	ch := cell.Bottom - cell.Top
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

func fillRGB(hdc win.HDC, r win.RECT, fr, fg, fb byte) {
	if r.Right <= r.Left || r.Bottom <= r.Top {
		return
	}
	lb := win.LOGBRUSH{LbStyle: win.BS_SOLID, LbColor: win.RGB(fr, fg, fb)}
	if brush := win.CreateBrushIndirect(&lb); brush != 0 {
		fillRect(hdc, r, brush)
		win.DeleteObject(win.HGDIOBJ(brush))
	}
}

// Horizontal stroke from x0→x1 centered on y (full-cell edge-to-edge capable).
func hStroke(hdc win.HDC, x0, x1, y, thick int32, fr, fg, fb byte) {
	if thick < 1 {
		thick = 1
	}
	top := y - thick/2
	fillRGB(hdc, win.RECT{Left: x0, Top: top, Right: x1, Bottom: top + thick}, fr, fg, fb)
}

// Vertical stroke from y0→y1 centered on x.
func vStroke(hdc win.HDC, x, y0, y1, thick int32, fr, fg, fb byte) {
	if thick < 1 {
		thick = 1
	}
	left := x - thick/2
	fillRGB(hdc, win.RECT{Left: left, Top: y0, Right: left + thick, Bottom: y1}, fr, fg, fb)
}

// Connection bits for light box-drawing arms that meet at the cell center.
type arm uint8

const (
	armN arm = 1 << iota
	armS
	armE
	armW
)

func drawArms(hdc win.HDC, cell win.RECT, a arm, heavy bool, fr, fg, fb byte) {
	cx, cy := cellMid(cell)
	t := lineThick(cell, heavy)
	if a&armN != 0 {
		vStroke(hdc, cx, cell.Top, cy+t/2, t, fr, fg, fb)
	}
	if a&armS != 0 {
		vStroke(hdc, cx, cy-t/2, cell.Bottom, t, fr, fg, fb)
	}
	if a&armE != 0 {
		hStroke(hdc, cx-t/2, cell.Right, cy, t, fr, fg, fb)
	}
	if a&armW != 0 {
		hStroke(hdc, cell.Left, cx+t/2, cy, t, fr, fg, fb)
	}
}

func drawBoxDrawing(hdc win.HDC, r rune, cell win.RECT, fr, fg, fb byte) bool {
	// Light single-line
	switch r {
	case '─': // U+2500
		_, cy := cellMid(cell)
		hStroke(hdc, cell.Left, cell.Right, cy, lineThick(cell, false), fr, fg, fb)
		return true
	case '│': // U+2502
		cx, _ := cellMid(cell)
		vStroke(hdc, cx, cell.Top, cell.Bottom, lineThick(cell, false), fr, fg, fb)
		return true
	case '┌':
		drawArms(hdc, cell, armS|armE, false, fr, fg, fb)
		return true
	case '┐':
		drawArms(hdc, cell, armS|armW, false, fr, fg, fb)
		return true
	case '└':
		drawArms(hdc, cell, armN|armE, false, fr, fg, fb)
		return true
	case '┘':
		drawArms(hdc, cell, armN|armW, false, fr, fg, fb)
		return true
	case '├':
		drawArms(hdc, cell, armN|armS|armE, false, fr, fg, fb)
		return true
	case '┤':
		drawArms(hdc, cell, armN|armS|armW, false, fr, fg, fb)
		return true
	case '┬':
		drawArms(hdc, cell, armS|armE|armW, false, fr, fg, fb)
		return true
	case '┴':
		drawArms(hdc, cell, armN|armE|armW, false, fr, fg, fb)
		return true
	case '┼':
		drawArms(hdc, cell, armN|armS|armE|armW, false, fr, fg, fb)
		return true
	// Heavy
	case '━':
		_, cy := cellMid(cell)
		hStroke(hdc, cell.Left, cell.Right, cy, lineThick(cell, true), fr, fg, fb)
		return true
	case '┃':
		cx, _ := cellMid(cell)
		vStroke(hdc, cx, cell.Top, cell.Bottom, lineThick(cell, true), fr, fg, fb)
		return true
	case '┏':
		drawArms(hdc, cell, armS|armE, true, fr, fg, fb)
		return true
	case '┓':
		drawArms(hdc, cell, armS|armW, true, fr, fg, fb)
		return true
	case '┗':
		drawArms(hdc, cell, armN|armE, true, fr, fg, fb)
		return true
	case '┛':
		drawArms(hdc, cell, armN|armW, true, fr, fg, fb)
		return true
	case '┣':
		drawArms(hdc, cell, armN|armS|armE, true, fr, fg, fb)
		return true
	case '┫':
		drawArms(hdc, cell, armN|armS|armW, true, fr, fg, fb)
		return true
	case '┳':
		drawArms(hdc, cell, armS|armE|armW, true, fr, fg, fb)
		return true
	case '┻':
		drawArms(hdc, cell, armN|armE|armW, true, fr, fg, fb)
		return true
	case '╋':
		drawArms(hdc, cell, armN|armS|armE|armW, true, fr, fg, fb)
		return true
	// Rounded corners (Lip Gloss RoundedBorder / Unicode light arcs).
	// These must be real quarter-circles — square L-arms look sharp vs WT.
	case '╭', '╮', '╯', '╰': // U+256D–U+2570
		drawRoundCorner(hdc, cell, r, fr, fg, fb)
		return true
	// Double lines — two parallel light strokes.
	case '═':
		_, cy := cellMid(cell)
		t := lineThick(cell, false)
		gap := t + 1
		hStroke(hdc, cell.Left, cell.Right, cy-gap, t, fr, fg, fb)
		hStroke(hdc, cell.Left, cell.Right, cy+gap, t, fr, fg, fb)
		return true
	case '║':
		cx, _ := cellMid(cell)
		t := lineThick(cell, false)
		gap := t + 1
		vStroke(hdc, cx-gap, cell.Top, cell.Bottom, t, fr, fg, fb)
		vStroke(hdc, cx+gap, cell.Top, cell.Bottom, t, fr, fg, fb)
		return true
	case '╔':
		drawDoubleCorner(hdc, cell, armS|armE, fr, fg, fb)
		return true
	case '╗':
		drawDoubleCorner(hdc, cell, armS|armW, fr, fg, fb)
		return true
	case '╚':
		drawDoubleCorner(hdc, cell, armN|armE, fr, fg, fb)
		return true
	case '╝':
		drawDoubleCorner(hdc, cell, armN|armW, fr, fg, fb)
		return true
	}
	// Fallback: still try light cross if unknown box char — leave to TextOut.
	return false
}

func drawDoubleCorner(hdc win.HDC, cell win.RECT, a arm, fr, fg, fb byte) {
	// Approximate double corners with heavy single arms for now (still seamless).
	drawArms(hdc, cell, a, true, fr, fg, fb)
}

// drawRoundCorner paints Unicode light arcs ╭╮╯╰ the way fonts/Lip Gloss
// intend: one continuous rounded-rect corner on the box midlines.
//
// ─ and │ meet at cell centers, so the sharp corner of a box is at (cx,cy).
// Rounding replaces that joint with a quarter-ellipse whose center sits
// *inside* the box by radius r, and whose arc bulges *out* toward the
// outer corner — not a second arc outside the cell, and not a square L
// stacked on a full-cell sweep.
//
// Angle: 0=east, increasing clockwise (y grows downward).
func drawRoundCorner(hdc win.HDC, cell win.RECT, which rune, fr, fg, fb byte) {
	cx, cy := cellMid(cell)
	t := lineThick(cell, false)
	cw := cell.Right - cell.Left
	ch := cell.Bottom - cell.Top

	// Radius fills most of the cell so Charm neon cards look openly rounded,
	// but stays inside the cell (leave a little room for stroke thickness).
	r := cw
	if ch < r {
		r = ch
	}
	r = r/2 - t
	if r < 2 {
		r = 2
	}
	// Cap so arms remain visible for joining to ─/│.
	if maxArm := cw / 2; r > maxArm-1 && maxArm > 2 {
		r = maxArm - 1
	}
	if maxArm := ch / 2; r > maxArm-1 && maxArm > 2 {
		r = maxArm - 1
	}

	switch which {
	case '╭': // top-left — open down + right
		// Interior center; arc west→north (outer TL bulge).
		acx, acy := cx+r, cy+r
		strokeEllipseCW(hdc, acx, acy, r, r, t, math.Pi, 3*math.Pi/2, fr, fg, fb)
		// Arms from arc ends to the far mid-edges (join ─ right, │ below).
		hStroke(hdc, acx, cell.Right, cy, t, fr, fg, fb) // north end → right
		vStroke(hdc, cx, acy, cell.Bottom, t, fr, fg, fb) // west end → bottom
	case '╮': // top-right — open down + left
		acx, acy := cx-r, cy+r
		strokeEllipseCW(hdc, acx, acy, r, r, t, 3*math.Pi/2, 2*math.Pi, fr, fg, fb)
		hStroke(hdc, cell.Left, acx, cy, t, fr, fg, fb)
		vStroke(hdc, cx, acy, cell.Bottom, t, fr, fg, fb)
	case '╯': // bottom-right — open up + left
		acx, acy := cx-r, cy-r
		strokeEllipseCW(hdc, acx, acy, r, r, t, 0, math.Pi/2, fr, fg, fb)
		hStroke(hdc, cell.Left, acx, cy, t, fr, fg, fb)
		vStroke(hdc, cx, cell.Top, acy, t, fr, fg, fb)
	case '╰': // bottom-left — open up + right
		acx, acy := cx+r, cy-r
		strokeEllipseCW(hdc, acx, acy, r, r, t, math.Pi/2, math.Pi, fr, fg, fb)
		hStroke(hdc, acx, cell.Right, cy, t, fr, fg, fb)
		vStroke(hdc, cx, cell.Top, acy, t, fr, fg, fb)
	default:
		return
	}
}

// strokeEllipseCW stamps a thick elliptical arc.
// Angles: 0=east, clockwise, y-down (x = cx + rx*cos, y = cy + ry*sin).
func strokeEllipseCW(hdc win.HDC, cx, cy, rx, ry, thick int32, start, end float64, fr, fg, fb byte) {
	if rx < 1 || ry < 1 {
		return
	}
	if thick < 1 {
		thick = 1
	}
	// Arc length ≈ average radius * Δθ
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
		x := cx + int32(math.Round(float64(rx)*math.Cos(th)))
		y := cy + int32(math.Round(float64(ry)*math.Sin(th)))
		fillRGB(hdc, win.RECT{
			Left:   x - half,
			Top:    y - half,
			Right:  x - half + thick,
			Bottom: y - half + thick,
		}, fr, fg, fb)
	}
}

func drawBlockElement(hdc win.HDC, r rune, cell win.RECT, fr, fg, fb byte) bool {
	cw := cell.Right - cell.Left
	ch := cell.Bottom - cell.Top
	switch r {
	case '█': // full block
		fillRGB(hdc, cell, fr, fg, fb)
		return true
	case '▀': // upper half
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top, Right: cell.Right, Bottom: cell.Top + ch/2}, fr, fg, fb)
		return true
	case '▄': // lower half
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top + ch/2, Right: cell.Right, Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '▌': // left half
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top, Right: cell.Left + cw/2, Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '▐': // right half
		fillRGB(hdc, win.RECT{Left: cell.Left + cw/2, Top: cell.Top, Right: cell.Right, Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '▔': // upper 1/8
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top, Right: cell.Right, Bottom: cell.Top + max32(1, ch/8)}, fr, fg, fb)
		return true
	case '▁': // lower 1/8
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Bottom - max32(1, ch/8), Right: cell.Right, Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '▏': // left 1/8
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top, Right: cell.Left + max32(1, cw/8), Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '▎': // left 1/4
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top, Right: cell.Left + max32(1, cw/4), Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '▍': // left 3/8
		fillRGB(hdc, win.RECT{Left: cell.Left, Top: cell.Top, Right: cell.Left + max32(1, (cw*3)/8), Bottom: cell.Bottom}, fr, fg, fb)
		return true
	case '░': // light shade ~25%
		fillShade(hdc, cell, fr, fg, fb, 1)
		return true
	case '▒': // medium ~50%
		fillShade(hdc, cell, fr, fg, fb, 2)
		return true
	case '▓': // dark ~75%
		fillShade(hdc, cell, fr, fg, fb, 3)
		return true
	}
	// Quadrants and other blocks: full fill is better than gappy font ink.
	if r >= 0x2580 && r <= 0x259F {
		fillRGB(hdc, cell, fr, fg, fb)
		return true
	}
	return false
}

// fillShade paints a checker denser for higher levels (1..3).
func fillShade(hdc win.HDC, cell win.RECT, fr, fg, fb byte, level int) {
	step := int32(4 - level) // 3,2,1
	if step < 1 {
		step = 1
	}
	for y := cell.Top; y < cell.Bottom; y += step {
		for x := cell.Left; x < cell.Right; x += step {
			if ((x-cell.Left)/step+(y-cell.Top)/step)%2 == 0 {
				fillRGB(hdc, win.RECT{Left: x, Top: y, Right: min32(x+step, cell.Right), Bottom: min32(y+step, cell.Bottom)}, fr, fg, fb)
			}
		}
	}
}

type triDir int

const (
	triRight triDir = iota
	triLeft
)

func fillTriangle(hdc win.HDC, cell win.RECT, fr, fg, fb byte, dir triDir) {
	// Approximate with stepped rects (GDI has no easy fill polygon without more FFI).
	cw := cell.Right - cell.Left
	ch := cell.Bottom - cell.Top
	if cw < 1 || ch < 1 {
		return
	}
	for row := int32(0); row < ch; row++ {
		var x0, x1 int32
		// progress 0..1 from top to bottom for a centered wedge? classic powerline is full-height right triangle.
		t := float64(row) / float64(ch-1)
		if ch == 1 {
			t = 0
		}
		// Midline triangle: left edge grows/shrinks — use classic: at mid height full width.
		// Simpler: solid right triangle with base on left (▶) or right (◀).
		half := ch / 2
		var span int32
		if row <= half {
			span = int32(float64(cw) * float64(row) / float64(max32(1, half)))
		} else {
			span = int32(float64(cw) * float64(ch-1-row) / float64(max32(1, half)))
		}
		if span < 1 {
			span = 1
		}
		switch dir {
		case triRight:
			x0 = cell.Left
			x1 = cell.Left + span
		case triLeft:
			x1 = cell.Right
			x0 = cell.Right - span
		}
		_ = t
		fillRGB(hdc, win.RECT{Left: x0, Top: cell.Top + row, Right: x1, Bottom: cell.Top + row + 1}, fr, fg, fb)
	}
}

func max32(a, b int32) int32 {
	if a > b {
		return a
	}
	return b
}

func min32(a, b int32) int32 {
	if a < b {
		return a
	}
	return b
}
