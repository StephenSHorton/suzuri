package chrome

import (
	"fmt"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// Notes: multi-note bank, floating card, persisted via notes.json (host).

const (
	notesVisRows   = 11
	notesMaxRunes  = 64 * 1024
	notesCaretChar = "▌"
	notesTabStop   = 4 // display width advances to next multiple of this
	notesMaxBank   = 48
)

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
	m.flushActiveNote()
	m.NotesOpen = false
}

// NotesSnapshot returns the bank for disk save (flushes editor first).
func (m *Model) NotesSnapshot() NotesBank {
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

func (m *Model) notesNew() {
	if len(m.notesBank) >= notesMaxBank {
		return
	}
	m.flushActiveNote()
	n := newNoteDoc("", "")
	m.notesBank = append(m.notesBank, n)
	m.notesActive = len(m.notesBank) - 1
	m.loadActiveNoteIntoEditor()
	m.notesDirty = true
}

func (m *Model) notesDeleteActive() {
	if len(m.notesBank) <= 1 {
		// Clear sole note instead of deleting the bank.
		m.notesRunes = nil
		m.notesCursor = 0
		m.notesSel = -1
		m.notesScroll = 0
		m.flushActiveNote()
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
}

func (m *Model) notesSwitch(delta int) {
	if len(m.notesBank) < 2 {
		return
	}
	m.flushActiveNote()
	n := len(m.notesBank)
	m.notesActive = (m.notesActive + delta%n + n) % n
	m.loadActiveNoteIntoEditor()
	m.notesDirty = true // active_id change
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
	extend := strings.HasPrefix(s, "shift+")

	switch s {
	case "esc":
		m.putAwayNotes()
		return
	case "ctrl+n":
		m.notesNew()
		return
	case "ctrl+pgup":
		m.notesSwitch(-1)
		return
	case "ctrl+pgdown":
		m.notesSwitch(1)
		return
	case "ctrl+shift+backspace":
		m.notesDeleteActive()
		return
	case "ctrl+a":
		// Select all
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
		// Copy is handled by the host (needs clipboard). Leave selection.
		return
	case "ctrl+x":
		// Cut: host copies then we delete selection; if host didn't, delete anyway.
		if m.notesHasSel() {
			m.notesDeleteSel()
		}
		return
	case "enter", "ctrl+m":
		m.notesInsert("\n")
		return
	case "tab", "shift+tab":
		// Real tab character (shift+tab same for now; outdent later).
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
		// Esc puts away; ctrl+c is copy (host clipboard), not close.
		// Keep \t as a real tab; drop other C0 controls.
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

func (m *Model) notesInsert(s string) {
	if s == "" {
		return
	}
	// Replace selection if any.
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
		// Skip spaces left, then word chars left.
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
	if row >= m.notesScroll+notesVisRows {
		m.notesScroll = row - notesVisRows + 1
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
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	m.notesInsert(s)
}

// NotesCutSelection removes selection after host copied it. Returns false if none.
func (m *Model) NotesCutSelection() bool {
	return m.notesDeleteSel()
}

type notesLine struct {
	start, end int // half-open index into runes
	width      int // visual columns
	hard       bool
}

// notesTabWidth is how many display columns a tab occupies at visual col.
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

// notesExpandTabs turns tabs into spaces for display only (buffer keeps \t).
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
		// Soft-wrap before this rune if it won't fit (unless line is empty).
		if col > 0 && col+w > width {
			out = append(out, notesLine{start: start, end: i, width: col, hard: false})
			start = i
			col = 0
			w = notesDisplayWidth(runes[i], col)
		}
		// Pathological: single tab wider than width — still emit one cell.
		if w > width {
			w = width
		}
		col += w
	}
	// After a trailing hard newline, start == len and this emits the blank
	// line the caret sits on. Do not add a second empty row for "\n" ends —
	// that made Enter look like a double newline with typing on the prior line.
	out = append(out, notesLine{start: start, end: len(runes), width: col, hard: false})
	return out
}

// notesCursorRowCol returns soft-line row and visual column of cursor.
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

// notesVisualCol is display column of index `at` within a soft line starting at start.
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

// notesIndexAtVisual maps a visual column to a rune index in [start,end].
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

// notesRuneOffset is cursor's index into a soft line's runes (for caret paint).
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
	outer := clampDialogWidth(56, windowCols)
	inner := dialogInnerWidth(outer)
	if inner < 20 {
		inner = 20
	}
	wrapW := inner - 2
	if wrapW < 8 {
		wrapW = 8
	}

	title := "Notes"
	if len(m.notesBank) > 0 && m.notesActive >= 0 && m.notesActive < len(m.notesBank) {
		title = "Notes · " + NoteDisplayTitle(m.notesBank[m.notesActive])
		if len(m.notesBank) > 1 {
			title += fmt.Sprintf("  (%d/%d)", m.notesActive+1, len(m.notesBank))
		}
	}

	lines := notesSoftLines(m.notesRunes, wrapW)
	scroll := m.notesScroll
	if scroll < 0 {
		scroll = 0
	}
	if scroll > 0 && scroll >= len(lines) {
		scroll = len(lines) - 1
	}
	row, _ := notesCursorRowCol(m.notesRunes, lines, m.notesCursor)
	caretOff := notesRuneOffset(lines, row, m.notesCursor)
	selLo, selHi := 0, 0
	hasSel := m.notesSel >= 0 && m.notesSel != m.notesCursor
	if hasSel {
		selLo, selHi = m.notesSel, m.notesCursor
		if selLo > selHi {
			selLo, selHi = selHi, selLo
		}
	}

	var body []string
	body = append(body, m.renderNotesBankStrip(inner))
	end := scroll + notesVisRows
	if end > len(lines) {
		end = len(lines)
	}
	for i := scroll; i < end; i++ {
		ln := lines[i]
		line := m.renderNotesLine(ln, i == row, caretOff, hasSel, selLo, selHi, wrapW)
		body = append(body, line)
	}
	for len(body) < notesVisRows+1 {
		body = append(body, styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).
			Render(padFit("", wrapW)))
	}
	if len(m.notesRunes) == 0 && row == 0 {
		// body[0] is bank strip; editor starts at body[1]
		if len(body) > 1 {
			body[1] = styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).
				Render(padFit(notesCaretChar, wrapW))
		}
		if len(body) > 2 {
			body[2] = styleDialogHint().Width(inner).MaxHeight(1).Padding(0, 1).
				Render(padFit("type a note — saved to disk · ctrl+n new", wrapW))
		}
	}
	footer := styleDialogHintKey().Render("esc") +
		styleDialogHint().Render(" put away  ") +
		styleDialogHintKey().Render("ctrl+n") +
		styleDialogHint().Render(" new  ") +
		styleDialogHintKey().Render("ctrl+pgup/dn") +
		styleDialogHint().Render(" switch  ") +
		styleDialogHintKey().Render("ctrl+a/c/x/v") +
		styleDialogHint().Render(" edit")
	return renderDialogCard(outer, title, body, footer)
}

func (m Model) renderNotesBankStrip(inner int) string {
	if inner < 8 {
		inner = 8
	}
	budget := inner - 2
	if budget < 8 {
		budget = 8
	}
	var parts []string
	used := 0
	for i, n := range m.notesBank {
		label := NoteDisplayTitle(n)
		if len([]rune(label)) > 14 {
			label = string([]rune(label)[:13]) + "…"
		}
		var chip string
		if i == m.notesActive {
			chip = styleDialogActive().Render(" " + label + " ")
		} else {
			chip = styleDialogHint().Render(" " + label + " ")
		}
		w := lipgloss.Width(chip)
		if used > 0 && used+w > budget {
			more := styleDialogHint().Render(fmt.Sprintf(" +%d", len(m.notesBank)-i))
			parts = append(parts, more)
			break
		}
		parts = append(parts, chip)
		used += w
	}
	row := strings.Join(parts, "")
	if lipgloss.Width(row) < budget {
		row += styleDialogValue().Render(strings.Repeat(" ", budget-lipgloss.Width(row)))
	}
	return styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).Render(row)
}

func (m Model) renderNotesLine(ln notesLine, isCursorRow bool, caretOff int, hasSel bool, selLo, selHi, wrapW int) string {
	inner := wrapW + 2
	seg := m.notesRunes[ln.start:ln.end]
	// Paint selection relative to this line's range; expand tabs for display.
	var parts []string
	if !isCursorRow {
		parts = append(parts, m.notesStyledSpan(seg, hasSel, selLo, selHi, ln.start, 0))
	} else {
		pos := ln.start
		vis := 0
		for i := 0; i <= len(seg); i++ {
			abs := ln.start + i
			if i == caretOff {
				if abs > pos {
					parts = append(parts, m.notesStyledSpan(m.notesRunes[pos:abs], hasSel, selLo, selHi, pos, vis))
					vis = notesVisualCol(m.notesRunes, ln.start, abs)
					pos = abs
				}
				parts = append(parts, styleDialogHintKey().Render(notesCaretChar))
			}
			if i == len(seg) {
				break
			}
		}
		if pos < ln.end {
			vis = notesVisualCol(m.notesRunes, ln.start, pos)
			parts = append(parts, m.notesStyledSpan(m.notesRunes[pos:ln.end], hasSel, selLo, selHi, pos, vis))
		}
	}
	content := strings.Join(parts, "")
	// Ensure full width panel fill.
	w := lipgloss.Width(content)
	if w < wrapW {
		content += styleDialogValue().Render(strings.Repeat(" ", wrapW-w))
	}
	return styleDialogValue().Width(inner).MaxHeight(1).Padding(0, 1).Render(
		// Content already styled; padFit would strip ANSI — use as-is with MaxWidth.
		content,
	)
}

// notesStyledSpan styles a contiguous span, splitting on selection boundaries.
// Tabs are expanded to spaces for paint only; absStart is the buffer index of rs[0].
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
