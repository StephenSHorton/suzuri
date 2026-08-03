package ui

// inputBar is the Warp-style command line: local edit, Enter → PTY.
// Supports soft-wrap + explicit newlines (Shift+Enter); history is per-bar
// (each pane owns its own inputBar). When split, each leaf paints its bar
// at the bottom of that pane.
type inputBar struct {
	runes   []rune
	cursor  int // 0..len(runes)
	history []string
	histIdx int    // -1 = editing live buffer; else index into history
	draft   string // saved live line when browsing history
	comp    completeSession
	// ghostKey/ghostCache avoid ReadDir on every paint/caret blink.
	ghostKey   string
	ghostCache string
}

const (
	inputBarPrompt       = "❯ "
	maxInputHist         = 200
	maxInputVisualRows   = 8
	minInputContentWidth = 8
	// Horizontal pad inside a pane bar (left + right).
	inputBarPadX = 8
)

// inputBarVPads returns hairline, top content inset, and bottom inset (symmetric).
func inputBarVPads(ch int32) (hair, topPad, botPad int32) {
	hair = ch / 10
	if hair < 1 {
		hair = 1
	}
	topPad = ch / 5
	if topPad < 2 {
		topPad = 2
	}
	botPad = topPad
	return hair, topPad, botPad
}

// paneInputContentCols is wrap width for a bar of the given pixel width.
func paneInputContentCols(paneW, cw int32) int {
	if cw < 1 {
		cw = cellW
	}
	promptW := int32(len([]rune(inputBarPrompt))) * cw
	cols := int((paneW - inputBarPadX - promptW - inputBarPadX) / cw)
	if cols < minInputContentWidth {
		cols = minInputContentWidth
	}
	return cols
}

// paneInputBarPixelHeight is the bar height for one pane (0 on alt-screen).
// ch/cw are cell metrics; paneW is the leaf width in pixels.
func paneInputBarPixelHeight(t *tab, paneW, cw, ch int32) int32 {
	if t == nil || t.altScreen() {
		return 0
	}
	if ch < 1 {
		ch = cellH
	}
	if cw < 1 {
		cw = cellW
	}
	rows := t.input.visualRows(paneInputContentCols(paneW, cw))
	if rows < 1 {
		rows = 1
	}
	cwdRows := int32(0)
	if displayPath(t.cwd) != "" {
		cwdRows = 1
	}
	hair, topPad, botPad := inputBarVPads(ch)
	return hair + topPad + cwdRows*ch + int32(rows)*ch + botPad
}

func (b *inputBar) text() string {
	return string(b.runes)
}

func (b *inputBar) clear() {
	b.runes = b.runes[:0]
	b.cursor = 0
	b.histIdx = -1
	b.draft = ""
	b.clearComplete()
}

func (b *inputBar) insertRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	// Keep newlines (multiline). Flatten CR; expand tabs.
	clean := make([]rune, 0, len(rs))
	for _, r := range rs {
		switch r {
		case '\r':
			// ignore CR; \n is the line break
		case '\n':
			clean = append(clean, '\n')
		case '\t':
			// Tab is completion at the host; pasted tabs become spaces.
			clean = append(clean, ' ', ' ')
		default:
			if r >= 32 {
				clean = append(clean, r)
			}
		}
	}
	if len(clean) == 0 {
		return
	}
	b.leaveHistoryBrowse()
	b.clearComplete()
	b.clampCursor()
	out := make([]rune, 0, len(b.runes)+len(clean))
	out = append(out, b.runes[:b.cursor]...)
	out = append(out, clean...)
	out = append(out, b.runes[b.cursor:]...)
	b.runes = out
	b.cursor += len(clean)
}

func (b *inputBar) insertRune(r rune) {
	b.insertRunes([]rune{r})
}

func (b *inputBar) insertNewline() {
	b.insertRune('\n')
}

func (b *inputBar) leaveHistoryBrowse() {
	if b.histIdx >= 0 {
		b.histIdx = -1
		b.draft = ""
	}
}

func (b *inputBar) clampCursor() {
	if b.cursor < 0 {
		b.cursor = 0
	}
	if b.cursor > len(b.runes) {
		b.cursor = len(b.runes)
	}
}

func (b *inputBar) backspace() {
	b.leaveHistoryBrowse()
	b.clearComplete()
	if b.cursor <= 0 {
		return
	}
	b.runes = append(b.runes[:b.cursor-1], b.runes[b.cursor:]...)
	b.cursor--
}

// deleteToLineStart removes text from the start of the current logical line
// through the caret (macOS ⌘⌫). With the caret at EOL this clears the line.
func (b *inputBar) deleteToLineStart() {
	b.leaveHistoryBrowse()
	b.clearComplete()
	b.clampCursor()
	ls := lineStartAt(b.runes, b.cursor)
	if b.cursor <= ls {
		return
	}
	b.runes = append(b.runes[:ls], b.runes[b.cursor:]...)
	b.cursor = ls
}

// clearLine clears the entire buffer (all lines). Prefer deleteToLineStart for ⌘⌫.
func (b *inputBar) clearLine() {
	b.clear()
}

func (b *inputBar) deleteForward() {
	b.leaveHistoryBrowse()
	b.clearComplete()
	if b.cursor >= len(b.runes) {
		return
	}
	b.runes = append(b.runes[:b.cursor], b.runes[b.cursor+1:]...)
}

func (b *inputBar) moveLeft() {
	b.clearComplete()
	if b.cursor > 0 {
		b.cursor--
	}
}

func (b *inputBar) moveRight() {
	b.clearComplete()
	if b.cursor < len(b.runes) {
		b.cursor++
	}
}

// moveWordLeft / moveWordRight: Option+←→ (macOS) or Ctrl+←→ (Windows / also Mac).
func (b *inputBar) moveWordLeft() {
	b.clearComplete()
	b.cursor = barWordBoundary(b.runes, b.cursor, -1)
}

func (b *inputBar) moveWordRight() {
	b.clearComplete()
	b.cursor = barWordBoundary(b.runes, b.cursor, 1)
}

// deleteWordLeft / deleteWordRight: Option/Ctrl+Backspace/Delete.
func (b *inputBar) deleteWordLeft() {
	b.leaveHistoryBrowse()
	b.clearComplete()
	if b.cursor <= 0 {
		return
	}
	start := barWordBoundary(b.runes, b.cursor, -1)
	b.runes = append(b.runes[:start], b.runes[b.cursor:]...)
	b.cursor = start
}

func (b *inputBar) deleteWordRight() {
	b.leaveHistoryBrowse()
	b.clearComplete()
	if b.cursor >= len(b.runes) {
		return
	}
	end := barWordBoundary(b.runes, b.cursor, 1)
	b.runes = append(b.runes[:b.cursor], b.runes[end:]...)
}

func barWordBoundary(runes []rune, i, dir int) int {
	n := len(runes)
	if i < 0 {
		i = 0
	}
	if i > n {
		i = n
	}
	isSpace := func(r rune) bool {
		return r == ' ' || r == '\t' || r == '\n'
	}
	if dir < 0 {
		for i > 0 && isSpace(runes[i-1]) {
			i--
		}
		for i > 0 && !isSpace(runes[i-1]) {
			i--
		}
		return i
	}
	for i < n && !isSpace(runes[i]) {
		i++
	}
	for i < n && isSpace(runes[i]) {
		i++
	}
	return i
}

func (b *inputBar) moveHome() {
	b.clearComplete()
	// Start of current logical line (after last \n), not whole buffer.
	i := b.cursor
	for i > 0 && b.runes[i-1] != '\n' {
		i--
	}
	b.cursor = i
}

// moveDocHome / moveDocEnd: Cmd+↑/↓ (macOS) — whole buffer extremes.
func (b *inputBar) moveDocHome() {
	b.clearComplete()
	b.cursor = 0
}

func (b *inputBar) moveDocEnd() {
	b.clearComplete()
	b.cursor = len(b.runes)
}

func (b *inputBar) moveEnd() {
	b.clearComplete()
	i := b.cursor
	for i < len(b.runes) && b.runes[i] != '\n' {
		i++
	}
	b.cursor = i
}

// historyUp walks toward older commands.
func (b *inputBar) historyUp() {
	b.clearComplete()
	if len(b.history) == 0 {
		return
	}
	if b.histIdx < 0 {
		b.draft = string(b.runes)
		b.histIdx = len(b.history) - 1
	} else if b.histIdx > 0 {
		b.histIdx--
	}
	b.applyHistory()
}

// historyDown walks toward newer / draft.
func (b *inputBar) historyDown() {
	b.clearComplete()
	if b.histIdx < 0 {
		return
	}
	if b.histIdx+1 >= len(b.history) {
		b.histIdx = -1
		b.runes = []rune(b.draft)
		b.cursor = len(b.runes)
		b.draft = ""
		return
	}
	b.histIdx++
	b.applyHistory()
}

func (b *inputBar) applyHistory() {
	if b.histIdx < 0 || b.histIdx >= len(b.history) {
		return
	}
	b.runes = []rune(b.history[b.histIdx])
	b.cursor = len(b.runes)
}

// submit returns the line to send to the PTY (without trailing CR) and clears.
// Empty lines still submit (shell gets a bare Enter). Newlines are kept so
// multi-line scripts go through as-is.
func (b *inputBar) submit() string {
	line := string(b.runes)
	if trimmed := stringsTrimSpace(line); trimmed != "" {
		if n := len(b.history); n == 0 || b.history[n-1] != line {
			b.history = append(b.history, line)
			if len(b.history) > maxInputHist {
				b.history = b.history[len(b.history)-maxInputHist:]
			}
		}
	}
	b.clear()
	return line
}

// wrapLayout breaks the buffer into display rows of at most width columns.
// Explicit '\n' always breaks. Returns display lines (without the '\n' char)
// and the caret row/col within that layout.
func (b *inputBar) wrapLayout(width int) (lines []string, caretRow, caretCol int) {
	if width < 1 {
		width = 1
	}
	type pos struct{ row, col int }
	// Map each rune index → display position; also index==len for caret at end.
	at := make([]pos, len(b.runes)+1)
	var (
		row, col int
		cur      []rune
	)
	flush := func() {
		lines = append(lines, string(cur))
		cur = cur[:0]
		row++
		col = 0
	}
	at[0] = pos{0, 0}
	for i, r := range b.runes {
		if r == '\n' {
			flush()
			at[i+1] = pos{row, col}
			continue
		}
		if col >= width {
			flush()
		}
		cur = append(cur, r)
		col++
		at[i+1] = pos{row, col}
	}
	// Trailing partial line (or empty buffer → one empty line).
	if len(cur) > 0 || len(lines) == 0 || (len(b.runes) > 0 && b.runes[len(b.runes)-1] == '\n') {
		lines = append(lines, string(cur))
	}
	if len(lines) == 0 {
		lines = []string{""}
	}
	b.clampCursor()
	p := at[b.cursor]
	// If caret maps past last flushed empty after final \n, clamp.
	if p.row >= len(lines) {
		p.row = len(lines) - 1
		p.col = len([]rune(lines[p.row]))
	}
	return lines, p.row, p.col
}

// visualRows is how many content rows the bar needs (capped).
func (b *inputBar) visualRows(width int) int {
	lines, _, _ := b.wrapLayout(width)
	n := len(lines)
	if n < 1 {
		n = 1
	}
	if n > maxInputVisualRows {
		n = maxInputVisualRows
	}
	return n
}

// moveVisualUp moves the caret one display row up. Returns false if already
// on the first row (caller may walk history).
func (b *inputBar) moveVisualUp(width int) bool {
	b.clearComplete()
	_, cr, cc := b.wrapLayout(width)
	if cr <= 0 {
		return false
	}
	b.cursor = indexFromLayout(b.runes, width, cr-1, cc)
	return true
}

// moveVisualDown moves one display row down. Returns false if on last row.
func (b *inputBar) moveVisualDown(width int) bool {
	b.clearComplete()
	lines, cr, cc := b.wrapLayout(width)
	if cr >= len(lines)-1 {
		return false
	}
	b.cursor = indexFromLayout(b.runes, width, cr+1, cc)
	return true
}

// indexFromLayout finds the rune index for a display (row, col).
func indexFromLayout(runes []rune, width, wantRow, wantCol int) int {
	if width < 1 {
		width = 1
	}
	row, col := 0, 0
	for i, r := range runes {
		if row == wantRow && col >= wantCol {
			return i
		}
		if r == '\n' {
			if row == wantRow {
				return i // end of that line
			}
			row++
			col = 0
			continue
		}
		if col >= width {
			row++
			col = 0
			if row == wantRow && col >= wantCol {
				return i
			}
		}
		col++
		if row == wantRow && col > wantCol {
			// landed past wantCol on this rune
			return i + 1
		}
	}
	// End of buffer / last line.
	return len(runes)
}

// visibleWindow returns a window of display lines when content exceeds max
// visual rows, keeping the caret row in view. Also returns caret row/col
// relative to the returned window.
func (b *inputBar) visibleWindow(width, maxRows int) (view []string, caretRow, caretCol int) {
	lines, cr, cc := b.wrapLayout(width)
	if maxRows < 1 {
		maxRows = 1
	}
	if len(lines) <= maxRows {
		return lines, cr, cc
	}
	start := cr - maxRows + 1
	if start < 0 {
		start = 0
	}
	end := start + maxRows
	if end > len(lines) {
		end = len(lines)
		start = end - maxRows
		if start < 0 {
			start = 0
		}
	}
	view = lines[start:end]
	return view, cr - start, cc
}

func stringsTrimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t' || s[i] == '\n' || s[i] == '\r') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t' || s[j-1] == '\n' || s[j-1] == '\r') {
		j--
	}
	return s[i:j]
}
