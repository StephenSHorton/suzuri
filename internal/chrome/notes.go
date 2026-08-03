package chrome

import (
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
)

// Scratch notes: one in-memory buffer, floating card like the palette.
// Esc / toggle closes the card but keeps text (no disk yet).

const (
	notesVisRows   = 12
	notesMaxRunes  = 64 * 1024
	notesCaretChar = "▌"
)

// OpenNotesMsg opens the notes surface (buffer preserved across open/close).
type OpenNotesMsg struct{}

// ToggleNotesMsg opens notes if closed, closes if open (text kept).
type ToggleNotesMsg struct{}

func (m *Model) openNotes() {
	m.closeModalsExcept("notes")
	m.NotesOpen = true
	if m.notesCursor < 0 {
		m.notesCursor = 0
	}
	if m.notesCursor > len(m.notesRunes) {
		m.notesCursor = len(m.notesRunes)
	}
}

func (m *Model) toggleNotes() {
	if m.NotesOpen {
		m.NotesOpen = false
		return
	}
	m.openNotes()
}

func (m *Model) handleNotesKey(msg tea.KeyMsg) {
	switch msg.String() {
	case "esc":
		// Put away — keep buffer.
		m.NotesOpen = false
	case "ctrl+c":
		// Don't clear notes; just close (same as esc). Ctrl+A/C copy later.
		m.NotesOpen = false
	case "enter", "ctrl+m":
		m.notesInsert("\n")
	case "backspace":
		m.notesBackspace()
	case "delete", "ctrl+d":
		m.notesDelete()
	case "left":
		m.notesMove(-1)
	case "right":
		m.notesMove(1)
	case "up":
		m.notesMoveVert(-1)
	case "down":
		m.notesMoveVert(1)
	case "home", "ctrl+a":
		m.notesHome()
	case "end", "ctrl+e":
		m.notesEnd()
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			var b strings.Builder
			for _, r := range msg.Runes {
				if r >= 32 || r == '\t' {
					if r == '\t' {
						b.WriteString("  ")
					} else {
						b.WriteRune(r)
					}
				}
			}
			if b.Len() > 0 {
				m.notesInsert(b.String())
			}
		}
	}
}

func (m *Model) notesInsert(s string) {
	if s == "" {
		return
	}
	rs := []rune(s)
	// Cap total size.
	if len(m.notesRunes)+len(rs) > notesMaxRunes {
		rs = rs[:notesMaxRunes-len(m.notesRunes)]
		if len(rs) == 0 {
			return
		}
	}
	cur := m.notesCursor
	if cur < 0 {
		cur = 0
	}
	if cur > len(m.notesRunes) {
		cur = len(m.notesRunes)
	}
	out := make([]rune, 0, len(m.notesRunes)+len(rs))
	out = append(out, m.notesRunes[:cur]...)
	out = append(out, rs...)
	out = append(out, m.notesRunes[cur:]...)
	m.notesRunes = out
	m.notesCursor = cur + len(rs)
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesBackspace() {
	if m.notesCursor <= 0 {
		return
	}
	i := m.notesCursor
	m.notesRunes = append(m.notesRunes[:i-1], m.notesRunes[i:]...)
	m.notesCursor = i - 1
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesDelete() {
	if m.notesCursor >= len(m.notesRunes) {
		return
	}
	i := m.notesCursor
	m.notesRunes = append(m.notesRunes[:i], m.notesRunes[i+1:]...)
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesMove(delta int) {
	m.notesCursor += delta
	if m.notesCursor < 0 {
		m.notesCursor = 0
	}
	if m.notesCursor > len(m.notesRunes) {
		m.notesCursor = len(m.notesRunes)
	}
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesHome() {
	// Start of current hard line.
	i := m.notesCursor
	for i > 0 && m.notesRunes[i-1] != '\n' {
		i--
	}
	m.notesCursor = i
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesEnd() {
	i := m.notesCursor
	for i < len(m.notesRunes) && m.notesRunes[i] != '\n' {
		i++
	}
	m.notesCursor = i
	m.notesEnsureCursorVisible(0)
}

// notesMoveVert moves by soft-wrapped visual row (width estimated later in render).
// Uses a reasonable default width; host re-render always uses real width.
func (m *Model) notesMoveVert(dir int) {
	w := m.notesWrapW
	if w < 8 {
		w = 40
	}
	lines := notesSoftLines(m.notesRunes, w)
	row, col := notesCursorRowCol(lines, m.notesCursor)
	row += dir
	if row < 0 {
		row = 0
		col = 0
	}
	if row >= len(lines) {
		m.notesCursor = len(m.notesRunes)
		m.notesEnsureCursorVisible(w)
		return
	}
	ln := lines[row]
	if col > ln.width {
		col = ln.width
	}
	m.notesCursor = ln.start + col
	if m.notesCursor > ln.end {
		m.notesCursor = ln.end
	}
	// Don't land past a trailing soft-wrap boundary into next line's start incorrectly.
	if m.notesCursor > len(m.notesRunes) {
		m.notesCursor = len(m.notesRunes)
	}
	m.notesEnsureCursorVisible(w)
}

func (m *Model) notesEnsureCursorVisible(wrapW int) {
	w := wrapW
	if w < 8 {
		w = m.notesWrapW
	}
	if w < 8 {
		w = 40
	}
	lines := notesSoftLines(m.notesRunes, w)
	row, _ := notesCursorRowCol(lines, m.notesCursor)
	if row < m.notesScroll {
		m.notesScroll = row
	}
	if row >= m.notesScroll+notesVisRows {
		m.notesScroll = row - notesVisRows + 1
	}
	if m.notesScroll < 0 {
		m.notesScroll = 0
	}
}

// NotesPaste inserts clipboard text (host supplies UTF-8).
func (m *Model) NotesPaste(s string) {
	if s == "" {
		return
	}
	// Normalize newlines.
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	m.notesInsert(s)
}

type notesLine struct {
	start, end int // half-open index into runes
	width      int // display columns (end-start, excluding hard \n at end)
	hard       bool
}

func notesSoftLines(runes []rune, width int) []notesLine {
	if width < 4 {
		width = 4
	}
	if len(runes) == 0 {
		return []notesLine{{start: 0, end: 0, width: 0}}
	}
	var out []notesLine
	start := 0
	col := 0
	for i := 0; i < len(runes); i++ {
		if runes[i] == '\n' {
			out = append(out, notesLine{start: start, end: i, width: col, hard: true})
			start = i + 1
			col = 0
			continue
		}
		if col >= width {
			out = append(out, notesLine{start: start, end: i, width: col, hard: false})
			start = i
			col = 0
		}
		col++
	}
	out = append(out, notesLine{start: start, end: len(runes), width: col, hard: false})
	// Trailing hard newline → empty line for caret.
	if runes[len(runes)-1] == '\n' {
		out = append(out, notesLine{start: len(runes), end: len(runes), width: 0, hard: false})
	}
	return out
}

func notesCursorRowCol(lines []notesLine, cursor int) (row, col int) {
	if len(lines) == 0 {
		return 0, 0
	}
	for i, ln := range lines {
		// Cursor at end of buffer: last line.
		if cursor >= ln.start && cursor <= ln.end {
			// Prefer later soft-wrap line when cursor == boundary.
			if cursor == ln.end && !ln.hard && i+1 < len(lines) && lines[i+1].start == ln.end {
				continue
			}
			return i, cursor - ln.start
		}
	}
	last := lines[len(lines)-1]
	return len(lines) - 1, last.width
}

func (m Model) renderNotes(windowCols int) string {
	outer := clampDialogWidth(56, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}
	// Content width inside padding for wrap.
	wrapW := inner - 2
	if wrapW < 8 {
		wrapW = 8
	}

	lines := notesSoftLines(m.notesRunes, wrapW)
	scroll := m.notesScroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > 0 && scroll >= len(lines) {
		scroll = len(lines) - 1
	}
	row, col := notesCursorRowCol(lines, m.notesCursor)

	var body []string
	end := scroll + notesVisRows
	if end > len(lines) {
		end = len(lines)
	}
	for i := scroll; i < end; i++ {
		ln := lines[i]
		seg := string(m.notesRunes[ln.start:ln.end])
		// Insert caret on cursor row.
		if i == row {
			rs := []rune(seg)
			if col > len(rs) {
				col = len(rs)
			}
			if col < 0 {
				col = 0
			}
			seg = string(rs[:col]) + notesCaretChar + string(rs[col:])
		}
		plain := padFit(seg, wrapW)
		body = append(body, styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).Render(plain))
	}
	// Pad empty rows so card height is stable.
	for len(body) < notesVisRows {
		body = append(body, styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).
			Render(padFit("", wrapW)))
	}
	if len(m.notesRunes) == 0 && row == 0 {
		// Placeholder under caret when empty.
		body[0] = styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).
			Render(padFit(notesCaretChar, wrapW))
		// Hint as second line if empty
		if notesVisRows > 1 {
			body[1] = styleDialogHint().Width(inner).MaxHeight(1).Padding(0, 1).
				Render(padFit("scratch notes — not saved to disk yet", wrapW))
		}
	}
	footer := styleDialogHintKey().Render("esc") +
		styleDialogHint().Render(" put away  ") +
		styleDialogHintKey().Render("enter") +
		styleDialogHint().Render(" newline  ") +
		styleDialogHintKey().Render("ctrl+shift+m") +
		styleDialogHint().Render(" toggle")
	return renderDialogCard(outer, "Notes", body, footer)
}

// notesRuneCount is for tests/debug.
func notesRuneCount(s string) int { return utf8.RuneCountInString(s) }
