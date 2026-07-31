package ui

import (
	"strings"

	"github.com/hinshun/vt10x"
)

const defaultScrollbackMax = 5000

// scrollback keeps rows that have scrolled off the live VT screen.
// History stores plain text (color not preserved for v0.3); live rows keep full cells.
type scrollback struct {
	lines  []string
	max    int
	prev   []string
	offset int
}

func newScrollback() *scrollback {
	return &scrollback{max: defaultScrollbackMax}
}

func snapshotScreenText(term vt10x.Terminal) []string {
	cols, rows := term.Size()
	out := make([]string, rows)
	buf := make([]rune, cols)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			buf[x] = displayRune(term.Cell(x, y).Char)
		}
		out[y] = string(buf)
	}
	return out
}

func snapshotScreenCells(term vt10x.Terminal) [][]cellPix {
	cols, rows := term.Size()
	out := make([][]cellPix, rows)
	for y := 0; y < rows; y++ {
		row := make([]cellPix, cols)
		for x := 0; x < cols; x++ {
			row[x] = glyphToCell(term.Cell(x, y))
		}
		out[y] = row
	}
	return out
}

func (s *scrollback) noteScreen(term vt10x.Terminal) {
	cur := snapshotScreenText(term)
	if len(s.prev) == len(cur) && len(cur) > 1 {
		// Multi-line scroll: find largest n where prev[n:] == cur[:len-n]
		if n := scrollAmount(s.prev, cur); n > 0 {
			for i := 0; i < n; i++ {
				s.push(s.prev[i])
			}
		}
	}
	s.prev = cur
	if s.offset > len(s.lines) {
		s.offset = len(s.lines)
	}
}

// scrollAmount returns how many rows the screen shifted up (0 if none).
func scrollAmount(prev, cur []string) int {
	if len(prev) != len(cur) || len(cur) < 2 {
		return 0
	}
	maxN := len(cur) - 1
	for n := maxN; n >= 1; n-- {
		ok := true
		for i := 0; i < len(cur)-n; i++ {
			if prev[i+n] != cur[i] {
				ok = false
				break
			}
		}
		if ok && (prev[0] != cur[0] || prev[len(prev)-1] != cur[len(cur)-1]) {
			return n
		}
	}
	return 0
}

func screenShiftedUp(prev, cur []string) bool {
	return scrollAmount(prev, cur) == 1
}

func (s *scrollback) push(line string) {
	s.lines = append(s.lines, strings.TrimRight(line, " "))
	if len(s.lines) > s.max {
		drop := len(s.lines) - s.max
		s.lines = append([]string(nil), s.lines[drop:]...)
		if s.offset > 0 {
			s.offset -= drop
			if s.offset < 0 {
				s.offset = 0
			}
		}
	}
}

func (s *scrollback) scrollBy(delta, viewportRows int) {
	maxOff := len(s.lines)
	s.offset += delta
	if s.offset < 0 {
		s.offset = 0
	}
	if s.offset > maxOff {
		s.offset = maxOff
	}
}

func (s *scrollback) atBottom() bool { return s.offset == 0 }

func (s *scrollback) stickBottom() { s.offset = 0 }

// viewCells builds the viewport with color for live rows and default colors for history.
func (s *scrollback) viewCells(term vt10x.Terminal, viewportRows int) [][]cellPix {
	live := snapshotScreenCells(term)
	cols, _ := term.Size()
	if viewportRows < 1 {
		viewportRows = 1
	}

	// Document length in lines
	docLen := len(s.lines) + len(live)
	start := docLen - viewportRows - s.offset
	if start < 0 {
		start = 0
	}

	out := make([][]cellPix, viewportRows)
	for i := 0; i < viewportRows; i++ {
		row := make([]cellPix, cols)
		for x := range row {
			row[x] = cellPix{Ch: ' ', FR: 220, FG: 220, FB: 220}
		}
		idx := start + i
		if idx < len(s.lines) {
			// history: monochrome
			rs := []rune(s.lines[idx])
			for x := 0; x < cols && x < len(rs); x++ {
				row[x] = cellPix{Ch: rs[x], FR: 180, FG: 180, FB: 180}
			}
		} else {
			li := idx - len(s.lines)
			if li >= 0 && li < len(live) {
				copy(row, live[li])
			}
		}
		out[i] = row
	}
	return out
}

// view keeps rune-only API for tests / simple callers.
func (s *scrollback) view(term vt10x.Terminal, viewportRows int) [][]rune {
	cells := s.viewCells(term, viewportRows)
	out := make([][]rune, len(cells))
	for y := range cells {
		row := make([]rune, len(cells[y]))
		for x := range cells[y] {
			row[x] = cells[y][x].Ch
		}
		out[y] = row
	}
	return out
}

func (s *scrollback) absLine(viewY, viewportRows, liveRows int) int {
	docLen := len(s.lines) + liveRows
	start := docLen - viewportRows - s.offset
	if start < 0 {
		start = 0
	}
	return start + viewY
}

func (s *scrollback) lineText(abs int, term vt10x.Terminal) string {
	if abs < 0 {
		return ""
	}
	if abs < len(s.lines) {
		return s.lines[abs]
	}
	live := snapshotScreenText(term)
	i := abs - len(s.lines)
	if i >= 0 && i < len(live) {
		return strings.TrimRight(live[i], " ")
	}
	return ""
}
