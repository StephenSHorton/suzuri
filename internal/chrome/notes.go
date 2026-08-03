package chrome

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Notes: two screens — full list, then full editor (Esc back to list).

const (
	notesListRows   = 12 // rows in list browser OR editor body
	notesMaxRunes   = 64 * 1024
	notesCaretChar  = "▌"
	notesTabStop    = 4
	notesMaxBank    = 48
	notesDialogWant = 56
)

// notesFocus is which screen owns keys.
type notesFocus int

const (
	notesFocusList notesFocus = iota // full-screen note browser
	notesFocusEditor                 // full-screen body editor
	notesFocusTitle                  // rename on the editor screen
)

// notesLayout describes the last-rendered card for host click hit-testing
// (cell coordinates relative to the full-width overlay grid).
type notesLayout struct {
	Cols      int
	Outer     int
	Inner     int
	// Overlay rows (0-based in overlayCells):
	TitleY    int // dialog title
	NameY     int // editor: note name (click to rename); -1 on list
	BodyY0    int // first list item or editor line
	BodyRows  int
	CardLeft  int
	CardRight int
	// Mode matches notesFocus at last layout compute (list vs editor/title).
	ListMode  bool
	Valid     bool
}

// OpenNotesMsg opens the notes surface (bank preserved across open/close).
type OpenNotesMsg struct{}

// ToggleNotesMsg opens notes if closed, closes if open (text kept / flushed).
type ToggleNotesMsg struct{}

// LoadNotesMsg replaces the in-memory bank (startup load from disk).
type LoadNotesMsg struct {
	Bank NotesBank
}

// NotesDeleteMsg deletes the active note (or clears the last one).
type NotesDeleteMsg struct{}

// NotesClickMsg is a host mouse click on the notes overlay grid (cell coords).
type NotesClickMsg struct {
	CellX int
	CellY int
	Cols  int
}

func (m *Model) initNotesBank(bank NotesBank) {
	bank = normalizeNotesBank(bank)
	m.notesBank = append([]NoteDoc(nil), bank.Notes...)
	m.notesActive = 0
	for i, n := range m.notesBank {
		if n.ID == bank.ActiveID {
			m.notesActive = i
			break
		}
	}
	m.notesFocus = notesFocusList
	m.notesTitle = ""
	m.loadActiveNoteIntoEditor()
}

func (m *Model) loadActiveNoteIntoEditor() {
	if len(m.notesBank) == 0 {
		m.notesBank = []NoteDoc{newNoteDoc("Scratch", "")}
		m.notesActive = 0
	}
	if m.notesActive < 0 || m.notesActive >= len(m.notesBank) {
		m.notesActive = 0
	}
	m.notesRunes = []rune(m.notesBank[m.notesActive].Body)
	m.notesCursor = len(m.notesRunes)
	m.notesSel = -1
	m.notesScroll = 0
}

// flushActiveNote writes the editor buffer into notesBank[active].
func (m *Model) flushActiveNote() {
	if len(m.notesBank) == 0 {
		return
	}
	if m.notesActive < 0 || m.notesActive >= len(m.notesBank) {
		m.notesActive = 0
	}
	body := string(m.notesRunes)
	n := m.notesBank[m.notesActive]
	if n.Body != body {
		n.Body = body
		n.Updated = timeNow()
		m.notesDirty = true
	}
	m.notesBank[m.notesActive] = n
}

func (m *Model) putAwayNotes() {
	if m.notesFocus == notesFocusTitle {
		m.commitNotesTitle(false)
	}
	m.flushActiveNote()
	m.NotesOpen = false
	m.notesFocus = notesFocusList
}

// NotesSnapshot returns the bank for disk save (flushes editor first).
func (m *Model) NotesSnapshot() NotesBank {
	if m.notesFocus == notesFocusTitle {
		m.commitNotesTitle(true)
	}
	m.flushActiveNote()
	activeID := ""
	if len(m.notesBank) > 0 && m.notesActive >= 0 && m.notesActive < len(m.notesBank) {
		activeID = m.notesBank[m.notesActive].ID
	}
	return NotesBank{
		ActiveID: activeID,
		Notes:    append([]NoteDoc(nil), m.notesBank...),
	}
}

// NotesDirty reports whether the bank needs a disk write.
func (m Model) NotesDirty() bool { return m.notesDirty }

// ClearNotesDirty after a successful SaveNotesBank.
func (m *Model) ClearNotesDirty() { m.notesDirty = false }

// MarkNotesDirty is used when host loads fail or external edits apply.
func (m *Model) MarkNotesDirty() { m.notesDirty = true }

func (m *Model) openNotes() {
	m.closeModalsExcept("notes")
	m.NotesOpen = true
	if len(m.notesBank) == 0 {
		m.initNotesBank(defaultNotesBank())
	}
	m.notesFocus = notesFocusList
	m.notesTitle = ""
	m.notesClampCursor()
	if m.notesSel < 0 {
		m.notesSel = -1
	}
}

func (m *Model) toggleNotes() {
	if m.NotesOpen {
		m.putAwayNotes()
		return
	}
	m.openNotes()
}

func (m *Model) notesSelect(i int) {
	if i < 0 || i >= len(m.notesBank) {
		return
	}
	if i == m.notesActive {
		return
	}
	m.flushActiveNote()
	m.notesActive = i
	m.loadActiveNoteIntoEditor()
	m.notesDirty = true
}

func (m *Model) notesNew() {
	if len(m.notesBank) >= notesMaxBank {
		return
	}
	if m.notesFocus == notesFocusTitle {
		m.commitNotesTitle(true)
	}
	m.flushActiveNote()
	n := newNoteDoc("", "")
	m.notesBank = append(m.notesBank, n)
	m.notesActive = len(m.notesBank) - 1
	m.loadActiveNoteIntoEditor()
	m.notesDirty = true
	m.beginNotesTitleEdit()
}

func (m *Model) notesDeleteActive() {
	if m.notesFocus == notesFocusTitle {
		m.commitNotesTitle(false)
	}
	if len(m.notesBank) <= 1 {
		m.notesRunes = nil
		m.notesCursor = 0
		m.notesSel = -1
		m.notesScroll = 0
		if len(m.notesBank) == 1 {
			m.notesBank[0].Title = ""
			m.notesBank[0].Body = ""
			m.notesBank[0].Updated = timeNow()
		}
		m.notesDirty = true
		return
	}
	i := m.notesActive
	m.notesBank = append(m.notesBank[:i], m.notesBank[i+1:]...)
	if m.notesActive >= len(m.notesBank) {
		m.notesActive = len(m.notesBank) - 1
	}
	m.loadActiveNoteIntoEditor()
	m.notesDirty = true
	m.notesFocus = notesFocusList
}

func (m *Model) beginNotesTitleEdit() {
	m.flushActiveNote()
	if len(m.notesBank) == 0 {
		return
	}
	n := m.notesBank[m.notesActive]
	// Seed with stored title, else derived display (so rename is natural).
	if t := strings.TrimSpace(n.Title); t != "" {
		m.notesTitle = t
	} else {
		m.notesTitle = NoteDisplayTitle(n)
		if m.notesTitle == "Untitled" {
			m.notesTitle = ""
		}
	}
	m.notesFocus = notesFocusTitle
}

func (m *Model) commitNotesTitle(keep bool) {
	if m.notesFocus != notesFocusTitle {
		return
	}
	if keep && len(m.notesBank) > 0 {
		t := strings.TrimSpace(m.notesTitle)
		n := m.notesBank[m.notesActive]
		if n.Title != t {
			n.Title = t
			n.Updated = timeNow()
			m.notesBank[m.notesActive] = n
			m.notesDirty = true
		}
	}
	m.notesTitle = ""
	m.notesFocus = notesFocusList
}

// timeNow is a seam for tests.
var timeNow = func() time.Time { return time.Now() }

func (m *Model) notesClampCursor() {
	if m.notesCursor < 0 {
		m.notesCursor = 0
	}
	if m.notesCursor > len(m.notesRunes) {
		m.notesCursor = len(m.notesRunes)
	}
	if m.notesSel > len(m.notesRunes) {
		m.notesSel = len(m.notesRunes)
	}
}

func (m *Model) notesHasSel() bool {
	return m.notesSel >= 0 && m.notesSel != m.notesCursor
}

func (m *Model) notesSelRange() (lo, hi int) {
	if !m.notesHasSel() {
		m.notesClampCursor()
		return m.notesCursor, m.notesCursor
	}
	a, b := m.notesSel, m.notesCursor
	if a > b {
		a, b = b, a
	}
	if a < 0 {
		a = 0
	}
	if b > len(m.notesRunes) {
		b = len(m.notesRunes)
	}
	return a, b
}

func (m *Model) notesClearSel() {
	m.notesSel = -1
}

// notesDeleteSel removes the selection if any. Returns true if it did.
func (m *Model) notesDeleteSel() bool {
	if !m.notesHasSel() {
		return false
	}
	lo, hi := m.notesSelRange()
	m.notesRunes = append(m.notesRunes[:lo], m.notesRunes[hi:]...)
	m.notesCursor = lo
	m.notesSel = -1
	m.notesDirty = true
	m.notesEnsureCursorVisible(0)
	return true
}

// NotesSelectedText returns the selected text (empty if no selection).
func (m Model) NotesSelectedText() string {
	if m.notesSel < 0 || m.notesSel == m.notesCursor {
		return ""
	}
	a, b := m.notesSel, m.notesCursor
	if a > b {
		a, b = b, a
	}
	if a < 0 {
		a = 0
	}
	if b > len(m.notesRunes) {
		b = len(m.notesRunes)
	}
	return string(m.notesRunes[a:b])
}

// NotesAllText returns the full notes buffer.
func (m Model) NotesAllText() string {
	return string(m.notesRunes)
}

func (m *Model) handleNotesKey(msg tea.KeyMsg) {
	s := msg.String()

	// Title rename mode (inline, like rename dialog).
	if m.notesFocus == notesFocusTitle {
		switch s {
		case "esc":
			m.commitNotesTitle(false)
			return
		case "enter":
			m.commitNotesTitle(true)
			// Stay on editor screen after naming (new note flow).
			m.notesFocus = notesFocusEditor
			return
		case "backspace":
			if m.notesTitle != "" {
				rs := []rune(m.notesTitle)
				m.notesTitle = string(rs[:len(rs)-1])
			}
			return
		default:
			if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
				for _, r := range msg.Runes {
					if r >= 32 {
						m.notesTitle += string(r)
					}
				}
			}
			return
		}
	}

	// List pane: browse bank, delete, open editor.
	if m.notesFocus == notesFocusList {
		switch s {
		case "esc":
			m.putAwayNotes()
			return
		case "up":
			if m.notesActive > 0 {
				m.notesSelect(m.notesActive - 1)
			}
			return
		case "down":
			if m.notesActive+1 < len(m.notesBank) {
				m.notesSelect(m.notesActive + 1)
			}
			return
		case "enter", "right", "tab":
			m.notesFocus = notesFocusEditor
			return
		case "n", "ctrl+n":
			m.notesNew()
			return
		case "d", "delete", "backspace":
			m.notesDeleteActive()
			return
		case "f2", "r":
			m.beginNotesTitleEdit()
			return
		case "ctrl+c":
			return
		default:
			// Typing in list focuses editor and inserts.
			if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
				m.notesFocus = notesFocusEditor
				m.handleNotesEditorKey(msg)
			}
			return
		}
	}

	// Editor pane.
	m.handleNotesEditorKey(msg)
}

func (m *Model) handleNotesEditorKey(msg tea.KeyMsg) {
	s := msg.String()
	extend := strings.HasPrefix(s, "shift+")

	switch s {
	case "esc":
		// Back to list (not close) — list is the "system" for notes.
		m.notesFocus = notesFocusList
		m.notesClearSel()
		return
	case "f2":
		m.beginNotesTitleEdit()
		return
	case "ctrl+n":
		m.notesNew()
		return
	case "ctrl+a":
		if len(m.notesRunes) == 0 {
			m.notesSel = -1
			m.notesCursor = 0
			return
		}
		m.notesSel = 0
		m.notesCursor = len(m.notesRunes)
		m.notesEnsureCursorVisible(0)
		return
	case "ctrl+c":
		return
	case "ctrl+x":
		if m.notesHasSel() {
			m.notesDeleteSel()
		}
		return
	case "enter", "ctrl+m":
		m.notesInsert("\n")
		return
	case "tab", "shift+tab":
		m.notesInsert("\t")
		return
	case "backspace":
		if m.notesDeleteSel() {
			return
		}
		m.notesBackspace()
		return
	case "delete", "ctrl+d":
		if m.notesDeleteSel() {
			return
		}
		m.notesDelete()
		return
	case "left", "shift+left":
		m.notesMove(-1, extend)
		return
	case "right", "shift+right":
		m.notesMove(1, extend)
		return
	case "up", "shift+up":
		m.notesMoveVert(-1, extend)
		return
	case "down", "shift+down":
		m.notesMoveVert(1, extend)
		return
	case "home", "shift+home":
		m.notesHome(extend)
		return
	case "end", "shift+end":
		m.notesEnd(extend)
		return
	case "ctrl+home", "ctrl+shift+home":
		m.notesDocHome(strings.Contains(s, "shift"))
		return
	case "ctrl+end", "ctrl+shift+end":
		m.notesDocEnd(strings.Contains(s, "shift"))
		return
	case "ctrl+left", "ctrl+shift+left":
		m.notesWordMove(-1, strings.Contains(s, "shift"))
		return
	case "ctrl+right", "ctrl+shift+right":
		m.notesWordMove(1, strings.Contains(s, "shift"))
		return
	default:
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 0 {
			var b strings.Builder
			for _, r := range msg.Runes {
				if r == '\t' || r >= 32 {
					b.WriteRune(r)
				}
			}
			if b.Len() > 0 {
				m.notesInsert(b.String())
			}
		}
	}
}

// handleNotesClick maps an overlay cell click into list / title / editor.
func (m *Model) handleNotesClick(cellX, cellY, cols int) {
	if !m.NotesOpen {
		return
	}
	m.computeNotesLayout(cols)
	lay := m.notesLayout
	if !lay.Valid {
		return
	}
	if cellX < lay.CardLeft || cellX >= lay.CardRight {
		return
	}

	// Editor screen: title row renames; body keeps/focuses editor.
	if !lay.ListMode {
		if lay.NameY >= 0 && (cellY == lay.NameY || cellY == lay.TitleY) {
			m.beginNotesTitleEdit()
			return
		}
		if m.notesFocus == notesFocusTitle {
			m.commitNotesTitle(true)
		}
		m.notesFocus = notesFocusEditor
		return
	}

	// List screen: click a row to select + open editor.
	if cellY < lay.BodyY0 || cellY >= lay.BodyY0+lay.BodyRows {
		return
	}
	row := cellY - lay.BodyY0
	start := 0
	if m.notesActive >= notesListRows {
		start = m.notesActive - notesListRows + 1
	}
	idx := start + row
	if idx >= 0 && idx < len(m.notesBank) {
		m.notesSelect(idx)
		m.notesFocus = notesFocusEditor
	}
}

func (m *Model) notesInsert(s string) {
	if s == "" {
		return
	}
	_ = m.notesDeleteSel()
	rs := []rune(s)
	if len(m.notesRunes)+len(rs) > notesMaxRunes {
		room := notesMaxRunes - len(m.notesRunes)
		if room <= 0 {
			return
		}
		rs = rs[:room]
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
	m.notesSel = -1
	m.notesDirty = true
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesBackspace() {
	if m.notesCursor <= 0 {
		return
	}
	i := m.notesCursor
	m.notesRunes = append(m.notesRunes[:i-1], m.notesRunes[i:]...)
	m.notesCursor = i - 1
	m.notesSel = -1
	m.notesDirty = true
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesDelete() {
	if m.notesCursor >= len(m.notesRunes) {
		return
	}
	i := m.notesCursor
	m.notesRunes = append(m.notesRunes[:i], m.notesRunes[i+1:]...)
	m.notesSel = -1
	m.notesDirty = true
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesBeginExtend(extend bool) {
	if extend {
		if m.notesSel < 0 {
			m.notesSel = m.notesCursor
		}
	} else {
		m.notesClearSel()
	}
}

func (m *Model) notesMove(delta int, extend bool) {
	m.notesBeginExtend(extend)
	m.notesCursor += delta
	m.notesClampCursor()
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesHome(extend bool) {
	m.notesBeginExtend(extend)
	i := m.notesCursor
	for i > 0 && m.notesRunes[i-1] != '\n' {
		i--
	}
	m.notesCursor = i
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesEnd(extend bool) {
	m.notesBeginExtend(extend)
	i := m.notesCursor
	for i < len(m.notesRunes) && m.notesRunes[i] != '\n' {
		i++
	}
	m.notesCursor = i
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesDocHome(extend bool) {
	m.notesBeginExtend(extend)
	m.notesCursor = 0
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesDocEnd(extend bool) {
	m.notesBeginExtend(extend)
	m.notesCursor = len(m.notesRunes)
	m.notesEnsureCursorVisible(0)
}

func (m *Model) notesWordMove(dir int, extend bool) {
	m.notesBeginExtend(extend)
	i := m.notesCursor
	n := len(m.notesRunes)
	if dir < 0 {
		for i > 0 && isNotesSpace(m.notesRunes[i-1]) {
			i--
		}
		for i > 0 && !isNotesSpace(m.notesRunes[i-1]) {
			i--
		}
	} else {
		for i < n && !isNotesSpace(m.notesRunes[i]) {
			i++
		}
		for i < n && isNotesSpace(m.notesRunes[i]) {
			i++
		}
	}
	m.notesCursor = i
	m.notesEnsureCursorVisible(0)
}

func isNotesSpace(r rune) bool {
	return r == ' ' || r == '\t' || r == '\n'
}

func (m *Model) notesMoveVert(dir int, extend bool) {
	m.notesBeginExtend(extend)
	w := m.notesWrapW
	if w < 8 {
		w = 40
	}
	lines := notesSoftLines(m.notesRunes, w)
	row, visCol := notesCursorRowCol(m.notesRunes, lines, m.notesCursor)
	row += dir
	if row < 0 {
		m.notesCursor = 0
		m.notesEnsureCursorVisible(w)
		return
	}
	if row >= len(lines) {
		m.notesCursor = len(m.notesRunes)
		m.notesEnsureCursorVisible(w)
		return
	}
	ln := lines[row]
	if visCol > ln.width {
		visCol = ln.width
	}
	m.notesCursor = notesIndexAtVisual(m.notesRunes, ln.start, ln.end, visCol)
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
	row, _ := notesCursorRowCol(m.notesRunes, lines, m.notesCursor)
	if row < m.notesScroll {
		m.notesScroll = row
	}
	if row >= m.notesScroll+notesListRows {
		m.notesScroll = row - notesListRows + 1
	}
	if m.notesScroll < 0 {
		m.notesScroll = 0
	}
}

// NotesPaste inserts clipboard text (replaces selection).
func (m *Model) NotesPaste(s string) {
	if s == "" {
		return
	}
	if m.notesFocus == notesFocusTitle {
		// Paste into title field.
		s = strings.ReplaceAll(s, "\r\n", " ")
		s = strings.ReplaceAll(s, "\n", " ")
		s = strings.ReplaceAll(s, "\r", " ")
		m.notesTitle += s
		return
	}
	if m.notesFocus != notesFocusEditor {
		m.notesFocus = notesFocusEditor
	}
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	m.notesInsert(s)
}

// NotesCutSelection removes selection after host copied it. Returns false if none.
func (m *Model) NotesCutSelection() bool {
	return m.notesDeleteSel()
}

type notesLine struct {
	start, end int
	width      int
	hard       bool
}

func notesTabWidth(col int) int {
	stop := notesTabStop
	if stop < 1 {
		stop = 4
	}
	return stop - (col % stop)
}

func notesDisplayWidth(r rune, col int) int {
	if r == '\t' {
		return notesTabWidth(col)
	}
	return 1
}

func notesExpandTabs(rs []rune, startCol int) string {
	if len(rs) == 0 {
		return ""
	}
	var b strings.Builder
	col := startCol
	for _, r := range rs {
		if r == '\t' {
			w := notesTabWidth(col)
			b.WriteString(strings.Repeat(" ", w))
			col += w
			continue
		}
		b.WriteRune(r)
		col++
	}
	return b.String()
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
		w := notesDisplayWidth(runes[i], col)
		if col > 0 && col+w > width {
			out = append(out, notesLine{start: start, end: i, width: col, hard: false})
			start = i
			col = 0
			w = notesDisplayWidth(runes[i], col)
		}
		if w > width {
			w = width
		}
		col += w
	}
	out = append(out, notesLine{start: start, end: len(runes), width: col, hard: false})
	return out
}

func notesCursorRowCol(runes []rune, lines []notesLine, cursor int) (row, visCol int) {
	if len(lines) == 0 {
		return 0, 0
	}
	for i, ln := range lines {
		if cursor >= ln.start && cursor <= ln.end {
			if cursor == ln.end && !ln.hard && i+1 < len(lines) && lines[i+1].start == ln.end {
				continue
			}
			return i, notesVisualCol(runes, ln.start, cursor)
		}
	}
	last := lines[len(lines)-1]
	return len(lines) - 1, last.width
}

func notesVisualCol(runes []rune, start, at int) int {
	col := 0
	if at < start {
		return 0
	}
	if at > len(runes) {
		at = len(runes)
	}
	for i := start; i < at; i++ {
		if runes[i] == '\n' {
			break
		}
		col += notesDisplayWidth(runes[i], col)
	}
	return col
}

func notesIndexAtVisual(runes []rune, start, end, target int) int {
	if target <= 0 {
		return start
	}
	col := 0
	for i := start; i < end; i++ {
		w := notesDisplayWidth(runes[i], col)
		if col+w > target {
			return i
		}
		col += w
		if col == target {
			return i + 1
		}
	}
	return end
}

func notesRuneOffset(lines []notesLine, row, cursor int) int {
	if row < 0 || row >= len(lines) {
		return 0
	}
	ln := lines[row]
	if cursor < ln.start {
		return 0
	}
	if cursor > ln.end {
		return ln.end - ln.start
	}
	return cursor - ln.start
}

func (m Model) renderNotes(windowCols int) string {
	// Two separate screens — never list+editor side by side.
	if m.notesFocus == notesFocusList {
		return m.renderNotesListScreen(windowCols)
	}
	return m.renderNotesEditorScreen(windowCols)
}

func (m Model) renderNotesListScreen(windowCols int) string {
	outer := clampDialogWidth(notesDialogWant, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}

	listStart := 0
	if m.notesActive >= notesListRows {
		listStart = m.notesActive - notesListRows + 1
	}

	var body []string
	itemRows := 0
	if len(m.notesBank) == 0 {
		body = append(body, styleDialogHint().Width(inner).MaxHeight(1).Padding(0, 1).
			Render(padFit("No notes — press n to create one", inner-2)))
		itemRows = 1
	} else {
		for i := 0; i < notesListRows && listStart+i < len(m.notesBank); i++ {
			body = append(body, m.renderNotesListRow(inner, listStart+i))
			itemRows++
		}
	}
	blank := styleDialogValue().Width(inner).MaxHeight(1).
		Render(strings.Repeat(" ", inner))
	for itemRows < notesListRows {
		body = append(body, blank)
		itemRows++
	}

	title := "Notes"
	if n := len(m.notesBank); n > 0 {
		title = fmt.Sprintf("Notes  (%d)", n)
	}
	return renderDialogCard(outer, title, body, m.renderNotesFooter())
}

func (m Model) renderNotesListRow(inner, bankIdx int) string {
	if bankIdx < 0 || bankIdx >= len(m.notesBank) {
		return styleDialogValue().Width(inner).MaxHeight(1).
			Render(strings.Repeat(" ", inner))
	}
	n := m.notesBank[bankIdx]
	title := NoteDisplayTitle(n)
	prev := strings.TrimSpace(n.Body)
	if i := strings.IndexByte(prev, '\n'); i >= 0 {
		prev = strings.TrimSpace(prev[:i])
	}
	// Don't repeat the title as preview.
	if prev == title {
		prev = ""
	}
	if prev != "" {
		prev = truncateNoteTitle(prev, 36)
	}

	active := bankIdx == m.notesActive
	prefix := "  "
	if active {
		prefix = "▸ "
	}
	// Title · preview  (palette-style single row)
	label := prefix + title
	if prev != "" {
		label = prefix + title + "  ·  " + prev
	}
	label = padFit(label, inner-2)
	if active {
		return styleDialogActive().Width(inner).MaxHeight(1).Render(label)
	}
	return styleDialogNormalItem().Width(inner).MaxHeight(1).Render(label)
}

func (m Model) renderNotesEditorScreen(windowCols int) string {
	outer := clampDialogWidth(notesDialogWant, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}
	wrapW := inner - 2
	if wrapW < 8 {
		wrapW = 8
	}

	nameLine := m.renderNotesNameRow(inner)
	edLines := notesSoftLines(m.notesRunes, wrapW)
	scroll := m.notesScroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > 0 && scroll >= len(edLines) {
		scroll = len(edLines) - 1
	}
	crow, _ := notesCursorRowCol(m.notesRunes, edLines, m.notesCursor)
	caretOff := notesRuneOffset(edLines, crow, m.notesCursor)
	selLo, selHi := 0, 0
	hasSel := m.notesSel >= 0 && m.notesSel != m.notesCursor
	if hasSel {
		selLo, selHi = m.notesSel, m.notesCursor
		if selLo > selHi {
			selLo, selHi = selHi, selLo
		}
	}

	var body []string
	body = append(body, nameLine)
	for i := 0; i < notesListRows; i++ {
		ei := scroll + i
		if ei < len(edLines) {
			ln := edLines[ei]
			isCur := m.notesFocus == notesFocusEditor && ei == crow
			body = append(body, m.renderNotesEditorLine(ln, isCur, caretOff, hasSel, selLo, selHi, inner, wrapW))
			continue
		}
		if ei == 0 && len(m.notesRunes) == 0 && m.notesFocus == notesFocusEditor {
			body = append(body, styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).
				Render(padFit(notesCaretChar, wrapW)))
			continue
		}
		if ei == 0 && len(m.notesRunes) == 0 {
			body = append(body, styleDialogHint().Width(inner).MaxHeight(1).Padding(0, 1).
				Render(padFit("start typing…", wrapW)))
			continue
		}
		body = append(body, styleDialogValue().Width(inner).MaxHeight(1).
			Render(strings.Repeat(" ", inner)))
	}

	cardTitle := "Note"
	if len(m.notesBank) > 0 && m.notesActive >= 0 && m.notesActive < len(m.notesBank) {
		cardTitle = NoteDisplayTitle(m.notesBank[m.notesActive])
	}
	return renderDialogCard(outer, cardTitle, body, m.renderNotesFooter())
}

// computeNotesLayout fills m.notesLayout for click tests (list vs editor screen).
func (m *Model) computeNotesLayout(windowCols int) {
	if windowCols < 20 {
		windowCols = 20
	}
	outer := clampDialogWidth(notesDialogWant, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}
	cardLeft := (windowCols - outer) / 2
	if cardLeft < 0 {
		cardLeft = 0
	}
	listMode := m.notesFocus == notesFocusList
	// Overlay y: border, title, rule, then body…
	// List: body starts at y=3 (title, rule, items…)
	// Editor: name at y=3, body at y=4
	nameY := -1
	bodyY0 := 3
	bodyRows := notesListRows
	if !listMode {
		nameY = 3
		bodyY0 = 4
	}
	m.notesLayout = notesLayout{
		Cols:      windowCols,
		Outer:     outer,
		Inner:     inner,
		TitleY:    1,
		NameY:     nameY,
		BodyY0:    bodyY0,
		BodyRows:  bodyRows,
		CardLeft:  cardLeft,
		CardRight: cardLeft + outer,
		ListMode:  listMode,
		Valid:     true,
	}
	m.notesWrapW = inner - 2
	if m.notesWrapW < 8 {
		m.notesWrapW = 8
	}
}

func (m Model) renderNotesNameRow(inner int) string {
	focused := m.notesFocus == notesFocusTitle
	if focused {
		body := m.notesTitle
		if body == "" {
			p := styleDialogHintKey().Render("title ")
			rest := styleDialogHint().Render(padFit("name…"+notesCaretChar, inner-lipgloss.Width("title ")-2))
			return panelFillLine(inner, p+rest)
		}
		plain := padFit("title "+body+notesCaretChar, inner)
		return styleDialogHintKey().Width(inner).MaxHeight(1).Render(plain)
	}
	label := "Untitled"
	if len(m.notesBank) > 0 && m.notesActive >= 0 && m.notesActive < len(m.notesBank) {
		label = NoteDisplayTitle(m.notesBank[m.notesActive])
	}
	hint := "  · click / f2 rename"
	budget := inner - 2
	if budget < 8 {
		budget = 8
	}
	maxLabel := budget - len([]rune(hint))
	if maxLabel < 4 {
		maxLabel = 4
		hint = ""
	}
	label = truncateNoteTitle(label, maxLabel)
	plain := padFit(label+hint, budget)
	return styleDialogLabel().Width(inner).MaxHeight(1).Padding(0, 1).Render(plain)
}

func (m Model) renderNotesEditorLine(ln notesLine, isCursorRow bool, caretOff int, hasSel bool, selLo, selHi, inner, wrapW int) string {
	seg := m.notesRunes[ln.start:ln.end]
	var parts []string
	if !isCursorRow {
		parts = append(parts, m.notesStyledSpan(seg, hasSel, selLo, selHi, ln.start, 0))
	} else {
		pos := ln.start
		for i := 0; i <= len(seg); i++ {
			abs := ln.start + i
			if i == caretOff {
				if abs > pos {
					vis := notesVisualCol(m.notesRunes, ln.start, pos)
					parts = append(parts, m.notesStyledSpan(m.notesRunes[pos:abs], hasSel, selLo, selHi, pos, vis))
					pos = abs
				}
				parts = append(parts, styleDialogHintKey().Render(notesCaretChar))
			}
			if i == len(seg) {
				break
			}
		}
		if pos < ln.end {
			vis := notesVisualCol(m.notesRunes, ln.start, pos)
			parts = append(parts, m.notesStyledSpan(m.notesRunes[pos:ln.end], hasSel, selLo, selHi, pos, vis))
		}
	}
	content := strings.Join(parts, "")
	w := lipgloss.Width(content)
	if w < wrapW {
		content += styleDialogValue().Render(strings.Repeat(" ", wrapW-w))
	}
	return styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).Render(content)
}

func (m Model) notesStyledSpan(rs []rune, hasSel bool, selLo, selHi, absStart, startCol int) string {
	if len(rs) == 0 {
		return ""
	}
	if !hasSel {
		return styleDialogValue().Render(notesExpandTabs(rs, startCol))
	}
	var b strings.Builder
	i := 0
	col := startCol
	for i < len(rs) {
		abs := absStart + i
		inSel := abs >= selLo && abs < selHi
		j := i + 1
		for j < len(rs) {
			a := absStart + j
			if (a >= selLo && a < selHi) != inSel {
				break
			}
			j++
		}
		chunk := notesExpandTabs(rs[i:j], col)
		for k := i; k < j; k++ {
			col += notesDisplayWidth(rs[k], col)
		}
		if inSel {
			b.WriteString(styleDialogActive().Render(chunk))
		} else {
			b.WriteString(styleDialogValue().Render(chunk))
		}
		i = j
	}
	return b.String()
}

func (m Model) renderNotesFooter() string {
	switch m.notesFocus {
	case notesFocusTitle:
		return styleDialogHintKey().Render("enter") +
			styleDialogHint().Render(" save name  ") +
			styleDialogHintKey().Render("esc") +
			styleDialogHint().Render(" cancel")
	case notesFocusList:
		return styleDialogHintKey().Render("↑↓") +
			styleDialogHint().Render(" move  ") +
			styleDialogHintKey().Render("enter") +
			styleDialogHint().Render(" open  ") +
			styleDialogHintKey().Render("n") +
			styleDialogHint().Render(" new  ") +
			styleDialogHintKey().Render("d") +
			styleDialogHint().Render(" delete  ") +
			styleDialogHintKey().Render("esc") +
			styleDialogHint().Render(" close")
	default:
		return styleDialogHintKey().Render("esc") +
			styleDialogHint().Render(" back to list  ") +
			styleDialogHintKey().Render("f2") +
			styleDialogHint().Render(" rename  ") +
			styleDialogHintKey().Render("ctrl+a/c/x/v") +
			styleDialogHint().Render(" edit")
	}
}

