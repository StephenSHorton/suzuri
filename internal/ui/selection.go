package ui

import (
	"strings"
	"time"
	"unicode"

	"github.com/hinshun/vt10x"
)

// cellSel is an inclusive rectangular selection in absolute document lines.
type cellSel struct {
	active bool
	x0, y0 int // anchor (absolute line, col)
	x1, y1 int // focus
}

// multiClick tracks double/triple clicks for word/line selection (terminal + notes).
// count is 1 on a fresh click, 2 within the window on the same cell, 3 on the next, then wraps to 1.
type multiClick struct {
	count int
	x, y  int
	at    time.Time
}

const multiClickWindow = 500 * time.Millisecond

func absInt(a int) int {
	if a < 0 {
		return -a
	}
	return a
}

// bump records a click at (x, y) and returns the multi-click count (1–3).
func (m *multiClick) bump(x, y int, now time.Time) int {
	same := m.count > 0 &&
		absInt(x-m.x) <= 1 &&
		absInt(y-m.y) <= 1 &&
		now.Sub(m.at) <= multiClickWindow
	if same {
		m.count++
		if m.count > 3 {
			m.count = 1
		}
	} else {
		m.count = 1
	}
	m.x, m.y = x, y
	m.at = now
	return m.count
}

func isWordRune(r rune) bool {
	return r == '_' || unicode.IsLetter(r) || unicode.IsDigit(r)
}

// wordBounds returns inclusive start/end columns for the word (or whitespace/punct run) at col.
func wordBounds(line string, col int) (start, end int) {
	rs := []rune(line)
	if len(rs) == 0 {
		return 0, 0
	}
	if col < 0 {
		col = 0
	}
	if col >= len(rs) {
		col = len(rs) - 1
	}
	r := rs[col]
	if isWordRune(r) {
		start, end = col, col
		for start > 0 && isWordRune(rs[start-1]) {
			start--
		}
		for end+1 < len(rs) && isWordRune(rs[end+1]) {
			end++
		}
		return start, end
	}
	if r == ' ' || r == '\t' {
		start, end = col, col
		for start > 0 && (rs[start-1] == ' ' || rs[start-1] == '\t') {
			start--
		}
		for end+1 < len(rs) && (rs[end+1] == ' ' || rs[end+1] == '\t') {
			end++
		}
		return start, end
	}
	// Punctuation / other: contiguous non-word non-space.
	start, end = col, col
	for start > 0 {
		p := rs[start-1]
		if isWordRune(p) || p == ' ' || p == '\t' {
			break
		}
		start--
	}
	for end+1 < len(rs) {
		p := rs[end+1]
		if isWordRune(p) || p == ' ' || p == '\t' {
			break
		}
		end++
	}
	return start, end
}

// applyShellMultiClick sets tab.sel for a 1/2/3 click on cell (x, absY).
// click 1: caret cell; 2: word; 3: whole line (0..cols-1).
func applyShellMultiClick(sel *cellSel, sb *scrollback, term vt10x.Terminal, x, absY, clickCount int) {
	if sel == nil || term == nil {
		return
	}
	cols, _ := term.Size()
	if cols < 1 {
		cols = 1
	}
	sel.active = true
	switch {
	case clickCount >= 3:
		sel.x0, sel.y0 = 0, absY
		sel.x1, sel.y1 = cols-1, absY
	case clickCount == 2:
		line := ""
		if sb != nil {
			line = sb.lineText(absY, term)
		}
		x0, x1 := wordBounds(line, x)
		if x1 >= cols {
			x1 = cols - 1
		}
		sel.x0, sel.y0 = x0, absY
		sel.x1, sel.y1 = x1, absY
	default:
		if x < 0 {
			x = 0
		}
		if x >= cols {
			x = cols - 1
		}
		sel.x0, sel.y0 = x, absY
		sel.x1, sel.y1 = x, absY
	}
}

func (s *cellSel) clear() {
	s.active = false
	s.x0, s.y0, s.x1, s.y1 = 0, 0, 0, 0
}

func (s *cellSel) empty() bool {
	return !s.active || (s.x0 == s.x1 && s.y0 == s.y1)
}

func (s *cellSel) norm() (minX, minY, maxX, maxY int) {
	minX, maxX = s.x0, s.x1
	minY, maxY = s.y0, s.y1
	if minY > maxY || (minY == maxY && minX > maxX) {
		minX, maxX = maxX, minX
		minY, maxY = maxY, minY
	}
	return
}

// text extracts selected text from scrollback + live screen (line-wise).
func (s *cellSel) text(sb *scrollback, term vt10x.Terminal) string {
	if s.empty() {
		return ""
	}
	minX, minY, maxX, maxY := s.norm()
	cols, _ := term.Size()
	if minX < 0 {
		minX = 0
	}
	if maxX >= cols {
		maxX = cols - 1
	}

	var b strings.Builder
	for y := minY; y <= maxY; y++ {
		line := sb.lineText(y, term)
		rs := []rune(line)
		// pad
		for len(rs) < cols {
			rs = append(rs, ' ')
		}
		start, end := 0, cols-1
		if y == minY {
			start = minX
		}
		if y == maxY {
			end = maxX
		}
		if start > end {
			start, end = end, start
		}
		if start < 0 {
			start = 0
		}
		if end >= len(rs) {
			end = len(rs) - 1
		}
		if end >= start {
			b.WriteString(string(rs[start : end+1]))
		}
		if y != maxY {
			b.WriteByte('\n')
		}
	}
	return strings.TrimRight(b.String(), " \t")
}

func (s *cellSel) containsAbs(x, y int) bool {
	if !s.active || s.empty() {
		return false
	}
	minX, minY, maxX, maxY := s.norm()
	if y < minY || y > maxY {
		return false
	}
	if y == minY && y == maxY {
		return x >= minX && x <= maxX
	}
	if y == minY {
		return x >= minX
	}
	if y == maxY {
		return x <= maxX
	}
	return true
}
