package ui

import (
	"strings"

	"github.com/hinshun/vt10x"
)

// cellSel is an inclusive rectangular selection in absolute document lines.
type cellSel struct {
	active bool
	x0, y0 int // anchor (absolute line, col)
	x1, y1 int // focus
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
