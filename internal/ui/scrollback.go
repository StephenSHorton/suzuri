package ui

import (
	"strings"

	"github.com/hinshun/vt10x"

	"github.com/StephenSHorton/suzuri/internal/chrome"
)

const defaultScrollbackMax = 5000

// History line kinds for Warp-style command blocks.
const (
	histNormal byte = iota
	histBlockRule  // horizontal rule above a command
	histBlockCmd   // "❯ command" header
)

type histLine struct {
	text string
	kind byte
}

// scrollback keeps rows that have scrolled off the live VT screen.
// History stores plain text (color not preserved for v0.3); live rows keep full cells.
// Command blocks are host-injected history lines with kind metadata for color.
type scrollback struct {
	lines  []histLine
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
	s.pushKind(line, histNormal)
}

func (s *scrollback) pushKind(line string, kind byte) {
	s.lines = append(s.lines, histLine{text: strings.TrimRight(line, " "), kind: kind})
	if len(s.lines) > s.max {
		drop := len(s.lines) - s.max
		s.lines = append([]histLine(nil), s.lines[drop:]...)
		if s.offset > 0 {
			s.offset -= drop
			if s.offset < 0 {
				s.offset = 0
			}
		}
	}
}

// pushBlock injects a Warp-style command block into history (above live screen).
// cols is used for the rule width; cmd may be multi-line.
func (s *scrollback) pushBlock(cmd string, cols int) {
	if stringsTrimSpace(cmd) == "" {
		return
	}
	if cols < 12 {
		cols = 12
	}
	ruleW := cols - 2
	if ruleW < 8 {
		ruleW = 8
	}
	if ruleW > 120 {
		ruleW = 120
	}
	// Spacer + rule + command line(s).
	s.pushKind("", histNormal)
	s.pushKind(strings.Repeat("─", ruleW), histBlockRule)
	// First line gets the prompt; continuation lines are indented.
	prompt := inputBarPrompt
	indent := strings.Repeat(" ", len([]rune(prompt)))
	parts := strings.Split(cmd, "\n")
	for i, p := range parts {
		if i == 0 {
			s.pushKind(prompt+p, histBlockCmd)
		} else {
			s.pushKind(indent+p, histBlockCmd)
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

// viewCells builds the viewport with color for live rows and styled history.
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
			hl := s.lines[idx]
			rs := []rune(hl.text)
			fr, fg, fb := histColor(hl.kind)
			for x := 0; x < cols && x < len(rs); x++ {
				row[x] = cellPix{Ch: rs[x], FR: fr, FG: fg, FB: fb}
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

// histColor returns FG for a history line kind (theme-aware).
func histColor(kind byte) (r, g, b byte) {
	switch kind {
	case histBlockRule:
		// Soft primary for the rule.
		return chrome.PrimR/2 + 40, chrome.PrimG/2 + 40, chrome.PrimB/2 + 40
	case histBlockCmd:
		return chrome.PrimR, chrome.PrimG, chrome.PrimB
	default:
		return chrome.SoftR, chrome.SoftG, chrome.SoftB
	}
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
		return s.lines[abs].text
	}
	live := snapshotScreenText(term)
	i := abs - len(s.lines)
	if i >= 0 && i < len(live) {
		return strings.TrimRight(live[i], " ")
	}
	return ""
}
