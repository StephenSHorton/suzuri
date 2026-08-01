package ui

// inputBar is the Warp-style fixed bottom command line: local edit, Enter → PTY.
type inputBar struct {
	runes   []rune
	cursor  int // 0..len(runes)
	history []string
	histIdx int    // -1 = editing live buffer; else index into history
	draft   string // saved live line when browsing history
}

const (
	inputBarRows   = 2 // separator + content row (in cell heights)
	inputBarPrompt = "❯ "
	maxInputHist   = 200
)

func (b *inputBar) text() string {
	return string(b.runes)
}

func (b *inputBar) clear() {
	b.runes = b.runes[:0]
	b.cursor = 0
	b.histIdx = -1
	b.draft = ""
}

func (b *inputBar) insertRunes(rs []rune) {
	if len(rs) == 0 {
		return
	}
	// Flatten newlines for single-line bar.
	clean := make([]rune, 0, len(rs))
	for _, r := range rs {
		switch r {
		case '\r', '\n':
			if len(clean) > 0 && clean[len(clean)-1] != ' ' {
				clean = append(clean, ' ')
			}
		case '\t':
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
	if b.cursor < 0 {
		b.cursor = 0
	}
	if b.cursor > len(b.runes) {
		b.cursor = len(b.runes)
	}
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

func (b *inputBar) leaveHistoryBrowse() {
	if b.histIdx >= 0 {
		b.histIdx = -1
		b.draft = ""
	}
}

func (b *inputBar) backspace() {
	b.leaveHistoryBrowse()
	if b.cursor <= 0 {
		return
	}
	b.runes = append(b.runes[:b.cursor-1], b.runes[b.cursor:]...)
	b.cursor--
}

func (b *inputBar) deleteForward() {
	b.leaveHistoryBrowse()
	if b.cursor >= len(b.runes) {
		return
	}
	b.runes = append(b.runes[:b.cursor], b.runes[b.cursor+1:]...)
}

func (b *inputBar) moveLeft() {
	if b.cursor > 0 {
		b.cursor--
	}
}

func (b *inputBar) moveRight() {
	if b.cursor < len(b.runes) {
		b.cursor++
	}
}

func (b *inputBar) moveHome() {
	b.cursor = 0
}

func (b *inputBar) moveEnd() {
	b.cursor = len(b.runes)
}

// historyUp walks toward older commands.
func (b *inputBar) historyUp() {
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

// submit returns the line to send to the PTY (without CR) and clears the bar.
// Empty lines still submit (shell gets a bare Enter).
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

// viewSlice returns the visible portion of the line for a content width of
// contentCols cells (excluding the prompt), and the caret column within that
// slice (0..contentCols).
func (b *inputBar) viewSlice(contentCols int) (visible string, caretCol int) {
	if contentCols < 1 {
		contentCols = 1
	}
	// Horizontal scroll so the cursor stays in view.
	start := 0
	if b.cursor > contentCols {
		start = b.cursor - contentCols
	}
	// Prefer keeping some left context when possible.
	if b.cursor-start > contentCols {
		start = b.cursor - contentCols
	}
	end := start + contentCols
	if end > len(b.runes) {
		end = len(b.runes)
	}
	if start > end {
		start = end
	}
	visible = string(b.runes[start:end])
	caretCol = b.cursor - start
	if caretCol < 0 {
		caretCol = 0
	}
	if caretCol > contentCols {
		caretCol = contentCols
	}
	return visible, caretCol
}

func stringsTrimSpace(s string) string {
	i, j := 0, len(s)
	for i < j && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	for j > i && (s[j-1] == ' ' || s[j-1] == '\t') {
		j--
	}
	return s[i:j]
}
