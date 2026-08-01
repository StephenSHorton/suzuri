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
//
// pin is a stick-bottom floor set on clear: history before pin stays in the
// buffer for scroll-up, but does not reappear when following the bottom.
type scrollback struct {
	lines  []histLine
	max    int
	prev   []string
	offset int
	pin    int // first history index shown at stick-bottom (0 = no pin)
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

// liveExtent returns how many leading live rows belong in the document.
// Trailing blank PTY rows are omitted so host-injected command blocks (history
// just above the live screen) stay visible at the bottom of the viewport
// instead of sitting above a full screen of empty cells.
//
// On the alternate screen (full-screen TUI apps) the entire grid is used —
// no clipping.
func liveExtent(term vt10x.Terminal) int {
	cols, rows := term.Size()
	if rows < 1 {
		return 0
	}
	if term.Mode()&vt10x.ModeAltScreen != 0 {
		return rows
	}
	last := -1
	if cur := term.Cursor(); cur.Y >= 0 && cur.Y < rows {
		last = cur.Y
	}
	for y := 0; y < rows; y++ {
		for x := 0; x < cols; x++ {
			ch := displayRune(term.Cell(x, y).Char)
			if ch != ' ' && ch != 0 {
				if y > last {
					last = y
				}
				break
			}
		}
	}
	if last < 0 {
		// Empty screen: keep a single row so the viewport isn't history-only
		// with no live anchor (quiet space prompt counts as blank).
		return 1
	}
	return last + 1
}

func snapshotLiveCells(term vt10x.Terminal) [][]cellPix {
	live := snapshotScreenCells(term)
	n := liveExtent(term)
	if n > len(live) {
		n = len(live)
	}
	return live[:n]
}

func snapshotLiveText(term vt10x.Terminal) []string {
	live := snapshotScreenText(term)
	n := liveExtent(term)
	if n > len(live) {
		n = len(live)
	}
	return live[:n]
}

func (s *scrollback) noteScreen(term vt10x.Terminal) {
	cur := snapshotScreenText(term)
	if len(s.prev) == len(cur) && len(cur) > 1 {
		// Full clear (clear / Clear-Host): previous live content becomes history
		// and pin stick-bottom so pre-clear history stays above (scroll up).
		if screenWasCleared(s.prev, cur) {
			for _, line := range s.prev {
				if strings.TrimSpace(line) != "" {
					s.push(line)
				}
			}
			// Floor for stick-bottom: nothing before this index until user scrolls up.
			s.pin = len(s.lines)
			s.stickBottom()
		} else if n := scrollAmount(s.prev, cur); n > 0 {
			// Multi-line scroll: find largest n where prev[n:] == cur[:len-n]
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

// screenWasCleared is true when a non-empty screen becomes effectively blank
// (CSI 2J / clear). Distinguishes from a normal scroll.
func screenWasCleared(prev, cur []string) bool {
	if len(prev) != len(cur) || len(cur) == 0 {
		return false
	}
	prevHad := false
	for _, line := range prev {
		if strings.TrimSpace(line) != "" {
			prevHad = true
			break
		}
	}
	if !prevHad {
		return false
	}
	for _, line := range cur {
		if strings.TrimSpace(line) != "" {
			return false
		}
	}
	return true
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
		if s.pin > 0 {
			s.pin -= drop
			if s.pin < 0 {
				s.pin = 0
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

// historyTail returns the last n history lines for diagnostics.
func (s *scrollback) historyTail(n int) []histLine {
	if n < 1 || len(s.lines) == 0 {
		return nil
	}
	if n > len(s.lines) {
		n = len(s.lines)
	}
	out := make([]histLine, n)
	copy(out, s.lines[len(s.lines)-n:])
	return out
}

// recentBlocks returns recent host-injected command block texts (newest last).
func (s *scrollback) recentBlocks(max int) []string {
	if max < 1 {
		return nil
	}
	var cmds []string
	var cur []string
	flush := func() {
		if len(cur) == 0 {
			return
		}
		cmds = append(cmds, strings.Join(cur, "\n"))
		cur = nil
	}
	for _, hl := range s.lines {
		switch hl.kind {
		case histBlockCmd:
			// Strip leading prompt on first line of a block.
			t := hl.text
			if strings.HasPrefix(t, inputBarPrompt) {
				t = strings.TrimPrefix(t, inputBarPrompt)
			} else {
				t = strings.TrimLeft(t, " ")
			}
			cur = append(cur, t)
		case histBlockRule:
			flush()
		default:
			if len(cur) > 0 {
				flush()
			}
		}
	}
	flush()
	if len(cmds) > max {
		cmds = cmds[len(cmds)-max:]
	}
	return cmds
}

// viewCells builds the viewport with color for live rows and styled history.
// Live rows are clipped to liveExtent so trailing blank PTY cells don't push
// recent history (command blocks) off the bottom of the screen.
//
// Alternate-screen apps own the whole viewport — history is not mixed in.
//
// Stick-bottom (offset==0) respects pin after clear: pre-pin history stays
// above and only appears when the user scrolls up.
func (s *scrollback) viewCells(term vt10x.Terminal, viewportRows int) [][]cellPix {
	cols, _ := term.Size()
	if cols < 1 {
		cols = 1
	}
	if viewportRows < 1 {
		viewportRows = 1
	}

	blankRow := func() []cellPix {
		row := make([]cellPix, cols)
		for x := range row {
			row[x] = cellPix{Ch: ' ', FR: 220, FG: 220, FB: 220}
		}
		return row
	}

	if term.Mode()&vt10x.ModeAltScreen != 0 {
		live := snapshotScreenCells(term)
		out := make([][]cellPix, viewportRows)
		for i := 0; i < viewportRows; i++ {
			row := blankRow()
			if i < len(live) {
				copy(row, live[i])
			}
			out[i] = row
		}
		return out
	}

	live := snapshotLiveCells(term)
	histN := len(s.lines)
	docLen := histN + len(live)

	out := make([][]cellPix, viewportRows)

	// Stick-bottom with pin + short post-clear content: bottom-align so the
	// pane stays empty above (history hidden) rather than pulling old lines in.
	if s.offset == 0 && s.pin > 0 && s.pin <= histN {
		contentLen := docLen - s.pin // history after pin + live
		if contentLen < 0 {
			contentLen = 0
		}
		if contentLen <= viewportRows {
			pad := viewportRows - contentLen
			for i := 0; i < viewportRows; i++ {
				if i < pad {
					out[i] = blankRow()
					continue
				}
				idx := s.pin + (i - pad)
				out[i] = s.rowAt(idx, histN, live, cols)
			}
			return out
		}
	}

	start := docLen - viewportRows - s.offset
	if start < 0 {
		start = 0
	}
	// At stick-bottom, never start before pin (hide pre-clear history).
	if s.offset == 0 && s.pin > 0 && start < s.pin {
		start = s.pin
	}

	for i := 0; i < viewportRows; i++ {
		out[i] = s.rowAt(start+i, histN, live, cols)
	}
	return out
}

func (s *scrollback) rowAt(idx, histN int, live [][]cellPix, cols int) []cellPix {
	row := make([]cellPix, cols)
	for x := range row {
		row[x] = cellPix{Ch: ' ', FR: 220, FG: 220, FB: 220}
	}
	if idx < 0 {
		return row
	}
	if idx < histN {
		hl := s.lines[idx]
		rs := []rune(hl.text)
		fr, fg, fb := histColor(hl.kind)
		for x := 0; x < cols && x < len(rs); x++ {
			row[x] = cellPix{Ch: rs[x], FR: fr, FG: fg, FB: fb}
		}
		return row
	}
	li := idx - histN
	if li >= 0 && li < len(live) {
		copy(row, live[li])
	}
	return row
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

// absLine maps a viewport row to a document line index. liveRows should be the
// effective live height (liveExtent), not the full PTY row count.
func (s *scrollback) absLine(viewY, viewportRows, liveRows int) int {
	if liveRows < 1 {
		liveRows = 1
	}
	histN := len(s.lines)
	docLen := histN + liveRows

	if s.offset == 0 && s.pin > 0 && s.pin <= histN {
		contentLen := docLen - s.pin
		if contentLen < 0 {
			contentLen = 0
		}
		if contentLen <= viewportRows {
			pad := viewportRows - contentLen
			if viewY < pad {
				return s.pin // blank pad maps to pin
			}
			return s.pin + (viewY - pad)
		}
	}

	start := docLen - viewportRows - s.offset
	if start < 0 {
		start = 0
	}
	if s.offset == 0 && s.pin > 0 && start < s.pin {
		start = s.pin
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
	live := snapshotLiveText(term)
	i := abs - len(s.lines)
	if i >= 0 && i < len(live) {
		return strings.TrimRight(live[i], " ")
	}
	return ""
}
