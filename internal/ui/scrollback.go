package ui

import (
	"strings"

	"github.com/hinshun/vt10x"
)

const defaultScrollbackMax = 5000

// scrollback keeps lines that have scrolled off the live VT screen so the user
// can wheel upward. Detection is heuristic: after each parse, if the screen
// shifted up by one row, the old top row is pushed into history.
type scrollback struct {
	lines  []string // oldest → newest (scrolled-off rows only)
	max    int
	prev   []string // previous live screen snapshot
	offset int      // 0 = pinned to bottom; >0 = lines scrolled up
}

func newScrollback() *scrollback {
	return &scrollback{max: defaultScrollbackMax}
}

func snapshotScreen(term vt10x.Terminal) []string {
	cols, rows := term.Size()
	out := make([]string, rows)
	buf := make([]rune, cols)
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			buf[x] = displayRune(term.Cell(x, y).Char)
		}
		// Keep full width so selection/copy stay column-aligned.
		out[y] = string(buf)
	}
	return out
}

func (s *scrollback) noteScreen(term vt10x.Terminal) {
	cur := snapshotScreen(term)
	if len(s.prev) == len(cur) && len(cur) > 1 {
		// Scrolled up by one: prev[1:] == cur[:len-1]
		if screenShiftedUp(s.prev, cur) {
			s.push(s.prev[0])
		}
	}
	s.prev = cur
	// Clamp offset if history shrank somehow
	if s.offset > len(s.lines) {
		s.offset = len(s.lines)
	}
}

func screenShiftedUp(prev, cur []string) bool {
	if len(prev) != len(cur) || len(cur) < 2 {
		return false
	}
	for i := 0; i < len(cur)-1; i++ {
		if prev[i+1] != cur[i] {
			return false
		}
	}
	// Top line must have changed (otherwise no scroll, just identical frame)
	return prev[0] != cur[0] || prev[len(prev)-1] != cur[len(cur)-1]
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

// view assembles viewportRows lines for painting (top → bottom).
func (s *scrollback) view(term vt10x.Terminal, viewportRows int) [][]rune {
	live := snapshotScreen(term)
	// Combined document: history + live
	doc := make([]string, 0, len(s.lines)+len(live))
	doc = append(doc, s.lines...)
	doc = append(doc, live...)

	cols, _ := term.Size()
	if viewportRows < 1 {
		viewportRows = 1
	}
	start := len(doc) - viewportRows - s.offset
	if start < 0 {
		start = 0
	}
	out := make([][]rune, viewportRows)
	for i := 0; i < viewportRows; i++ {
		row := make([]rune, cols)
		for x := range row {
			row[x] = ' '
		}
		idx := start + i
		if idx >= 0 && idx < len(doc) {
			rs := []rune(doc[idx])
			for x := 0; x < cols && x < len(rs); x++ {
				row[x] = rs[x]
			}
		}
		out[i] = row
	}
	return out
}

// absLine maps a viewport row to an absolute document line index.
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
	live := snapshotScreen(term)
	i := abs - len(s.lines)
	if i >= 0 && i < len(live) {
		return strings.TrimRight(live[i], " ")
	}
	return ""
}
